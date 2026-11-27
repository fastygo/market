package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/editorial"
	"github.com/fastygo/market/internal/web/screens"
)

// mountContent serves the public editorial routes (ADR-0035).
func (h *Handler) mountContent(m *http.ServeMux) {
	if h.Editorial == nil {
		return
	}
	m.HandleFunc("GET /blog", h.blog)
	m.HandleFunc("GET /blog/{slug}", h.article)
	m.HandleFunc("GET /pages/{slug}", h.infoPage)
	m.HandleFunc("GET /collections", h.collections)
	m.HandleFunc("GET /collections/{slug}", h.collection)
}

func (h *Handler) contentLocs(ctx context.Context) []string {
	if h.Editorial == nil {
		return nil
	}
	locs := []string{"/blog", "/collections"}
	if as, err := h.Editorial.PublishedArticles(ctx, 500); err == nil {
		for _, a := range as {
			locs = append(locs, "/blog/"+a.Slug)
		}
	}
	if ps, err := h.Editorial.Pages(ctx); err == nil {
		for _, p := range ps {
			if p.Status == "published" {
				locs = append(locs, "/pages/"+p.Slug)
			}
		}
	}
	if cs, err := h.Editorial.PublishedCollections(ctx); err == nil {
		for _, c := range cs {
			locs = append(locs, "/collections/"+c.Slug)
		}
	}
	return locs
}

func (h *Handler) blog(w http.ResponseWriter, r *http.Request) {
	as, err := h.Editorial.PublishedArticles(r.Context(), 60)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, _, err := h.shell(r.Context(), h.Dict.T("ui.blog.title"), "blog")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, screens.BlogPage(s, as))
}

func (h *Handler) article(w http.ResponseWriter, r *http.Request) {
	a, err := h.Editorial.Article(r.Context(), r.PathValue("slug"))
	if errors.Is(err, editorial.ErrNotFound) {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, _, err := h.shell(r.Context(), a.Title, "blog")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s.Description = a.Excerpt
	h.render(w, r, http.StatusOK, screens.ArticlePage(s, a))
}

func (h *Handler) infoPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.Editorial.Page(r.Context(), r.PathValue("slug"))
	if errors.Is(err, editorial.ErrNotFound) || (err == nil && p.Status != "published") {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, _, err := h.shell(r.Context(), p.Title, "")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, screens.InfoPage(s, p))
}

func (h *Handler) collections(w http.ResponseWriter, r *http.Request) {
	cs, err := h.Editorial.PublishedCollections(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, _, err := h.shell(r.Context(), h.Dict.T("ui.collections.title"), "collections")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, screens.CollectionsPage(s, cs))
}

func (h *Handler) collection(w http.ResponseWriter, r *http.Request) {
	c, err := h.Editorial.Collection(r.Context(), r.PathValue("slug"))
	if errors.Is(err, editorial.ErrNotFound) || (err == nil && c.Status != "published") {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, sf, err := h.shell(r.Context(), c.Title, "collections")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var ps []catalog.Product
	if len(c.ProductIDs) > 0 {
		ps, err = h.Catalog.Products(r.Context(), sf.Currency, catalog.Filter{IDs: c.ProductIDs, Limit: 100})
		if err != nil {
			h.fail(w, r, err)
			return
		}
	}
	s.Description = c.Lead
	h.render(w, r, http.StatusOK, screens.CollectionPage(s, c, ps))
}

// productView is the product page state: reviews, the buyer's right to
// review (an active entitlement) and the download link.
func (h *Handler) productView(w http.ResponseWriter, r *http.Request, p catalog.Product, author catalog.Seller, more []catalog.Product) screens.ProductView {
	v := screens.ProductView{P: p, Author: author, More: more}
	if h.Editorial != nil {
		v.Reviews, _ = h.Editorial.ProductReviews(r.Context(), p.ID)
	}
	c := h.Commerce
	if c == nil || c.Cabinet == nil {
		return v
	}
	v.CSRF = c.Cabinet.csrf(w, r)
	if b, ok := c.buyer(r); ok && c.Cabinet.Orders != nil {
		if os, err := c.Cabinet.Orders.Orders(r.Context(), b.ID); err == nil {
			for _, o := range os {
				for _, it := range o.Items {
					if it.Slug == p.Slug && it.Entitlement == "active" {
						v.Download = true
						v.CanReview = h.Editorial != nil
					}
				}
			}
		}
	}
	return v
}

// checkoutPage is GET /checkout/{slug}: the order summary for a license.
func (h *Handler) checkoutPage(w http.ResponseWriter, r *http.Request) {
	h.renderCheckout(w, r, http.StatusOK, r.URL.Query().Get("license"), "")
}

func (h *Handler) renderCheckout(w http.ResponseWriter, r *http.Request, status int, license, errKey string) {
	s, p, ok := h.loadProduct(w, r)
	if !ok {
		return
	}
	if license != "extended" {
		license = "regular"
	}
	cv := h.checkoutView(w, r)
	cv.ErrorKey = errKey
	if errKey != "" {
		cv.Email = firstNonEmpty(cv.Email, r.PostFormValue("email"))
	}
	s.Title = h.Dict.T("ui.checkout.title") + ": " + p.Title
	w.Header().Set("Cache-Control", "no-store")
	h.render(w, r, status, screens.CheckoutPage(s, screens.CheckoutPageView{P: p, License: license, CV: cv}))
}

// productReview is POST /products/{slug}/reviews: buyers with an active
// entitlement; reviews wait for an editor.
func (h *Handler) productReview(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requireBuyer(w, r)
	if !ok || !h.parseForm(w, r) {
		return
	}
	_, p, ok := h.loadProduct(w, r)
	if !ok {
		return
	}
	v := h.productView(w, r, p, catalog.Seller{}, nil)
	back := screens.ProductHref(p.Slug)
	if !v.CanReview {
		http.Redirect(w, r, back+"?err=ui.review.denied#reviews", http.StatusSeeOther)
		return
	}
	rating, _ := strconv.Atoi(r.PostFormValue("rating"))
	err := h.Editorial.AddReview(r.Context(), editorial.Review{ProductID: p.ID, UserID: b.ID, Rating: rating,
		Body: strings.TrimSpace(r.PostFormValue("body"))})
	switch {
	case errors.Is(err, editorial.ErrInvalid):
		http.Redirect(w, r, back+"?err=ui.review.invalid#reviews", http.StatusSeeOther)
	case err != nil:
		h.Log.Warn("review", "err", err)
		http.Redirect(w, r, back+"?err=ui.review.exists#reviews", http.StatusSeeOther)
	default:
		http.Redirect(w, r, back+"?ok=ui.review.sent#reviews", http.StatusSeeOther)
	}
}

const refCookie = "market_ref"

// captureRef remembers a partner code from ?ref= for 30 days and counts
// the click; unknown codes are ignored.
func (h *Handler) captureRef(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("ref")
	if code == "" || r.Method != http.MethodGet || h.Commerce == nil || h.Commerce.Cabinet == nil || h.Commerce.Cabinet.Roles == nil {
		return
	}
	code = strings.ToLower(strings.TrimSpace(code))
	if len(code) > 32 {
		return
	}
	p, _, err := h.Commerce.Cabinet.Roles.PartnerByCode(r.Context(), code)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: refCookie, Value: url.QueryEscape(p.Code), Path: "/", MaxAge: int((30 * 24 * time.Hour).Seconds()),
		HttpOnly: true, Secure: h.Commerce.Cabinet.Secure, SameSite: http.SameSiteLaxMode})
	if err := h.Commerce.Cabinet.Roles.RecordClick(r.Context(), p.ID, r.URL.Path, h.Commerce.hash(h.Commerce.clientIP(r))); err != nil {
		h.Log.Warn("referral click", "err", err)
	}
}

// referral is the order snapshot of the remembered partner; partners do
// not earn on their own purchases.
func (h *Handler) referral(r *http.Request, buyerID string) map[string]any {
	c := h.Commerce.Cabinet
	if c.Roles == nil {
		return nil
	}
	ck, err := r.Cookie(refCookie)
	if err != nil || ck.Value == "" {
		return nil
	}
	code, _ := url.QueryUnescape(ck.Value)
	p, userID, err := c.Roles.PartnerByCode(r.Context(), code)
	if err != nil || userID == buyerID {
		return nil
	}
	return map[string]any{"partner": p.ID, "code": p.Code, "rate_bps": p.RateBPS}
}

// parseMinor reads a decimal amount ("29", "29.9", "29.90") as minor units.
func parseMinor(s string) (int64, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" {
		return 0, false
	}
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		return 0, false
	}
	for len(frac) < 2 {
		frac += "0"
	}
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil || n < 0 || n > 100_000_000_00 {
		return 0, false
	}
	return n, true
}
