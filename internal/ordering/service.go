package ordering

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/platform/money"
)

// Storefront is the config checkout needs.
type Storefront struct {
	ID, Code, Region, Currency, OrderPrefix, Mode string
	Display                                       TaxDisplay
	Providers                                     map[string]string // method -> provider code
}

// Consent accepted by the buyer at checkout.
type Consent struct{ Document, Version string }

// CheckoutRequest is the input of POST /checkout.
type CheckoutRequest struct {
	StorefrontCode string
	BuyerID        string
	BuyerEmail     string
	IdempotencyKey string
	OfferIDs       []string
	Method         string
	Consents       []Consent
	IPHash         string
}

// NewOrder is what tx 1 writes: order, items, payment `created`, consents.
type NewOrder struct {
	Storefront   Storefront
	BuyerID      string
	BuyerEmail   string
	OrderKey     string
	PaymentKey   string
	Provider     string
	Method       string
	SellerID     string
	Lines        []Line
	Totals       Totals
	TermsVersion string
	Consents     []Consent
	IPHash       string
	Digest       string
	Now          time.Time
}

// OrderView is an order with its current payment.
type OrderView struct {
	OrderID           string        `json:"order_id"`
	Number            string        `json:"number"`
	Status            OrderStatus   `json:"status"`
	BuyerID           string        `json:"-"`
	Total             money.Money   `json:"-"`
	PaymentID         string        `json:"payment_id"`
	PaymentKey        string        `json:"-"`
	PaymentStatus     PaymentStatus `json:"payment_status"`
	Provider          string        `json:"provider"`
	Method            string        `json:"-"`
	ProviderPaymentID string        `json:"-"`
	ConfirmationURL   string        `json:"confirmation_url,omitempty"`
}

// Applied reports what one apply transaction did.
type Applied struct {
	OrderID        string
	Order          OrderStatus
	Payment        PaymentStatus
	Changed        bool
	EffectsWritten int
}

// InboxItem is a claimed payment event.
type InboxItem struct {
	ID, Provider, ProviderPaymentID string
	Attempts                        int
}

// OutboxItem is a claimed external effect.
type OutboxItem struct {
	ID, Topic, Key string
	Payload        json.RawMessage
	Attempts       int
}

// Candidate kinds for reconciliation.
const (
	ReconcileFetch  = "fetch"  // payment with a provider id: FetchPayment + apply
	ReconcileAttach = "attach" // payment `created` without provider id (crash between steps 2 and 3)
	ReconcileReplay = "replay" // paid order with missing in-tx effects
)

// Candidate is one reconciliation item.
type Candidate struct {
	Kind, OrderID, PaymentID, PaymentKey, Provider, Method, ProviderPaymentID string
	Amount                                                                    money.Money
}

// Store is implemented by platform/pb. Every method is one short write
// transaction or a read; none is called with a transaction open.
type Store interface {
	Storefront(ctx context.Context, code string) (Storefront, error)
	Offers(ctx context.Context, ids []string) ([]Offer, error)
	PlaceOrder(ctx context.Context, o NewOrder) (OrderView, error)
	AttachProvider(ctx context.Context, paymentID, providerPaymentID, confirmationURL string) (OrderView, error)
	ApplyPayment(ctx context.Context, provider string, st payments.Status, now time.Time) (Applied, error)
	ReplayEffects(ctx context.Context, orderID string, now time.Time) (Applied, error)
	OrderForBuyer(ctx context.Context, orderID, buyerID string) (OrderView, error)
	AcceptEvent(ctx context.Context, provider string, ev payments.Event, now time.Time) (duplicate bool, err error)
	ClaimEvent(ctx context.Context, now time.Time, lease time.Duration) (InboxItem, bool, error)
	FinishEvent(ctx context.Context, id string, procErr error, now time.Time, maxAttempts int) error
	ClaimOutbox(ctx context.Context, now time.Time, lease time.Duration) (OutboxItem, bool, error)
	FinishOutbox(ctx context.Context, id string, procErr error, now time.Time, maxAttempts int, backoff time.Duration) error
	ReconcileCandidates(ctx context.Context, now time.Time, olderThan time.Duration) ([]Candidate, error)
}

// Checkout errors.
var (
	ErrIdempotencyKey = errors.New("ordering: Idempotency-Key is required")
	ErrMethod         = errors.New("ordering: payment method is not enabled")
	ErrConsent        = errors.New("ordering: required consent missing")
	ErrFree           = errors.New("ordering: free products are delivered without checkout (phase 2)")
	ErrMultiSeller    = errors.New("ordering: one seller per order in phase 1")
	ErrNotFound       = errors.New("ordering: not found")
)

// OutboxHandler runs one external effect; it must be idempotent by key.
type OutboxHandler func(ctx context.Context, key string, payload json.RawMessage) error

// Service is the ordering use cases: checkout, apply, inbox and outbox
// workers, reconciliation (ADR-0007).
type Service struct {
	Store            Store
	Tax              TaxPolicy
	Gateways         map[string]payments.Gateway
	Outbox           map[string]OutboxHandler
	RequiredConsents []string // e.g. "terms"
	Now              func() time.Time
	ReturnURL        func(orderID string) string

	Lease          time.Duration // inbox and outbox lease
	MaxAttempts    int
	OutboxBackoff  time.Duration
	ReconcileAfter time.Duration

	// AfterCreatePayment is a test crash hook between steps 2 and 3.
	AfterCreatePayment func() error
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) gateway(provider string) (payments.Gateway, error) {
	g, ok := s.Gateways[provider]
	if !ok {
		return nil, fmt.Errorf("%w: provider %q", ErrMethod, provider)
	}
	return g, nil
}

// Checkout runs steps 0 to 3 of the protocol. A retry with the same key
// returns the same order and never creates a second order or payment.
func (s *Service) Checkout(ctx context.Context, req CheckoutRequest) (OrderView, error) {
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 128 {
		return OrderView{}, ErrIdempotencyKey
	}
	sf, err := s.Store.Storefront(ctx, req.StorefrontCode)
	if err != nil {
		return OrderView{}, err
	}
	provider, ok := sf.Providers[req.Method]
	if !ok {
		return OrderView{}, ErrMethod
	}
	if _, err := s.gateway(provider); err != nil {
		return OrderView{}, err
	}
	terms := ""
	for _, doc := range s.RequiredConsents {
		found := false
		for _, c := range req.Consents {
			if c.Document == doc && c.Version != "" {
				found = true
				if doc == "terms" {
					terms = c.Version
				}
			}
		}
		if !found {
			return OrderView{}, fmt.Errorf("%w: %s", ErrConsent, doc)
		}
	}
	offers, err := s.Store.Offers(ctx, req.OfferIDs)
	if err != nil {
		return OrderView{}, err
	}
	if len(offers) == 0 || len(offers) != len(req.OfferIDs) {
		return OrderView{}, ErrNotFound
	}
	in := make([]TaxLineInput, len(offers))
	for i, o := range offers {
		if o.SellerID != offers[0].SellerID {
			return OrderView{}, ErrMultiSeller
		}
		if sf.Mode != "marketplace" {
			offers[i].SaleModel = Principal // ADR-0005: single_store is always principal
		}
		in[i] = TaxLineInput{Category: o.TaxCategory, Seller: o.SellerTax}
	}
	now := s.now()
	res, err := s.Tax.ResolveLines(now, sf.Display, in)
	if err != nil {
		return OrderView{}, err
	}
	lines, tot, err := Price(sf.Currency, sf.Display, offers, res)
	if err != nil {
		return OrderView{}, err
	}
	if tot.Sum == 0 {
		return OrderView{}, ErrFree
	}
	orderKey := req.BuyerID + ":" + req.IdempotencyKey
	v, err := s.Store.PlaceOrder(ctx, NewOrder{
		Storefront: sf, BuyerID: req.BuyerID, BuyerEmail: req.BuyerEmail, OrderKey: orderKey,
		PaymentKey: "pay:" + orderKey + ":" + provider, Provider: provider, Method: req.Method,
		SellerID: offers[0].SellerID, Lines: lines, Totals: tot, TermsVersion: terms,
		Consents: req.Consents, IPHash: req.IPHash, Digest: Digest(lines, tot), Now: now,
	})
	if err != nil {
		return OrderView{}, err
	}
	return s.ensurePayment(ctx, v)
}

// ensurePayment runs steps 2 and 3 for an order still waiting for its
// provider payment; the provider returns the same payment for the key.
func (s *Service) ensurePayment(ctx context.Context, v OrderView) (OrderView, error) {
	if v.Status != OrderPending && v.Status != OrderAwaitingPayment {
		return v, nil
	}
	if v.PaymentStatus != PaymentCreated && v.PaymentStatus != PaymentPending {
		return v, nil
	}
	gw, err := s.gateway(v.Provider)
	if err != nil {
		return v, err
	}
	ret := ""
	if s.ReturnURL != nil {
		ret = s.ReturnURL(v.OrderID)
	}
	res, err := gw.CreatePayment(ctx, payments.CreatePaymentRequest{
		IdempotencyKey: v.PaymentKey, Amount: v.Total, Method: payments.Method(v.Method),
		Description: v.Number, ReturnURL: ret,
	})
	if err != nil {
		return v, err
	}
	if s.AfterCreatePayment != nil {
		if err := s.AfterCreatePayment(); err != nil {
			return v, err
		}
	}
	return s.Store.AttachProvider(ctx, v.PaymentID, res.ProviderPaymentID, res.ConfirmationURL)
}

// Apply confirms a payment with the provider (no tx open) and applies the
// result in one transaction. Inbox, return URL and reconciliation share it.
func (s *Service) Apply(ctx context.Context, provider, providerPaymentID string) (Applied, error) {
	gw, err := s.gateway(provider)
	if err != nil {
		return Applied{}, err
	}
	st, err := gw.FetchPayment(ctx, providerPaymentID)
	if err != nil {
		return Applied{}, err
	}
	return s.Store.ApplyPayment(ctx, provider, st, s.now())
}

// Return handles GET /checkout/return/{order}; query parameters are ignored.
func (s *Service) Return(ctx context.Context, buyerID, orderID string) (OrderView, error) {
	v, err := s.Store.OrderForBuyer(ctx, orderID, buyerID)
	if err != nil {
		return v, err
	}
	if v.ProviderPaymentID != "" {
		if _, err := s.Apply(ctx, v.Provider, v.ProviderPaymentID); err != nil {
			return v, err
		}
	}
	return s.Store.OrderForBuyer(ctx, orderID, buyerID)
}

// AcceptWebhook verifies and stores one provider event (one short insert).
func (s *Service) AcceptWebhook(ctx context.Context, provider string, ev payments.Event) (bool, error) {
	return s.Store.AcceptEvent(ctx, provider, ev, s.now())
}

// ProcessInboxOnce claims one event, confirms it with FetchPayment outside
// any transaction and applies it. It reports whether an item was handled.
func (s *Service) ProcessInboxOnce(ctx context.Context) (bool, error) {
	it, ok, err := s.Store.ClaimEvent(ctx, s.now(), s.lease())
	if err != nil || !ok {
		return false, err
	}
	_, perr := s.Apply(ctx, it.Provider, it.ProviderPaymentID)
	return true, s.Store.FinishEvent(ctx, it.ID, perr, s.now(), s.maxAttempts())
}

// ProcessOutboxOnce claims one outbox row and runs its effect.
func (s *Service) ProcessOutboxOnce(ctx context.Context) (bool, error) {
	it, ok, err := s.Store.ClaimOutbox(ctx, s.now(), s.lease())
	if err != nil || !ok {
		return false, err
	}
	h, found := s.Outbox[it.Topic]
	var perr error
	if !found {
		perr = fmt.Errorf("ordering: no outbox handler for %q", it.Topic)
	} else {
		perr = h(ctx, it.Key, it.Payload)
	}
	backoff := s.OutboxBackoff
	if backoff == 0 {
		backoff = 30 * time.Second
	}
	return true, s.Store.FinishOutbox(ctx, it.ID, perr, s.now(), s.maxAttempts(), backoff)
}

// Reconcile runs one reconciliation pass (cron every 5 min) through the
// same apply path.
func (s *Service) Reconcile(ctx context.Context) (int, error) {
	after := s.ReconcileAfter
	if after == 0 {
		after = 10 * time.Minute
	}
	cs, err := s.Store.ReconcileCandidates(ctx, s.now(), after)
	if err != nil {
		return 0, err
	}
	var errs []error
	for _, c := range cs {
		switch c.Kind {
		case ReconcileFetch:
			_, err = s.Apply(ctx, c.Provider, c.ProviderPaymentID)
		case ReconcileAttach:
			_, err = s.ensurePayment(ctx, OrderView{OrderID: c.OrderID, Status: OrderPending, PaymentID: c.PaymentID,
				PaymentKey: c.PaymentKey, PaymentStatus: PaymentCreated, Provider: c.Provider, Method: c.Method, Total: c.Amount})
		case ReconcileReplay:
			_, err = s.Store.ReplayEffects(ctx, c.OrderID, s.now())
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", c.Kind, c.OrderID, err))
		}
	}
	return len(cs), errors.Join(errs...)
}

// RunWorkers runs exactly one inbox worker and one outbox worker until ctx
// is done (shutdown: stop claiming; leases expire if a call is cut off).
func (s *Service) RunWorkers(ctx context.Context, idle time.Duration, onErr func(error)) {
	loop := func(step func(context.Context) (bool, error)) {
		for ctx.Err() == nil {
			did, err := step(ctx)
			if err != nil && onErr != nil {
				onErr(err)
			}
			if !did {
				select {
				case <-ctx.Done():
				case <-time.After(idle):
				}
			}
		}
	}
	go loop(s.ProcessInboxOnce)
	go loop(s.ProcessOutboxOnce)
}

func (s *Service) lease() time.Duration {
	if s.Lease == 0 {
		return time.Minute
	}
	return s.Lease
}

func (s *Service) maxAttempts() int {
	if s.MaxAttempts == 0 {
		return 8
	}
	return s.MaxAttempts
}

// Digest is the sha256 of the line snapshot (orders.snapshot_digest).
func Digest(lines []Line, t Totals) string {
	type snap struct {
		Offer, Seller, Slug, License, TaxCode string
		Unit, Line, Tax, Commission           int64
		Rate                                  int
		Mode                                  TaxMode
	}
	s := make([]snap, len(lines))
	for i, l := range lines {
		s[i] = snap{l.Offer.ID, l.Offer.SellerID, l.Offer.ProductSlug, l.Offer.LicenseType, l.Tax.Code, l.UnitPrice, l.LineMinor, l.TaxMinor, l.Commission, l.Tax.RateBP, l.Tax.Mode}
	}
	b, _ := json.Marshal(struct {
		Lines  []snap
		Totals Totals
	}{s, t})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
