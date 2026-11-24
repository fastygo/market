// Package fakegw is an in-memory payments.Gateway for tests and local
// development (MARKET_FAKE_GATEWAY=1). It behaves like a real provider in
// the ways the checkout protocol depends on: CreatePayment is idempotent by
// key, FetchPayment is the source of truth, webhooks need a signature. Every
// call fails with tx.ErrNetworkInTx inside a write transaction, and tests
// can inject faults or block calls through BeforeCall.
package fakegw

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/platform/tx"
)

// Code of the fake provider.
const Code payments.ProviderCode = "fake"

// SignatureHeader carries hex HMAC-SHA256 of the webhook body.
const SignatureHeader = "X-Fake-Signature"

// Gateway is the fake provider.
type Gateway struct {
	// Secret signs webhooks.
	Secret []byte
	// ConfirmBase prefixes confirmation URLs, e.g. "/fakepay/".
	ConfirmBase string
	// BeforeCall runs before every operation (outside the gateway lock);
	// returning an error fails the call.
	BeforeCall func(ctx context.Context, op string) error

	mu       sync.Mutex
	byKey    map[string]string
	payments map[string]*payments.Status
	calls    map[string]int
}

// New returns an empty fake gateway.
func New(secret []byte) *Gateway {
	return &Gateway{Secret: secret, ConfirmBase: "/fakepay/", byKey: map[string]string{}, payments: map[string]*payments.Status{}, calls: map[string]int{}}
}

var _ payments.Gateway = (*Gateway)(nil)

// Code implements payments.Gateway.
func (g *Gateway) Code() payments.ProviderCode { return Code }

// Methods implements payments.Gateway.
func (g *Gateway) Methods() []payments.Method { return []payments.Method{"card"} }

func (g *Gateway) enter(ctx context.Context, op string) error {
	if err := tx.AssertNoTx(ctx); err != nil {
		return err
	}
	g.mu.Lock()
	g.calls[op]++
	g.mu.Unlock()
	if g.BeforeCall != nil {
		return g.BeforeCall(ctx, op)
	}
	return nil
}

// Calls returns how many times op was called.
func (g *Gateway) Calls(op string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[op]
}

// Payments returns the number of distinct payments created.
func (g *Gateway) Payments() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.payments)
}

// CreatePayment implements payments.Gateway; idempotent by key.
func (g *Gateway) CreatePayment(ctx context.Context, req payments.CreatePaymentRequest) (payments.CreatePaymentResult, error) {
	if err := g.enter(ctx, "create"); err != nil {
		return payments.CreatePaymentResult{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	id, ok := g.byKey[req.IdempotencyKey]
	if !ok {
		sum := sha256.Sum256([]byte(req.IdempotencyKey))
		id = "fake_" + hex.EncodeToString(sum[:8])
		g.byKey[req.IdempotencyKey] = id
		g.payments[id] = &payments.Status{ProviderPaymentID: id, State: payments.StatePending, Amount: req.Amount}
	}
	st := *g.payments[id]
	return payments.CreatePaymentResult{ProviderPaymentID: id, ConfirmationURL: g.ConfirmBase + id, Status: st}, nil
}

// FetchPayment implements payments.Gateway.
func (g *Gateway) FetchPayment(ctx context.Context, id string) (payments.Status, error) {
	if err := g.enter(ctx, "fetch"); err != nil {
		return payments.Status{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.payments[id]
	if !ok {
		return payments.Status{}, payments.ErrNotFound
	}
	return *p, nil
}

// CancelPayment implements payments.Gateway.
func (g *Gateway) CancelPayment(ctx context.Context, id, _ string) (payments.Status, error) {
	if err := g.enter(ctx, "cancel"); err != nil {
		return payments.Status{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.payments[id]
	if !ok {
		return payments.Status{}, payments.ErrNotFound
	}
	if p.State == payments.StatePending {
		p.State = payments.StateCanceled
	}
	return *p, nil
}

// Refund implements payments.Gateway (full refund).
func (g *Gateway) Refund(ctx context.Context, req payments.RefundRequest) (payments.RefundResult, error) {
	if err := g.enter(ctx, "refund"); err != nil {
		return payments.RefundResult{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.payments[req.ProviderPaymentID]
	if !ok {
		return payments.RefundResult{}, payments.ErrNotFound
	}
	p.State = payments.StateRefunded
	return payments.RefundResult{ProviderRefundID: "refund_" + req.ProviderPaymentID, State: payments.StateSucceeded}, nil
}

// SetState is the provider side of the fake: the buyer paid, the payment
// failed, a late success arrived.
func (g *Gateway) SetState(id string, s payments.State) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.payments[id]
	if ok {
		p.State = s
	}
	return ok
}

type webhookBody struct {
	EventID   string `json:"event_id"`
	Type      string `json:"type"`
	PaymentID string `json:"payment_id"`
}

// Webhook builds a signed webhook body for a payment state change.
func (g *Gateway) Webhook(eventID, paymentID string, s payments.State) (body []byte, signature string) {
	body, _ = json.Marshal(webhookBody{EventID: eventID, Type: "payment." + string(s), PaymentID: paymentID})
	return body, g.sign(body)
}

func (g *Gateway) sign(body []byte) string {
	m := hmac.New(sha256.New, g.Secret)
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// ParseWebhook implements payments.Gateway: verify only, no side effects.
func (g *Gateway) ParseWebhook(ctx context.Context, r *http.Request) (payments.Event, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return payments.Event{}, err
	}
	if !hmac.Equal([]byte(r.Header.Get(SignatureHeader)), []byte(g.sign(body))) {
		return payments.Event{}, payments.ErrInvalidSignature
	}
	var b webhookBody
	if err := json.Unmarshal(body, &b); err != nil || b.EventID == "" || b.PaymentID == "" {
		return payments.Event{}, payments.ErrInvalidSignature
	}
	return payments.Event{EventID: b.EventID, Type: b.Type, ProviderPaymentID: b.PaymentID, Payload: body}, nil
}
