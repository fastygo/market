package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fastygo/market/internal/account"
	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/web/screens"
)

// Cabinet is the buyer account (ADR-0027): PocketBase users auth with an
// httpOnly session cookie, CSRF tokens on every form, magic sign-in links.
// It is separate from the PocketBase admin UI (/_/).
type Cabinet struct {
	Users   account.Users
	Magic   account.Magic
	Orders  AccountReader
	Mailer  SignInMailer
	CSRFKey []byte
	// Secure marks cookies Secure (public URL is https).
	Secure bool
	// AllowedOrigin is the public origin (scheme://host) accepted in the
	// Origin header of form posts besides the request host.
	AllowedOrigin string
	// TermsVersion is the accepted terms version recorded with consent.
	TermsVersion string
	// Method is the payment method code used by the product page form.
	Method string
}

// AccountReader reads the cabinet data (platform/pb.Account).
type AccountReader interface {
	Orders(ctx context.Context, buyerID string) ([]account.Order, error)
	Order(ctx context.Context, buyerID, number string) (account.Order, error)
}

// SignInMailer sends magic sign-in links (notify.Service).
type SignInMailer interface {
	SignInEmail(ctx context.Context, u account.User, next string) error
}

// AccountPatterns are the cabinet routes.
var AccountPatterns = []string{
	"GET /account",
	"POST /account/login",
	"POST /account/link",
	"GET /account/magic/{token}",
	"POST /account/logout",
	"GET /account/orders",
	"GET /account/orders/{number}",
	"POST /checkout/form",
	"GET /fakepay/{id}",
}

const (
	sessionCookie = "market_session"
	csrfCookie    = "market_csrf"
)

func (h *Handler) mountAccount(m *http.ServeMux) {
	if h.Commerce == nil || h.Commerce.Cabinet == nil {
		return
	}
	m.HandleFunc("GET /account", h.accountHome)
	m.HandleFunc("POST /account/login", h.accountLogin)
	m.HandleFunc("POST /account/link", h.accountLink)
	m.HandleFunc("GET /account/magic/{token}", h.accountMagic)
	m.HandleFunc("POST /account/logout", h.accountLogout)
	m.HandleFunc("GET /account/orders", h.accountOrders)
	m.HandleFunc("GET /account/orders/{number}", h.accountOrder)
	m.HandleFunc("POST /checkout/form", h.checkoutForm)
	if h.Commerce.FakePay != nil {
		m.HandleFunc("GET /fakepay/{id}", h.fakePayPage)
	}
}

// buyer is the signed-in buyer: bearer token (API clients) or the session
// cookie (SSR cabinet).
func (c *Commerce) buyer(r *http.Request) (Buyer, bool) {
	if c.Auth != nil {
		if b, ok := c.Auth(r); ok {
			return b, true
		}
	}
	if c.Cabinet == nil {
		return Buyer{}, false
	}
	ck, err := r.Cookie(sessionCookie)
	if err != nil || ck.Value == "" {
		return Buyer{}, false
	}
	u, err := c.Cabinet.Users.FromSession(r.Context(), ck.Value)
	if err != nil {
		return Buyer{}, false
	}
	return Buyer{ID: u.ID, Email: u.Email}, true
}

func (c *Cabinet) setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true,
		Secure: c.Secure, SameSite: http.SameSiteLaxMode})
}

// csrf returns the form token: HMAC of a random per-browser cookie.
func (c *Cabinet) csrf(w http.ResponseWriter, r *http.Request) string {
	v := ""
	if ck, err := r.Cookie(csrfCookie); err == nil && len(ck.Value) >= 32 {
		v = ck.Value
	} else {
		b := make([]byte, 24)
		_, _ = rand.Read(b)
		v = base64.RawURLEncoding.EncodeToString(b)
		c.setCookie(w, csrfCookie, v, 0)
	}
	return c.sign(v)
}

func (c *Cabinet) sign(v string) string {
	m := hmac.New(sha256.New, c.CSRFKey)
	m.Write([]byte("csrf\n" + v))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// checkCSRF: the form token matches the cookie, and a present Origin
// header is this host or the public origin.
func (c *Cabinet) checkCSRF(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		u, err := url.Parse(o)
		if err != nil || (u.Host != r.Host && o != c.AllowedOrigin) {
			return false
		}
	}
	ck, err := r.Cookie(csrfCookie)
	if err != nil || len(ck.Value) < 32 {
		return false
	}
	return hmac.Equal([]byte(r.PostFormValue("csrf")), []byte(c.sign(ck.Value)))
}

func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (h *Handler) parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil || !h.Commerce.Cabinet.checkCSRF(r) {
		h.notice(w, r, http.StatusForbidden, "ui.account.csrf")
		return false
	}
	return true
}

func (h *Handler) accountShell(w http.ResponseWriter, r *http.Request, titleKey string) (screens.Shell, bool) {
	s, _, err := h.shell(r.Context(), h.Dict.T(titleKey), "account")
	if err != nil {
		h.fail(w, r, err)
		return s, false
	}
	return s, true
}

func (h *Handler) signInPage(w http.ResponseWriter, r *http.Request, status int, v screens.SignInView) {
	s, ok := h.accountShell(w, r, "ui.account.signin_title")
	if !ok {
		return
	}
	v.CSRF = h.Commerce.Cabinet.csrf(w, r)
	w.Header().Set("Cache-Control", "no-store")
	h.render(w, r, status, screens.SignInPage(s, v))
}

func (h *Handler) accountHome(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.Commerce.buyer(r); ok {
		http.Redirect(w, r, "/account/orders", http.StatusSeeOther)
		return
	}
	h.signInPage(w, r, http.StatusOK, screens.SignInView{Next: account.SafeNext(r.URL.Query().Get("next"))})
}

func (h *Handler) startSession(w http.ResponseWriter, r *http.Request, userID, next string) {
	c := h.Commerce.Cabinet
	tok, err := c.Users.SessionToken(r.Context(), userID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	c.setCookie(w, sessionCookie, tok, int((7 * 24 * time.Hour).Seconds()))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, account.SafeNext(next), http.StatusSeeOther)
}

func (h *Handler) accountLogin(w http.ResponseWriter, r *http.Request) {
	if !h.parseForm(w, r) {
		return
	}
	c := h.Commerce.Cabinet
	email, next := r.PostFormValue("email"), r.PostFormValue("next")
	u, err := c.Users.CheckPassword(r.Context(), email, r.PostFormValue("password"))
	if err != nil {
		h.signInPage(w, r, http.StatusUnauthorized, screens.SignInView{Email: email, Next: next, ErrorKey: "ui.account.bad_credentials"})
		return
	}
	h.startSession(w, r, u.ID, next)
}

// accountLink always answers the same way, so it does not reveal which
// emails have accounts.
func (h *Handler) accountLink(w http.ResponseWriter, r *http.Request) {
	if !h.parseForm(w, r) {
		return
	}
	c := h.Commerce.Cabinet
	email, err := account.NormalizeEmail(r.PostFormValue("email"))
	next := account.SafeNext(r.PostFormValue("next"))
	if err != nil {
		h.signInPage(w, r, http.StatusUnprocessableEntity, screens.SignInView{Next: next, ErrorKey: "ui.account.bad_email"})
		return
	}
	if u, err := c.Users.ByEmail(r.Context(), email); err == nil && c.Mailer != nil {
		if err := c.Mailer.SignInEmail(r.Context(), u, next); err != nil {
			h.Log.Warn("account: sign-in email", "err", err)
		}
	}
	h.signInPage(w, r, http.StatusOK, screens.SignInView{Email: email, Next: next, InfoKey: "ui.account.link_sent"})
}

func (h *Handler) accountMagic(w http.ResponseWriter, r *http.Request) {
	c := h.Commerce.Cabinet
	u, next, err := c.Magic.Verify(r.Context(), r.PathValue("token"), c.Users)
	if err != nil {
		h.signInPage(w, r, http.StatusForbidden, screens.SignInView{ErrorKey: "ui.account.link_invalid"})
		return
	}
	if err := c.Users.MarkVerified(r.Context(), u.ID); err != nil {
		h.Log.Warn("account: verify", "err", err)
	}
	h.startSession(w, r, u.ID, next)
}

func (h *Handler) accountLogout(w http.ResponseWriter, r *http.Request) {
	if !h.parseForm(w, r) {
		return
	}
	h.Commerce.Cabinet.setCookie(w, sessionCookie, "", -1)
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (h *Handler) requireBuyer(w http.ResponseWriter, r *http.Request) (Buyer, bool) {
	b, ok := h.Commerce.buyer(r)
	if !ok {
		http.Redirect(w, r, "/account?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
	}
	return b, ok
}

func (h *Handler) accountOrders(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requireBuyer(w, r)
	if !ok {
		return
	}
	os, err := h.Commerce.Cabinet.Orders.Orders(r.Context(), b.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.accountShell(w, r, "ui.account.orders")
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.render(w, r, http.StatusOK, screens.OrdersPage(s, screens.AccountView{Email: b.Email, CSRF: h.Commerce.Cabinet.csrf(w, r)}, os))
}

func (h *Handler) accountOrder(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requireBuyer(w, r)
	if !ok {
		return
	}
	o, err := h.Commerce.Cabinet.Orders.Order(r.Context(), b.ID, r.PathValue("number"))
	if errors.Is(err, account.ErrOrderNotFound) {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.accountShell(w, r, h.Dict.T("ui.account.order")+" "+o.Number)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.render(w, r, http.StatusOK, screens.OrderPage(s, screens.AccountView{Email: b.Email, CSRF: h.Commerce.Cabinet.csrf(w, r)}, o))
}

// checkoutView is the product page buy form state.
func (h *Handler) checkoutView(w http.ResponseWriter, r *http.Request) screens.CheckoutView {
	c := h.Commerce
	if c == nil || c.Cabinet == nil || c.Orders == nil {
		return screens.CheckoutView{}
	}
	v := screens.CheckoutView{Enabled: true, CSRF: c.Cabinet.csrf(w, r), Key: newKey(), Terms: c.Cabinet.TermsVersion}
	if b, ok := c.buyer(r); ok {
		v.Email, v.Signed = b.Email, true
	}
	return v
}

// checkoutForm is the SSR checkout: signed-in buyer, or a guest email that
// gets (or reuses) an account; the guest is not signed in here, the
// purchase email carries the cabinet link.
func (h *Handler) checkoutForm(w http.ResponseWriter, r *http.Request) {
	if !h.parseForm(w, r) {
		return
	}
	c := h.Commerce
	slug := r.PostFormValue("product")
	again := func(key string) {
		s, p, ok := h.loadProduct(w, r)
		if !ok {
			return
		}
		v := h.checkoutView(w, r)
		v.ErrorKey, v.Email = key, firstNonEmpty(v.Email, r.PostFormValue("email"))
		h.render(w, r, http.StatusUnprocessableEntity, screens.ProductPage(s, p, v))
	}
	r.SetPathValue("slug", slug)
	if r.PostFormValue("terms") != "1" {
		again("ui.checkout.terms_required")
		return
	}
	b, signed := c.buyer(r)
	if !signed {
		u, _, err := c.Cabinet.Users.Ensure(r.Context(), r.PostFormValue("email"))
		if errors.Is(err, account.ErrInvalidEmail) {
			again("ui.account.bad_email")
			return
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		b = Buyer{ID: u.ID, Email: u.Email}
	}
	key := r.PostFormValue("key")
	if key == "" {
		key = newKey()
	}
	v, err := c.Orders.Checkout(r.Context(), ordering.CheckoutRequest{
		StorefrontCode: h.StorefrontCode, BuyerID: b.ID, BuyerEmail: b.Email, IdempotencyKey: "form:" + key,
		ProductSlug: slug, LicenseType: firstNonEmpty(r.PostFormValue("license"), "regular"), Method: c.Cabinet.Method,
		Consents: []ordering.Consent{{Document: "terms", Version: c.Cabinet.TermsVersion}}, IPHash: c.hash(c.clientIP(r)),
	})
	switch {
	case errors.Is(err, ordering.ErrNotFound), errors.Is(err, ordering.ErrFree), errors.Is(err, ordering.ErrOfferInactive), errors.Is(err, ordering.ErrMethod):
		again("ui.checkout.unavailable")
		return
	case err != nil:
		h.Log.Error("checkout form", "err", err)
		again("ui.checkout.failed")
		return
	}
	target := v.ConfirmationURL
	if v.Status == ordering.OrderPaid || target == "" {
		target = "/checkout/return/" + v.OrderID
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// returnPage renders the browser return from the provider: the payment is
// confirmed with the provider first; only the owner sees the order link.
func (h *Handler) returnPage(w http.ResponseWriter, r *http.Request) {
	c := h.Commerce
	id := r.PathValue("order")
	v, err := c.Orders.ReturnPublic(r.Context(), id)
	if errors.Is(err, ordering.ErrNotFound) {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	if err != nil {
		h.Log.Warn("return", "order", id, "err", err)
		if v.OrderID == "" {
			h.fail(w, r, err)
			return
		}
	}
	rv := screens.ReturnView{State: "pending"}
	switch v.Status {
	case ordering.OrderPaid, ordering.OrderFulfilled:
		rv.State = "paid"
	case ordering.OrderCancelled:
		rv.State = "failed"
	}
	if b, ok := c.buyer(r); ok && b.ID == v.BuyerID {
		rv.OrderHref = "/account/orders/" + v.Number
	}
	s, _, err := h.shell(r.Context(), h.Dict.T("ui.checkout.title"), "")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.render(w, r, http.StatusOK, screens.ReturnPage(s, rv))
}

// fakePayPage is the development provider page (plays the YooKassa test
// store page): pay or cancel, then back to the return URL.
func (h *Handler) fakePayPage(w http.ResponseWriter, r *http.Request) {
	g := h.Commerce.FakePay
	id := r.PathValue("id")
	st, err := g.FetchPayment(r.Context(), id)
	if err != nil {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	s, _, err := h.shell(r.Context(), h.Dict.T("ui.fakepay.title"), "")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.render(w, r, http.StatusOK, screens.FakePayPage(s, screens.FakePayView{ID: id, Amount: st.Amount}))
}

// AllPatterns is every route the handler may mount; the host router must
// forward all of them (storefront, commerce and cabinet).
func AllPatterns() []string {
	out := append([]string{}, Patterns...)
	out = append(out, CommercePatterns...)
	return append(out, AccountPatterns...)
}
