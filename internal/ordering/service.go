package ordering

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/fastygo/market/internal/mail"
	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/platform/money"
	"github.com/fastygo/market/internal/taxdocs"
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
	ProductSlug    string // alternative to OfferIDs: one product
	LicenseType    string // with ProductSlug; default "regular"
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
	// PaidImmediately writes the order as paid with delivery effects and
	// no payment provider (0-amount / free checkout).
	PaidImmediately bool
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
	ReconcileRefund = "refund" // refund still pending at the provider
	ReconcileTaxDoc = "taxdoc" // tax document pending at an async register
)

// Candidate is one reconciliation item.
type Candidate struct {
	Kind, OrderID, PaymentID, PaymentKey, Provider, Method, ProviderPaymentID string
	RefundID, DocID                                                           string
	Amount                                                                    money.Money
}

// Store is implemented by platform/pb. Every method is one short write
// transaction or a read; none is called with a transaction open.
type Store interface {
	Storefront(ctx context.Context, code string) (Storefront, error)
	Offers(ctx context.Context, ids []string) ([]Offer, error)
	OfferFor(ctx context.Context, productSlug, licenseType, currency string) (string, error)
	PlaceOrder(ctx context.Context, o NewOrder) (OrderView, error)
	AttachProvider(ctx context.Context, paymentID, providerPaymentID, confirmationURL string) (OrderView, error)
	ApplyPayment(ctx context.Context, provider string, st payments.Status, now time.Time) (Applied, error)
	ReplayEffects(ctx context.Context, orderID string, now time.Time) (Applied, error)
	OrderForBuyer(ctx context.Context, orderID, buyerID string) (OrderView, error)
	// Order reads an order without an owner check (public return page).
	Order(ctx context.Context, orderID string) (OrderView, error)
	// SaleDocument builds the sale tax document of an order from its
	// snapshot (read only); nil when the region needs none.
	SaleDocument(ctx context.Context, orderID string) (*taxdocs.Document, error)
	AcceptEvent(ctx context.Context, provider string, ev payments.Event, now time.Time) (duplicate bool, err error)
	ClaimEvent(ctx context.Context, now time.Time, lease time.Duration) (InboxItem, bool, error)
	FinishEvent(ctx context.Context, id string, procErr error, now time.Time, maxAttempts int) error
	ClaimOutbox(ctx context.Context, now time.Time, lease time.Duration) (OutboxItem, bool, error)
	FinishOutbox(ctx context.Context, id string, procErr error, now time.Time, maxAttempts int, backoff time.Duration) error
	ReconcileCandidates(ctx context.Context, now time.Time, olderThan time.Duration) ([]Candidate, error)

	// Refunds (full only): request writes the row and its outbox command
	// in one tx; apply records the provider answer and the effects.
	RequestRefund(ctx context.Context, orderID, reason, note, requestedBy string, now time.Time) (RefundRef, error)
	Refund(ctx context.Context, id string) (RefundRef, error)
	ApplyRefund(ctx context.Context, id string, res payments.RefundResult, callErr error, now time.Time) (Applied, error)
	// RequestTimeoutCancels marks awaiting orders older than the timeout
	// and queues CancelPayment; the order changes only on confirmation.
	RequestTimeoutCancels(ctx context.Context, now time.Time, olderThan time.Duration) (int, error)
	TaxDocument(ctx context.Context, id string) (TaxDoc, error)
	ApplyTaxDoc(ctx context.Context, id string, res taxdocs.Result, callErr error, now time.Time, maxAttempts int) (retry bool, err error)
}

// RefundRef is a refunds row with what the provider call needs.
type RefundRef struct {
	ID                string      `json:"refund_id"`
	OrderID           string      `json:"order_id"`
	PaymentID         string      `json:"payment_id"`
	Provider          string      `json:"provider"`
	ProviderPaymentID string      `json:"-"`
	Key               string      `json:"-"`
	Status            string      `json:"status"`
	Reason            string      `json:"reason_code"`
	Amount            money.Money `json:"-"`
}

// TaxDoc is a tax_documents row and its built document.
type TaxDoc struct {
	ID, Status, ExternalID string
	Doc                    taxdocs.Document
}

// Checkout errors.
var (
	ErrIdempotencyKey = errors.New("ordering: Idempotency-Key is required")
	ErrMethod         = errors.New("ordering: payment method is not enabled")
	ErrConsent        = errors.New("ordering: required consent missing")
	// ErrFree is retained for callers that still switch on it; Checkout
	// no longer returns it (a 0-amount cart is granted immediately).
	ErrFree          = errors.New("ordering: free products are delivered without checkout (phase 2)")
	ErrMultiSeller   = errors.New("ordering: one seller per order in phase 1")
	ErrNotFound      = errors.New("ordering: not found")
	ErrNotRefundable = errors.New("ordering: order cannot be refunded in its current state")
	ErrReason        = errors.New("ordering: unknown refund reason code")
)

// Outbox topics handled by the service itself.
const (
	TopicRefund = "payment.refund"
	TopicCancel = "payment.cancel"
	TopicTaxDoc = "taxdoc.issue"
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

	Issuer            taxdocs.Issuer
	TaxDocMaxAttempts int           // default 5, then failed + alert
	PaymentTimeout    time.Duration // default 30 min

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
	if len(req.OfferIDs) == 0 && req.ProductSlug != "" {
		lt := req.LicenseType
		if lt == "" {
			lt = "regular"
		}
		id, err := s.Store.OfferFor(ctx, req.ProductSlug, lt, sf.Currency)
		if err != nil {
			return OrderView{}, err
		}
		req.OfferIDs = []string{id}
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
		return s.checkoutFree(ctx, req, sf, offers, lines, tot, terms, now)
	}
	provider, ok := sf.Providers[req.Method]
	if !ok {
		return OrderView{}, ErrMethod
	}
	if _, err := s.gateway(provider); err != nil {
		return OrderView{}, err
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

// checkoutFree places a paid 0-amount order and grants the entitlement
// without a payment gateway. The idempotency key is derived from the
// buyer and product so repeated clicks do not create more orders.
func (s *Service) checkoutFree(ctx context.Context, req CheckoutRequest, sf Storefront, offers []Offer, lines []Line, tot Totals, terms string, now time.Time) (OrderView, error) {
	if req.BuyerID == "" {
		return OrderView{}, ErrFree
	}
	lt := offers[0].LicenseType
	if lt == "" {
		lt = "regular"
	}
	orderKey := req.BuyerID + ":free:" + offers[0].ProductID + ":" + lt
	return s.Store.PlaceOrder(ctx, NewOrder{
		Storefront: sf, BuyerID: req.BuyerID, BuyerEmail: req.BuyerEmail, OrderKey: orderKey,
		PaymentKey: "pay:" + orderKey + ":free", Provider: "free", Method: "free",
		SellerID: offers[0].SellerID, Lines: lines, Totals: tot, TermsVersion: terms,
		Consents: req.Consents, IPHash: req.IPHash, Digest: Digest(lines, tot), Now: now,
		PaidImmediately: true,
	})
}

// MissingGateways reports storefront methods whose provider has no
// configured gateway (start-up warning; checkout of paid items will fail).
func MissingGateways(methods map[string]string, gateways map[string]payments.Gateway) []string {
	var out []string
	for method, provider := range methods {
		if _, ok := gateways[provider]; !ok {
			out = append(out, "method "+method+" maps to provider "+provider+", but no gateway is configured")
		}
	}
	sort.Strings(out)
	return out
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
	doc, err := s.Store.SaleDocument(ctx, v.OrderID)
	if err != nil {
		return v, err
	}
	desc := v.Number
	if desc == "" {
		desc = v.OrderID
	}
	res, err := gw.CreatePayment(ctx, payments.CreatePaymentRequest{
		IdempotencyKey: v.PaymentKey, Amount: v.Total, Method: payments.Method(v.Method),
		Description: desc, ReturnURL: ret, OrderID: v.OrderID, Document: doc,
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

// ReturnPublic handles the browser return from the provider without a
// signed-in buyer (guest checkout, or the session cookie is not sent): the
// payment is confirmed with the provider and only the status is shown.
func (s *Service) ReturnPublic(ctx context.Context, orderID string) (OrderView, error) {
	v, err := s.Store.Order(ctx, orderID)
	if err != nil {
		return v, err
	}
	if v.ProviderPaymentID != "" {
		if _, err := s.Apply(ctx, v.Provider, v.ProviderPaymentID); err != nil {
			return v, err
		}
	}
	return s.Store.Order(ctx, orderID)
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
	h, found := s.builtin(it.Topic)
	if !found {
		h, found = s.Outbox[it.Topic]
	}
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
	if IsPermanent(perr) {
		// a retry cannot fix it: the row goes dead now (attempts already
		// counts this claim) and the worker logs it through onErr
		if err := s.Store.FinishOutbox(ctx, it.ID, perr, s.now(), it.Attempts, backoff); err != nil {
			return true, err
		}
		return true, fmt.Errorf("ordering: outbox %s %s failed permanently after %d attempt(s), not retried: %w", it.Topic, it.Key, it.Attempts, perr)
	}
	return true, s.Store.FinishOutbox(ctx, it.ID, perr, s.now(), s.maxAttempts(), backoff)
}

// IsPermanent reports provider errors a retry cannot fix
// (payments.ErrPermanent, mail.ErrPermanent). Transient errors (network,
// 5xx, SMTP 4xx and authentication) stay retryable with backoff.
func IsPermanent(err error) bool {
	return err != nil && (errors.Is(err, payments.ErrPermanent) || errors.Is(err, mail.ErrPermanent))
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
		case ReconcileRefund:
			_, err = s.ExecuteRefund(ctx, c.RefundID)
		case ReconcileTaxDoc:
			err = s.IssueTaxDoc(ctx, c.DocID)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", c.Kind, c.OrderID, err))
		}
	}
	return len(cs), errors.Join(errs...)
}

// RunWorkers runs exactly one inbox worker and one outbox worker until ctx
// is cancelled. The returned wait blocks until both have finished their
// current step (graceful shutdown: no step is cut off by closing the
// database) or the timeout passes; it reports whether they finished.
func (s *Service) RunWorkers(ctx context.Context, idle time.Duration, onErr func(error)) (wait func(timeout time.Duration) bool) {
	var wg sync.WaitGroup
	loop := func(step func(context.Context) (bool, error)) {
		defer wg.Done()
		t := time.NewTimer(idle)
		defer t.Stop()
		for ctx.Err() == nil {
			did, err := step(ctx)
			if err != nil && onErr != nil {
				onErr(err)
			}
			if !did {
				t.Reset(idle)
				select {
				case <-ctx.Done():
				case <-t.C:
				}
			}
		}
	}
	wg.Add(2)
	go loop(s.ProcessInboxOnce)
	go loop(s.ProcessOutboxOnce)
	return func(timeout time.Duration) bool {
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
			return true
		case <-time.After(timeout):
			return false
		}
	}
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

func (s *Service) builtin(topic string) (OutboxHandler, bool) {
	switch topic {
	case TopicRefund:
		return func(ctx context.Context, _ string, p json.RawMessage) error {
			var b struct {
				RefundID string `json:"refund_id"`
			}
			if err := json.Unmarshal(p, &b); err != nil {
				return err
			}
			_, err := s.ExecuteRefund(ctx, b.RefundID)
			return err
		}, true
	case TopicCancel:
		return func(ctx context.Context, key string, p json.RawMessage) error {
			var b struct {
				Provider, ProviderPaymentID string
			}
			if err := json.Unmarshal(p, &b); err != nil {
				return err
			}
			gw, err := s.gateway(b.Provider)
			if err != nil {
				return err
			}
			st, err := gw.CancelPayment(ctx, b.ProviderPaymentID, key)
			if err != nil {
				return err
			}
			// cancelled only on the provider's confirmation; a late
			// success moves the order to paid instead
			_, err = s.Store.ApplyPayment(ctx, b.Provider, st, s.now())
			return err
		}, true
	case TopicTaxDoc:
		return func(ctx context.Context, _ string, p json.RawMessage) error {
			var b struct {
				DocID string `json:"doc_id"`
			}
			if err := json.Unmarshal(p, &b); err != nil {
				return err
			}
			return s.IssueTaxDoc(ctx, b.DocID)
		}, true
	}
	return nil, false
}

// RefundOrder is the refund domain command (operator or buyer request;
// full refunds only). The refund row and its outbox command commit first;
// the provider is then called outside any transaction and the answer is
// applied. If the call fails the outbox worker retries with the same key.
func (s *Service) RefundOrder(ctx context.Context, orderID, reason, note, requestedBy string) (RefundRef, error) {
	if !ValidReason(reason) {
		return RefundRef{}, ErrReason
	}
	ref, err := s.Store.RequestRefund(ctx, orderID, reason, note, requestedBy, s.now())
	if err != nil {
		return ref, err
	}
	if _, err := s.ExecuteRefund(ctx, ref.ID); err != nil {
		return ref, err
	}
	return s.Store.Refund(ctx, ref.ID)
}

// ExecuteRefund calls Gateway.RefundPayment for a pending refund and
// applies the result. Idempotent: a final refund is left as it is.
func (s *Service) ExecuteRefund(ctx context.Context, refundID string) (Applied, error) {
	ref, err := s.Store.Refund(ctx, refundID)
	if err != nil || ref.Status != "pending" {
		return Applied{}, err
	}
	gw, err := s.gateway(ref.Provider)
	if err != nil {
		return Applied{}, err
	}
	res, callErr := gw.RefundPayment(ctx, payments.RefundRequest{IdempotencyKey: ref.Key, ProviderPaymentID: ref.ProviderPaymentID, Amount: ref.Amount})
	a, err := s.Store.ApplyRefund(ctx, refundID, res, callErr, s.now())
	if err != nil {
		return a, err
	}
	return a, callErr
}

// RequestTimeouts runs the timeout cron: awaiting orders older than
// PaymentTimeout get an outbox CancelPayment request.
func (s *Service) RequestTimeouts(ctx context.Context) (int, error) {
	d := s.PaymentTimeout
	if d == 0 {
		d = 30 * time.Minute
	}
	return s.Store.RequestTimeoutCancels(ctx, s.now(), d)
}

// IssueTaxDoc issues (or fetches, when the register answered pending) one
// tax document outside any transaction and applies the result. An error
// is returned while the attempt cap is not reached, so the outbox retries;
// at the cap the document is failed and an alert is raised.
func (s *Service) IssueTaxDoc(ctx context.Context, id string) error {
	if s.Issuer == nil {
		return errors.New("ordering: no tax document issuer configured")
	}
	d, err := s.Store.TaxDocument(ctx, id)
	if err != nil || d.Status != "pending" {
		return err
	}
	var res taxdocs.Result
	var callErr error
	if d.ExternalID != "" {
		res, callErr = s.Issuer.Fetch(ctx, d.ExternalID)
	} else {
		res, callErr = s.Issuer.Issue(ctx, d.Doc)
	}
	max := s.TaxDocMaxAttempts
	if max == 0 {
		max = 5
	}
	retry, err := s.Store.ApplyTaxDoc(ctx, id, res, callErr, s.now(), max)
	if err != nil {
		return err
	}
	if retry {
		return callErr
	}
	return nil
}
