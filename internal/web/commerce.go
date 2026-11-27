package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"

	"github.com/fastygo/market/internal/licensing"
	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/payments/fakegw"
	"github.com/fastygo/market/internal/platform/ratelimit"
)

// Buyer is the signed-in user of a request.
type Buyer struct{ ID, Email string }

// Commerce wires the phase 1 money and delivery routes. Phase 1 serves
// checkout as JSON for bearer-token clients (no cookie session yet, so no
// CSRF surface); SSR checkout pages come with the account pages.
type Commerce struct {
	Auth     func(r *http.Request) (Buyer, bool)
	Orders   *ordering.Service
	Licenses *licensing.Service
	// Operator authenticates operator commands (PocketBase superusers);
	// nil disables them.
	Operator func(r *http.Request) (string, bool)
	// FakePay enables POST /fakepay/{id}/{event} (development only).
	FakePay *fakegw.Gateway
	// HashSalt salts ip and user agent hashes in the downloads log.
	HashSalt string
	// Cabinet enables the SSR buyer account and checkout form; nil keeps
	// the JSON API only.
	Cabinet *Cabinet
	// Files serves local release files (LocalPresigner URLs); nil when
	// releases live in S3.
	Files http.Handler
	// ClientIP resolves the client address behind trusted proxies
	// (clientip.Resolver); nil means the direct peer (RemoteAddr).
	ClientIP func(r *http.Request) string
	// AuthLimit rate-limits sign-in posts per client IP; nil disables it.
	AuthLimit *ratelimit.Limiter
}

// CommercePatterns are the phase 1 routes (Go 1.22 ServeMux syntax).
var CommercePatterns = []string{
	"POST /checkout",
	"GET /checkout/return/{order}",
	"POST /hooks/pay/{provider}",
	"POST /webhooks/{provider}",
	"POST /fakepay/{id}/{event}",
	"POST /admin/orders/{order}/refund",
	"GET /dl/{token}",
	"GET /files/{key...}",
}

func (h *Handler) mountCommerce(m *http.ServeMux) {
	if h.Commerce == nil {
		return
	}
	m.HandleFunc("POST /checkout", h.checkout)
	m.HandleFunc("GET /checkout/return/{order}", h.checkoutReturn)
	m.HandleFunc("POST /hooks/pay/{provider}", h.webhook)
	m.HandleFunc("POST /webhooks/{provider}", h.webhook)
	m.HandleFunc("GET /dl/{token}", h.dl)
	h.mountAccount(m)
	if h.Commerce.Files != nil {
		m.Handle("GET /files/{key...}", h.Commerce.Files)
	}
	if h.Commerce.Operator != nil {
		m.HandleFunc("POST /admin/orders/{order}/refund", h.refund)
	}
	if h.Commerce.FakePay != nil {
		m.HandleFunc("POST /fakepay/{id}/{event}", h.fakePay)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

type checkoutBody struct {
	Product     string            `json:"product"`
	LicenseType string            `json:"license_type"`
	OfferIDs    []string          `json:"offer_ids"`
	Method      string            `json:"method"`
	Consents    map[string]string `json:"consents"` // document -> version
}

func (h *Handler) checkout(w http.ResponseWriter, r *http.Request) {
	c := h.Commerce
	buyer, ok := c.Auth(r)
	if !ok {
		jsonError(w, http.StatusUnauthorized, "auth_required")
		return
	}
	var b checkoutBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&b); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	req := ordering.CheckoutRequest{
		StorefrontCode: h.StorefrontCode, BuyerID: buyer.ID, BuyerEmail: buyer.Email,
		IdempotencyKey: r.Header.Get("Idempotency-Key"), OfferIDs: b.OfferIDs, ProductSlug: b.Product,
		LicenseType: b.LicenseType, Method: b.Method, IPHash: c.hash(c.clientIP(r)),
	}
	for doc, ver := range b.Consents {
		req.Consents = append(req.Consents, ordering.Consent{Document: doc, Version: ver})
	}
	v, err := c.Orders.Checkout(r.Context(), req)
	if err != nil {
		h.orderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) orderError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ordering.ErrIdempotencyKey):
		jsonError(w, http.StatusBadRequest, "idempotency_key_required")
	case errors.Is(err, ordering.ErrConsent):
		jsonError(w, http.StatusUnprocessableEntity, "consent_required")
	case errors.Is(err, ordering.ErrMethod):
		jsonError(w, http.StatusUnprocessableEntity, "method_not_enabled")
	case errors.Is(err, ordering.ErrNotFound):
		jsonError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, ordering.ErrFree), errors.Is(err, ordering.ErrOfferInactive), errors.Is(err, ordering.ErrMultiSeller):
		jsonError(w, http.StatusUnprocessableEntity, "not_purchasable")
	default:
		h.Log.Error("checkout", "path", r.URL.Path, "err", err)
		jsonError(w, http.StatusBadGateway, "try_again")
	}
}

func (h *Handler) checkoutReturn(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") == "" {
		// a browser coming back from the provider page
		h.returnPage(w, r)
		return
	}
	buyer, ok := h.Commerce.Auth(r)
	if !ok {
		jsonError(w, http.StatusUnauthorized, "auth_required")
		return
	}
	v, err := h.Commerce.Orders.Return(r.Context(), buyer.ID, r.PathValue("order"))
	if err != nil {
		h.orderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// webhook: verify -> one insert into the inbox -> 2xx; duplicates are 2xx.
func (h *Handler) webhook(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	gw, ok := h.Commerce.Orders.Gateways[provider]
	if !ok {
		jsonError(w, http.StatusNotFound, "unknown_provider")
		return
	}
	ev, err := gw.ParseWebhook(r.Context(), r)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_signature")
		return
	}
	dup, err := h.Commerce.Orders.AcceptWebhook(r.Context(), provider, ev)
	if err != nil {
		h.Log.Error("webhook", "provider", provider, "err", err)
		jsonError(w, http.StatusServiceUnavailable, "retry")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"accepted": true, "duplicate": dup})
}

// fakeEvents maps the dev routes to provider states: the buyer paid, a
// chargeback opened, the dispute was won or lost.
var fakeEvents = map[string]payments.State{
	"succeed": payments.StateSucceeded,
	"dispute": payments.StateDisputed,
	"win":     payments.StateSucceeded,
	"lose":    payments.StateRefunded,
	"cancel":  payments.StateCanceled,
}

// fakePay plays the provider side: the state changes and the provider
// sends a signed webhook (delivered in-process here).
func (h *Handler) fakePay(w http.ResponseWriter, r *http.Request) {
	g := h.Commerce.FakePay
	id, event := r.PathValue("id"), r.PathValue("event")
	st, ok := fakeEvents[event]
	if !ok {
		jsonError(w, http.StatusNotFound, "unknown_event")
		return
	}
	if !g.SetState(id, st) {
		jsonError(w, http.StatusNotFound, "not_found")
		return
	}
	evID := "evt-" + id + "-" + event
	body, _ := g.Webhook(evID, id, st)
	if _, err := h.Commerce.Orders.AcceptWebhook(r.Context(), string(fakegw.Code), payments.Event{
		EventID: evID, Type: "payment." + string(st), ProviderPaymentID: id, Payload: body,
	}); err != nil {
		h.fail(w, r, err)
		return
	}
	if ret := g.ReturnURL(id); ret != "" && r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		// the provider page form: back to the shop like a real provider
		http.Redirect(w, r, ret, http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"payment": id, "state": string(st)})
}

type refundBody struct {
	Reason string `json:"reason"`
	Note   string `json:"note"`
}

// refund is the operator refund command (full refunds only).
func (h *Handler) refund(w http.ResponseWriter, r *http.Request) {
	op, ok := h.Commerce.Operator(r)
	if !ok {
		jsonError(w, http.StatusUnauthorized, "operator_required")
		return
	}
	var b refundBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&b); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	ref, err := h.Commerce.Orders.RefundOrder(r.Context(), r.PathValue("order"), b.Reason, b.Note, op)
	switch {
	case errors.Is(err, ordering.ErrReason):
		jsonError(w, http.StatusUnprocessableEntity, "invalid_reason")
	case errors.Is(err, ordering.ErrNotRefundable):
		jsonError(w, http.StatusConflict, "not_refundable")
	case errors.Is(err, ordering.ErrNotFound):
		jsonError(w, http.StatusNotFound, "not_found")
	case err != nil && ref.ID != "":
		// recorded; the provider call is retried by the outbox
		h.Log.Warn("refund", "order", r.PathValue("order"), "err", err)
		writeJSON(w, http.StatusAccepted, ref)
	case err != nil:
		h.orderError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, ref)
	}
}

// download is the plain product type delivery entry (ADR-0009, ADR-0019):
// signed-in owner of an active entitlement -> 302 to /dl/{token}.
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	if h.Commerce == nil || h.Commerce.Licenses == nil {
		if _, _, ok := h.loadProduct(w, r); ok {
			h.notice(w, r, http.StatusNotImplemented, "ui.product.download_unavailable")
		}
		return
	}
	buyer, ok := h.Commerce.buyer(r)
	if !ok {
		if h.Commerce.Cabinet != nil {
			http.Redirect(w, r, "/account?next="+url.QueryEscape("/account/orders"), http.StatusSeeOther)
			return
		}
		h.notice(w, r, http.StatusUnauthorized, "ui.product.download_signin")
		return
	}
	link, err := h.Commerce.Licenses.Link(r.Context(), buyer.ID, r.PathValue("slug"))
	if errors.Is(err, licensing.ErrNoEntitlement) {
		h.notice(w, r, http.StatusForbidden, "ui.product.download_denied")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, link, http.StatusFound)
}

// dl re-checks the token, entitlement and release, logs the download and
// redirects to the pre-signed storage URL.
func (h *Handler) dl(w http.ResponseWriter, r *http.Request) {
	c := h.Commerce
	if c.Licenses == nil {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	url, err := c.Licenses.Resolve(r.Context(), r.PathValue("token"), c.hash(c.clientIP(r)), c.hash(r.UserAgent()))
	switch {
	case errors.Is(err, licensing.ErrTokenExpired):
		h.notice(w, r, http.StatusGone, "ui.product.download_expired")
		return
	case errors.Is(err, licensing.ErrTokenInvalid), errors.Is(err, licensing.ErrNoEntitlement):
		h.notice(w, r, http.StatusForbidden, "ui.product.download_denied")
		return
	case errors.Is(err, licensing.ErrFileMissing):
		h.Log.Warn("download: release file missing", "err", err)
		h.notice(w, r, http.StatusNotFound, "ui.product.download_missing")
		return
	case err != nil:
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, url, http.StatusFound)
}

func (c *Commerce) hash(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(c.HashSalt + "\n" + s))
	return hex.EncodeToString(sum[:12])
}

func (c *Commerce) clientIP(r *http.Request) string {
	if c.ClientIP != nil {
		return c.ClientIP(r)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
