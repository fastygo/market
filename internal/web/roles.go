package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/fastygo/market/internal/account"
	"github.com/fastygo/market/internal/cabinet"
	"github.com/fastygo/market/internal/web/screens"
)

// reqState carries the response writer and the lazily loaded principal of
// one request, so the page frame can show the signed-in user.
type reqState struct {
	w      http.ResponseWriter
	r      *http.Request
	p      cabinet.Principal
	loaded bool
	signed bool
}

type reqKey struct{}

func withRequest(w http.ResponseWriter, r *http.Request) *http.Request {
	st := &reqState{w: w}
	r = r.WithContext(context.WithValue(r.Context(), reqKey{}, st))
	st.r = r
	return r
}

func stateOf(ctx context.Context) *reqState {
	st, _ := ctx.Value(reqKey{}).(*reqState)
	return st
}

// principal is the signed-in user with roles (zero, false for guests).
func (h *Handler) principal(r *http.Request) (cabinet.Principal, bool) {
	st := stateOf(r.Context())
	if st != nil && st.loaded {
		return st.p, st.signed
	}
	var p cabinet.Principal
	signed := false
	if c := h.Commerce; c != nil && c.Cabinet != nil {
		if b, ok := c.buyer(r); ok {
			signed = true
			p = cabinet.Principal{UserID: b.ID, Email: b.Email}
			if c.Cabinet.Roles != nil {
				if full, err := c.Cabinet.Roles.Principal(r.Context(), b.ID); err == nil {
					p = full
				} else {
					h.Log.Warn("principal", "err", err)
				}
			}
		}
	}
	if st != nil {
		st.p, st.signed, st.loaded = p, signed, true
	}
	return p, signed
}

// decorate fills the frame: the viewer, its CSRF token and a flash
// message from ?ok= or ?err= (dictionary keys only).
func (h *Handler) decorate(ctx context.Context, s *screens.Shell) {
	st := stateOf(ctx)
	if st == nil || st.r == nil {
		return
	}
	r := st.r
	q := r.URL.Query()
	for _, k := range []string{"ok", "err"} {
		if key := q.Get(k); strings.HasPrefix(key, "ui.") && h.Dict.T(key) != key {
			s.Flash, s.FlashError = h.Dict.T(key), k == "err"
		}
	}
	p, signed := h.principal(r)
	if !signed {
		return
	}
	v := screens.Viewer{Signed: true, Email: p.Email, Name: p.Name, CSRF: h.Commerce.Cabinet.csrf(st.w, r),
		Vendor: p.Has(cabinet.Vendor), Author: p.Has(cabinet.Author), Partner: p.Has(cabinet.Partner), Editor: p.Has(cabinet.Editor)}
	if len(p.Sellers) > 0 {
		v.SellerName = p.Sellers[0].Name
	}
	s.Viewer = v
}

// requireRole signs the user in first, then requires the role; a missing
// applyable role leads to the application page.
func (h *Handler) requireRole(w http.ResponseWriter, r *http.Request, role cabinet.Role) (cabinet.Principal, bool) {
	p, signed := h.principal(r)
	if !signed {
		http.Redirect(w, r, "/account?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
		return p, false
	}
	if !p.Has(role) {
		if role == cabinet.Editor {
			h.notice(w, r, http.StatusForbidden, "ui.error.forbidden")
		} else {
			http.Redirect(w, r, "/account/roles?err=ui.roles.required", http.StatusSeeOther)
		}
		return p, false
	}
	return p, true
}

// cabinetShell is the frame of a cabinet page (never cached).
func (h *Handler) cabinetShell(w http.ResponseWriter, r *http.Request, title string, workspace bool) (screens.Shell, bool) {
	s, _, err := h.shell(r.Context(), title, "account")
	if err != nil {
		h.fail(w, r, err)
		return s, false
	}
	s.Workspace = workspace
	w.Header().Set("Cache-Control", "no-store")
	return s, true
}

// done redirects after a successful post with a flash key.
func done(w http.ResponseWriter, r *http.Request, to, key string) {
	sep := "?"
	if strings.Contains(to, "?") {
		sep = "&"
	}
	http.Redirect(w, r, to+sep+"ok="+key, http.StatusSeeOther)
}

func failed(w http.ResponseWriter, r *http.Request, to, key string) {
	sep := "?"
	if strings.Contains(to, "?") {
		sep = "&"
	}
	http.Redirect(w, r, to+sep+"err="+key, http.StatusSeeOther)
}

func errKey(err error) string {
	switch {
	case errors.Is(err, cabinet.ErrInvalid):
		return "ui.form.invalid"
	case errors.Is(err, cabinet.ErrDenied):
		return "ui.error.forbidden"
	case errors.Is(err, cabinet.ErrNotFound):
		return "ui.error.not_found"
	}
	return "ui.form.failed"
}

// ---- buyer account ----

func (h *Handler) accountRegister(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuthLimit(w, r) || !h.parseForm(w, r) {
		return
	}
	c := h.Commerce.Cabinet
	next := account.SafeNext(r.PostFormValue("next"))
	if !c.SignupsOpen {
		h.signInPage(w, r, http.StatusForbidden, screens.SignInView{Next: next, ErrorKey: "ui.auth.signups_closed"})
		return
	}
	email, err := account.NormalizeEmail(r.PostFormValue("email"))
	if err != nil {
		h.signInPage(w, r, http.StatusUnprocessableEntity, screens.SignInView{Next: next, Register: true, ErrorKey: "ui.account.bad_email"})
		return
	}
	if r.PostFormValue("terms") != "1" {
		h.signInPage(w, r, http.StatusUnprocessableEntity, screens.SignInView{Email: email, Next: next, Register: true, ErrorKey: "ui.checkout.terms_required"})
		return
	}
	// The same answer for new and existing emails: the link proves the
	// address, so nobody learns which emails have accounts.
	u, _, err := c.Users.Ensure(r.Context(), email)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if c.Mailer != nil {
		if err := c.Mailer.SignInEmail(r.Context(), u, next); err != nil {
			h.Log.Warn("account: register email", "err", err)
		}
	}
	h.signInPage(w, r, http.StatusOK, screens.SignInView{Email: email, Next: next, InfoKey: "ui.account.link_sent"})
}

func (h *Handler) accountPurchases(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requireBuyer(w, r)
	if !ok {
		return
	}
	os, err := h.Commerce.Cabinet.Orders.Orders(r.Context(), b.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.cabinetShell(w, r, h.Dict.T("ui.account.purchases"), false)
	if !ok {
		return
	}
	h.render(w, r, http.StatusOK, screens.PurchasesPage(s, os))
}

func (h *Handler) accountSaved(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireBuyer(w, r); !ok {
		return
	}
	s, ok := h.cabinetShell(w, r, h.Dict.T("ui.account.saved"), false)
	if ok {
		h.render(w, r, http.StatusOK, screens.SavedPage(s))
	}
}

func (h *Handler) accountProfile(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requireBuyer(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		if !h.parseForm(w, r) {
			return
		}
		if err := h.Commerce.Cabinet.Roles.SetName(r.Context(), b.ID, r.PostFormValue("name")); err != nil {
			failed(w, r, "/account/profile", errKey(err))
			return
		}
		done(w, r, "/account/profile", "ui.form.saved")
		return
	}
	s, ok := h.cabinetShell(w, r, h.Dict.T("ui.account.profile"), false)
	if ok {
		h.render(w, r, http.StatusOK, screens.ProfilePage(s))
	}
}

func (h *Handler) accountSecurity(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requireBuyer(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		if !h.parseForm(w, r) {
			return
		}
		pw := r.PostFormValue("password")
		if pw != r.PostFormValue("password2") {
			failed(w, r, "/account/security", "ui.account.password_mismatch")
			return
		}
		if len(pw) < cabinet.MinPassword {
			failed(w, r, "/account/security", "ui.account.password_short")
			return
		}
		if err := h.Commerce.Cabinet.Roles.SetPassword(r.Context(), b.ID, pw); err != nil {
			failed(w, r, "/account/security", errKey(err))
			return
		}
		done(w, r, "/account/security", "ui.account.password_set")
		return
	}
	s, ok := h.cabinetShell(w, r, h.Dict.T("ui.account.security"), false)
	if ok {
		h.render(w, r, http.StatusOK, screens.SecurityPage(s))
	}
}

func (h *Handler) accountRoles(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requireBuyer(w, r)
	if !ok {
		return
	}
	c := h.Commerce.Cabinet
	if r.Method == http.MethodPost {
		if !h.parseForm(w, r) {
			return
		}
		if !c.SignupsOpen {
			failed(w, r, "/account/roles", "ui.roles.closed")
			return
		}
		err := c.Roles.Apply(r.Context(), cabinet.Application{UserID: b.ID, Email: b.Email, Role: r.PostFormValue("role"),
			StoreName: r.PostFormValue("store_name"), StoreSlug: r.PostFormValue("store_slug"),
			Website: r.PostFormValue("website"), Message: r.PostFormValue("message")})
		if err != nil {
			failed(w, r, "/account/roles", errKey(err))
			return
		}
		done(w, r, "/account/roles", "ui.roles.sent")
		return
	}
	p, _ := h.principal(r)
	apps, err := c.Roles.MyApplications(r.Context(), b.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.cabinetShell(w, r, h.Dict.T("ui.roles.title"), false)
	if ok {
		h.render(w, r, http.StatusOK, screens.RolesPage(s, screens.RolesView{P: p, Apps: apps, CSRF: s.Viewer.CSRF, Closed: !c.SignupsOpen}))
	}
}
