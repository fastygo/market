package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/fastygo/market/internal/licensing"
	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/payments/fakegw"
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
	// FakePay enables POST /fakepay/{id}/succeed (development only).
	FakePay *fakegw.Gateway
	// HashSalt salts ip and user agent hashes in the downloads log.
	HashSalt string
}

// CommercePatterns are the phase 1 routes (Go 1.22 ServeMux syntax).
var CommercePatterns = []string{
	"POST /checkout",
	"GET /checkout/return/{order}",
	"POST /hooks/pay/{provider}",
	"POST /fakepay/{id}/succeed",
	"GET /dl/{token}",
}

func (h *Handler) mountCommerce(m *http.ServeMux) {
	if h.Commerce == nil {
		return
	}
	m.HandleFunc("POST /checkout", h.checkout)
	m.HandleFunc("GET /checkout/return/{order}", h.checkoutReturn)
	m.HandleFunc("POST /hooks/pay/{provider}", h.webhook)
	m.HandleFunc("GET /dl/{token}", h.dl)
	if h.Commerce.FakePay != nil {
		m.HandleFunc("POST /fakepay/{id}/succeed", h.fakePay)
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
		LicenseType: b.LicenseType, Method: b.Method, IPHash: c.hash(clientIP(r)),
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

// fakePay plays the provider side: the buyer paid; the provider sends a
// signed webhook (delivered in-process here).
func (h *Handler) fakePay(w http.ResponseWriter, r *http.Request) {
	g := h.Commerce.FakePay
	id := r.PathValue("id")
	if !g.SetState(id, payments.StateSucceeded) {
		jsonError(w, http.StatusNotFound, "not_found")
		return
	}
	body, _ := g.Webhook("evt-"+id+"-succeeded", id, payments.StateSucceeded)
	if _, err := h.Commerce.Orders.AcceptWebhook(r.Context(), string(fakegw.Code), payments.Event{
		EventID: "evt-" + id + "-succeeded", Type: "payment.succeeded", ProviderPaymentID: id, Payload: body,
	}); err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"payment": id, "state": "succeeded"})
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
	buyer, ok := h.Commerce.Auth(r)
	if !ok {
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
	url, err := c.Licenses.Resolve(r.Context(), r.PathValue("token"), c.hash(clientIP(r)), c.hash(r.UserAgent()))
	switch {
	case errors.Is(err, licensing.ErrTokenExpired):
		h.notice(w, r, http.StatusGone, "ui.product.download_expired")
		return
	case errors.Is(err, licensing.ErrTokenInvalid), errors.Is(err, licensing.ErrNoEntitlement):
		h.notice(w, r, http.StatusForbidden, "ui.product.download_denied")
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

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
