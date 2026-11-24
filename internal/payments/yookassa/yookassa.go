// Package yookassa is the YooKassa (API v3) payments.Gateway adapter
// (ADR-0024). Credentials come only from config built from env (ADR-0016);
// they are sent as HTTP Basic auth and never logged. A test store and a
// live store differ only in shop id and secret key.
//
// Protocol notes:
//   - one-stage payments (capture=true), confirmation type redirect;
//   - Idempotence-Key is sha256(hex) of the engine key: deterministic per
//     attempt and within YooKassa's 64-character limit;
//   - receipts ("Receipts from YooKassa", 54-FZ) are sent with the payment
//     when enabled; the return receipt of a full refund is formed by
//     YooKassa automatically unless RefundReceipts is set;
//   - notifications carry no signature: the sender IP may be checked
//     against the published list, and the object is always re-fetched with
//     GET before the event is accepted; the status comes from FetchPayment.
package yookassa

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/platform/money"
	"github.com/fastygo/market/internal/platform/tx"
	"github.com/fastygo/market/internal/taxdocs"
)

// Code of the provider.
const Code payments.ProviderCode = "yookassa"

// DefaultBaseURL is the API v3 endpoint (test and live stores alike).
const DefaultBaseURL = "https://api.yookassa.ru/v3"

// NotificationNets is the published list of addresses YooKassa sends
// notifications from (developer docs, "Incoming notifications").
var NotificationNets = []string{
	"185.71.76.0/27", "185.71.77.0/27", "77.75.153.0/25", "77.75.156.11/32",
	"77.75.156.35/32", "77.75.154.128/25", "2a02:5180::/32",
}

// DefaultMethods maps engine method codes to YooKassa payment_method_data
// types; a method that is not listed lets the buyer choose on the
// YooKassa page.
var DefaultMethods = map[payments.Method]string{"card": "bank_card", "sbp": "sbp", "yoo_money": "yoo_money"}

// Config of the adapter.
type Config struct {
	ShopID    string
	SecretKey string
	BaseURL   string       // default DefaultBaseURL
	HTTP      *http.Client // default: 20 s timeout

	// Receipts sends the sale receipt with every payment (54-FZ through
	// "Receipts from YooKassa"); requires VATCode.
	Receipts bool
	// RefundReceipts sends the return receipt with refunds (only for the
	// "payment first, then receipt" scheme; the default schemes form the
	// return receipt of a full refund automatically).
	RefundReceipts bool
	// VATCode maps a document line tax to the receipt vat_code (region
	// pack, e.g. ru.YooKassaVATCode).
	VATCode func(taxMode, taxCode string) (int, error)

	// CheckIP rejects notifications from addresses outside
	// NotificationNets. ClientIP returns the sender address (behind a
	// proxy: the trusted forwarded header); default r.RemoteAddr.
	CheckIP  bool
	ClientIP func(r *http.Request) string

	// Methods overrides DefaultMethods.
	Methods map[payments.Method]string
}

// Gateway is the YooKassa adapter.
type Gateway struct {
	cfg  Config
	nets []netip.Prefix
}

var _ payments.Gateway = (*Gateway)(nil)

// New validates the config. Secrets are not part of any error text.
func New(cfg Config) (*Gateway, error) {
	if strings.TrimSpace(cfg.ShopID) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, errors.New("yookassa: shop id and secret key are required")
	}
	if cfg.Receipts && cfg.VATCode == nil {
		return nil, errors.New("yookassa: receipts need a VAT code mapping")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 20 * time.Second}
	}
	if cfg.Methods == nil {
		cfg.Methods = DefaultMethods
	}
	g := &Gateway{cfg: cfg}
	for _, n := range NotificationNets {
		g.nets = append(g.nets, netip.MustParsePrefix(n))
	}
	return g, nil
}

// Code implements payments.Gateway.
func (g *Gateway) Code() payments.ProviderCode { return Code }

// Methods implements payments.Gateway.
func (g *Gateway) Methods() []payments.Method {
	out := make([]payments.Method, 0, len(g.cfg.Methods))
	for m := range g.cfg.Methods {
		out = append(out, m)
	}
	return out
}

// IdempotenceKey derives the provider key from an engine key: the same
// attempt always sends the same key, and it fits the 64-character limit.
func IdempotenceKey(engineKey string) string {
	sum := sha256.Sum256([]byte(engineKey))
	return hex.EncodeToString(sum[:])
}

type amount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

type payment struct {
	ID             string  `json:"id"`
	Status         string  `json:"status"`
	Paid           bool    `json:"paid"`
	Amount         amount  `json:"amount"`
	RefundedAmount *amount `json:"refunded_amount,omitempty"`
	Confirmation   *struct {
		Type string `json:"type"`
		URL  string `json:"confirmation_url"`
	} `json:"confirmation,omitempty"`
	Test bool `json:"test"`
}

type refund struct {
	ID        string `json:"id"`
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
}

// APIError is a non-2xx answer; it carries YooKassa's code and
// description, never request headers.
type APIError struct {
	Status      int
	Code        string `json:"code"`
	Description string `json:"description"`
	Parameter   string `json:"parameter"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("yookassa: HTTP %d %s %s %s", e.Status, e.Code, e.Parameter, e.Description)
}

// Unwrap classifies the error: 400, 401, 403 cannot be fixed by a retry.
func (e *APIError) Unwrap() error {
	switch e.Status {
	case http.StatusNotFound:
		return payments.ErrNotFound
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return payments.ErrPermanent
	}
	return nil
}

// do sends one request outside any transaction (ADR-0007 rule zero).
func (g *Gateway) do(ctx context.Context, method, path, idemKey string, body, out any) error {
	if err := tx.AssertNoTx(ctx); err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.cfg.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.SetBasicAuth(g.cfg.ShopID, g.cfg.SecretKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idemKey != "" {
		req.Header.Set("Idempotence-Key", IdempotenceKey(idemKey))
	}
	res, err := g.cfg.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("yookassa: %s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusAccepted {
		// YooKassa could not answer within 30 s: repeat with the same key later
		return fmt.Errorf("yookassa: %s %s: processing, retry after %s ms", method, path, res.Header.Get("Retry-After"))
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		e := &APIError{Status: res.StatusCode}
		_ = json.Unmarshal(b, e)
		return e
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("yookassa: decode %s: %w", path, err)
		}
	}
	return nil
}

// FormatAmount renders minor units as the API decimal string.
func FormatAmount(m money.Money) (amount, error) {
	e, ok := money.Exponent(m.Currency)
	if !ok {
		return amount{}, money.ErrUnknownCurrency
	}
	if m.Minor < 0 {
		return amount{}, errors.New("yookassa: negative amount")
	}
	if e == 0 {
		return amount{Value: strconv.FormatInt(m.Minor, 10), Currency: m.Currency}, nil
	}
	p := int64(1)
	for i := 0; i < e; i++ {
		p *= 10
	}
	return amount{Value: fmt.Sprintf("%d.%0*d", m.Minor/p, e, m.Minor%p), Currency: m.Currency}, nil
}

// ParseAmount reads the API decimal string into minor units.
func ParseAmount(a amount) (money.Money, error) {
	e, ok := money.Exponent(a.Currency)
	if !ok {
		return money.Money{}, money.ErrUnknownCurrency
	}
	whole, frac, _ := strings.Cut(a.Value, ".")
	if len(frac) > e {
		return money.Money{}, fmt.Errorf("yookassa: amount %q has too many decimals", a.Value)
	}
	frac += strings.Repeat("0", e-len(frac))
	v, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil || v < 0 {
		return money.Money{}, fmt.Errorf("yookassa: bad amount %q", a.Value)
	}
	return money.New(v, a.Currency)
}

// status normalizes a YooKassa payment. waiting_for_capture does not occur
// with capture=true and is reported as pending; a succeeded payment whose
// refunded amount reaches the amount is refunded (full refunds only).
func status(p payment) (payments.Status, error) {
	amt, err := ParseAmount(p.Amount)
	if err != nil {
		return payments.Status{}, err
	}
	st := payments.Status{ProviderPaymentID: p.ID, Amount: amt}
	switch p.Status {
	case "pending", "waiting_for_capture":
		st.State = payments.StatePending
	case "succeeded":
		st.State = payments.StateSucceeded
		if p.RefundedAmount != nil {
			if r, err := ParseAmount(*p.RefundedAmount); err == nil && r.Minor >= amt.Minor && amt.Minor > 0 {
				st.State = payments.StateRefunded
			}
		}
	case "canceled":
		st.State = payments.StateCanceled
	default:
		return payments.Status{}, fmt.Errorf("yookassa: unknown payment status %q", p.Status)
	}
	return st, nil
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// CreatePayment implements payments.Gateway: POST /payments, one stage,
// redirect confirmation; idempotent by key for 24 h at YooKassa (the
// engine attaches the result within minutes or reconciles it).
func (g *Gateway) CreatePayment(ctx context.Context, req payments.CreatePaymentRequest) (payments.CreatePaymentResult, error) {
	if req.ReturnURL == "" || !strings.HasPrefix(req.ReturnURL, "http") {
		return payments.CreatePaymentResult{}, fmt.Errorf("%w: yookassa needs an absolute return URL", payments.ErrPermanent)
	}
	amt, err := FormatAmount(req.Amount)
	if err != nil {
		return payments.CreatePaymentResult{}, err
	}
	body := map[string]any{
		"amount":       amt,
		"capture":      true,
		"confirmation": map[string]any{"type": "redirect", "return_url": req.ReturnURL},
		"description":  truncate(req.Description, 128),
		"metadata":     map[string]string{"order_id": req.OrderID},
	}
	if t, ok := g.cfg.Methods[req.Method]; ok && t != "" {
		body["payment_method_data"] = map[string]string{"type": t}
	}
	if g.cfg.Receipts && req.Document != nil {
		r, err := g.receipt(*req.Document)
		if err != nil {
			return payments.CreatePaymentResult{}, err
		}
		body["receipt"] = r
	}
	var p payment
	if err := g.do(ctx, http.MethodPost, "/payments", req.IdempotencyKey, body, &p); err != nil {
		return payments.CreatePaymentResult{}, err
	}
	st, err := status(p)
	if err != nil {
		return payments.CreatePaymentResult{}, err
	}
	res := payments.CreatePaymentResult{ProviderPaymentID: p.ID, Status: st}
	if p.Confirmation != nil {
		res.ConfirmationURL = p.Confirmation.URL
	}
	return res, nil
}

// receipt maps a region-neutral document to the YooKassa receipt object.
// Agent lines (marketplace) need supplier data and are not supported yet.
func (g *Gateway) receipt(d taxdocs.Document) (map[string]any, error) {
	if d.BuyerEmail == "" {
		return nil, fmt.Errorf("%w: receipt needs the buyer email", payments.ErrPermanent)
	}
	if len(d.Lines) == 0 || len(d.Lines) > 100 {
		return nil, fmt.Errorf("%w: receipt needs 1 to 100 lines", payments.ErrPermanent)
	}
	items := make([]map[string]any, 0, len(d.Lines))
	for _, l := range d.Lines {
		if l.Agent {
			return nil, fmt.Errorf("%w: agent receipt lines are not supported yet", payments.ErrPermanent)
		}
		qty := l.Qty
		if qty <= 0 {
			qty = 1
		}
		if l.Amount%int64(qty) != 0 {
			return nil, fmt.Errorf("%w: line amount is not a multiple of the quantity", payments.ErrPermanent)
		}
		unit, err := FormatAmount(money.Money{Minor: l.Amount / int64(qty), Currency: d.Currency})
		if err != nil {
			return nil, err
		}
		vat, err := g.cfg.VATCode(l.TaxMode, l.TaxCode)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", payments.ErrPermanent, err)
		}
		items = append(items, map[string]any{
			"description": truncate(l.Title, 128), "quantity": strconv.Itoa(qty) + ".00", "amount": unit,
			"vat_code": vat, "payment_mode": l.Method, "payment_subject": l.Subject,
		})
	}
	return map[string]any{"customer": map[string]string{"email": d.BuyerEmail}, "items": items}, nil
}

// FetchPayment implements payments.Gateway: GET /payments/{id}.
func (g *Gateway) FetchPayment(ctx context.Context, id string) (payments.Status, error) {
	p, err := g.get(ctx, id)
	if err != nil {
		return payments.Status{}, err
	}
	return status(p)
}

func (g *Gateway) get(ctx context.Context, id string) (payment, error) {
	if !validID(id) {
		return payment{}, payments.ErrNotFound
	}
	var p payment
	err := g.do(ctx, http.MethodGet, "/payments/"+id, "", nil, &p)
	return p, err
}

// validID keeps ids from notifications out of the URL path syntax.
func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// CancelPayment implements payments.Gateway. YooKassa cancels only
// payments in waiting_for_capture; a pending payment expires on the
// provider side and is picked up by reconciliation, so its current status
// is returned unchanged.
func (g *Gateway) CancelPayment(ctx context.Context, id, key string) (payments.Status, error) {
	p, err := g.get(ctx, id)
	if err != nil {
		return payments.Status{}, err
	}
	if p.Status == "waiting_for_capture" {
		if err := g.do(ctx, http.MethodPost, "/payments/"+id+"/cancel", key, map[string]any{}, &p); err != nil {
			return payments.Status{}, err
		}
	}
	return status(p)
}

// RefundPayment implements payments.Gateway: POST /refunds (full refund).
func (g *Gateway) RefundPayment(ctx context.Context, req payments.RefundRequest) (payments.RefundResult, error) {
	amt, err := FormatAmount(req.Amount)
	if err != nil {
		return payments.RefundResult{}, err
	}
	body := map[string]any{"payment_id": req.ProviderPaymentID, "amount": amt}
	if g.cfg.Receipts && g.cfg.RefundReceipts && req.Document != nil {
		r, err := g.receipt(*req.Document)
		if err != nil {
			return payments.RefundResult{}, err
		}
		body["receipt"] = r
	}
	var r refund
	if err := g.do(ctx, http.MethodPost, "/refunds", req.IdempotencyKey, body, &r); err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusBadRequest {
			// e.g. the payment is not refundable: a final answer
			return payments.RefundResult{State: payments.StateFailed}, nil
		}
		return payments.RefundResult{}, err
	}
	res := payments.RefundResult{ProviderRefundID: r.ID}
	switch r.Status {
	case "succeeded":
		res.State = payments.StateSucceeded
	case "pending":
		res.State = payments.StatePending
	default:
		res.State = payments.StateFailed
	}
	return res, nil
}

type notification struct {
	Type   string `json:"type"`
	Event  string `json:"event"`
	Object struct {
		ID        string `json:"id"`
		PaymentID string `json:"payment_id"`
	} `json:"object"`
}

// ParseWebhook implements payments.Gateway. No side effects: optional IP
// check, then the object is re-fetched by id (a forged id is not found in
// this shop). The body status is never used.
func (g *Gateway) ParseWebhook(ctx context.Context, r *http.Request) (payments.Event, error) {
	if g.cfg.CheckIP && !g.trusted(g.clientIP(r)) {
		return payments.Event{}, payments.ErrInvalidSignature
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return payments.Event{}, err
	}
	var n notification
	if err := json.Unmarshal(body, &n); err != nil || n.Type != "notification" || n.Event == "" || !validID(n.Object.ID) {
		return payments.Event{}, payments.ErrInvalidSignature
	}
	paymentID := n.Object.ID
	switch {
	case strings.HasPrefix(n.Event, "payment."):
		if _, err := g.get(ctx, paymentID); err != nil {
			if errors.Is(err, payments.ErrNotFound) {
				return payments.Event{}, payments.ErrInvalidSignature
			}
			return payments.Event{}, err
		}
	case strings.HasPrefix(n.Event, "refund."):
		var rf refund
		if err := g.do(ctx, http.MethodGet, "/refunds/"+n.Object.ID, "", nil, &rf); err != nil {
			if errors.Is(err, payments.ErrNotFound) {
				return payments.Event{}, payments.ErrInvalidSignature
			}
			return payments.Event{}, err
		}
		if !validID(rf.PaymentID) {
			return payments.Event{}, payments.ErrInvalidSignature
		}
		paymentID = rf.PaymentID
	default:
		return payments.Event{}, payments.ErrInvalidSignature
	}
	return payments.Event{EventID: n.Event + ":" + n.Object.ID, Type: n.Event, ProviderPaymentID: paymentID, Payload: body}, nil
}

func (g *Gateway) clientIP(r *http.Request) string {
	if g.cfg.ClientIP != nil {
		return g.cfg.ClientIP(r)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (g *Gateway) trusted(ip string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, n := range g.nets {
		if n.Contains(a) {
			return true
		}
	}
	return false
}
