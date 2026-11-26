// Package web is the storefront HTTP layer: plain net/http handlers that
// render templ screens. PocketBase mounts them in cmd/market; tests use
// them directly with fake readers.
package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/storefront"
	"github.com/fastygo/market/internal/web/screens"
)

//go:embed static
var staticFS embed.FS

// Static returns the embedded static assets (compiled CSS).
func Static() fs.FS {
	sub, _ := fs.Sub(staticFS, "static")
	return sub
}

// Patterns are the storefront routes (Go 1.22 ServeMux syntax).
var Patterns = []string{
	"GET /{$}",
	"GET /catalog",
	"GET /catalog/{slug}",
	"GET /products/{slug}",
	"GET /products/{slug}/download",
	"GET /authors/{slug}",
	"GET /static/{path...}",
}

// Handler serves the storefront.
type Handler struct {
	Catalog        catalog.Reader
	Storefronts    storefront.Reader
	StorefrontCode string
	Dict           *i18n.Dict
	Now            func() time.Time
	Log            *slog.Logger
	Commerce       *Commerce // phase 1 routes; nil serves the catalog only
	// BaseURL is the public origin for absolute links (canonical, og:url,
	// JSON-LD): MARKET_PUBLIC_URL or its alias MARKET_BASE_URL, no trailing
	// slash. Empty (development) omits the absolute links.
	BaseURL string
	// Observe records request, render and DB metrics; nil disables it.
	Observe Observer
	mux     *http.ServeMux
}

// Observer is the metrics port (implemented by platform/metrics): Start
// wraps a request and returns done, called with the route pattern and the
// status; Render gets the templ render time alone (data is loaded before).
type Observer interface {
	Start(r *http.Request) (*http.Request, func(route string, status int))
	Render(route string, d time.Duration)
}

// statusWriter keeps the response status for the metrics.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// New builds the handler and its routes.
func New(h Handler) *Handler {
	if h.Now == nil {
		h.Now = time.Now
	}
	if h.Log == nil {
		h.Log = slog.Default()
	}
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", h.home)
	m.HandleFunc("GET /catalog", h.catalog)
	m.HandleFunc("GET /catalog/{slug}", h.catalog)
	m.HandleFunc("GET /products/{slug}", h.product)
	m.HandleFunc("GET /products/{slug}/download", h.download)
	m.HandleFunc("GET /authors/{slug}", h.author)
	h.mountCommerce(m)
	m.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(Static()))))
	h.mux = m
	return &h
}

type pathKey struct{}

// ServeHTTP keeps the request path in the context for the canonical link.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(context.WithValue(r.Context(), pathKey{}, r.URL.Path))
	r, nonce := withNonce(r)
	if !strings.HasPrefix(r.URL.Path, "/static/") {
		setCSP(w, nonce)
	}
	if h.Observe == nil {
		h.mux.ServeHTTP(w, r)
		return
	}
	r, done := h.Observe.Start(r)
	sw := &statusWriter{ResponseWriter: w}
	h.mux.ServeHTTP(sw, r) // sets r.Pattern
	if sw.code == 0 {
		sw.code = http.StatusOK
	}
	done(r.Pattern, sw.code)
}

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) shell(ctx context.Context, title, current string) (screens.Shell, storefront.Storefront, error) {
	sf, err := h.Storefronts.Storefront(ctx, h.StorefrontCode)
	if err != nil {
		return screens.Shell{}, sf, err
	}
	cats, err := h.Catalog.Categories(ctx)
	if err != nil {
		return screens.Shell{}, sf, err
	}
	if title == "" {
		title = sf.BrandName
	}
	path, _ := ctx.Value(pathKey{}).(string)
	return screens.Shell{
		Dict: h.Dict, Lang: h.Dict.Locale, Brand: sf.BrandName, Year: h.Now().Year(),
		Title: title, Current: current, CSSHref: skinHref(sf.Skin), Categories: cats,
		BrandInfo: sf.Brand, BaseURL: strings.TrimRight(h.BaseURL, "/"), Path: path,
		CSPNonce: nonceFrom(ctx),
	}, sf, nil
}

// skinHref is the compiled stylesheet of a storefront skin; unknown or
// empty skins fall back to the neutral default. The query is a digest of
// the embedded file, so a deploy never pairs new markup with a stylesheet
// cached under the same URL (static files are cached for an hour).
func skinHref(skin string) string {
	if skin != "" {
		if v := skinVersion("skins/" + skin + ".css"); v != "" {
			return "/static/skins/" + skin + ".css?v=" + v
		}
	}
	return "/static/skins/default.css?v=" + skinVersion("skins/default.css")
}

var skinVersions sync.Map // embedded path -> first 12 hex chars of its SHA-256 ("" when missing)

func skinVersion(name string) string {
	if v, ok := skinVersions.Load(name); ok {
		return v.(string)
	}
	v := ""
	if b, err := fs.ReadFile(Static(), name); err == nil {
		sum := sha256.Sum256(b)
		v = hex.EncodeToString(sum[:6])
	}
	skinVersions.Store(name, v)
	return v
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	var buf bytes.Buffer
	start := time.Now()
	ctx := r.Context()
	if n := nonceFrom(ctx); n != "" {
		ctx = templ.WithNonce(ctx, n)
	}
	err := c.Render(ctx, &buf)
	if h.Observe != nil {
		h.Observe.Render(r.Pattern, time.Since(start))
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.Log.Error("web", "path", r.URL.Path, "err", err)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

func (h *Handler) notice(w http.ResponseWriter, r *http.Request, status int, key string) {
	s, _, err := h.shell(r.Context(), h.Dict.T(key), "")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, status, screens.NoticePage(s, key))
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	s, sf, err := h.shell(r.Context(), "", "home")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ps, err := h.Catalog.Products(r.Context(), sf.Currency, catalog.Filter{Limit: 8})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s.JSONLD = screens.SiteJSONLD(s)
	h.render(w, r, http.StatusOK, screens.HomePage(s, ps))
}

func (h *Handler) catalog(w http.ResponseWriter, r *http.Request) {
	var active catalog.Category
	if slug := r.PathValue("slug"); slug != "" {
		c, err := h.Catalog.Category(r.Context(), slug)
		if errors.Is(err, catalog.ErrNotFound) {
			h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
			return
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		active = c
	}
	title := active.Title
	if title == "" {
		title = h.Dict.T("ui.catalog.title")
	}
	s, sf, err := h.shell(r.Context(), title, "catalog")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 100 {
		q = q[:100]
	}
	page := 1
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 1 {
			page = n
		}
	}
	if page > catalog.MaxPage {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	ps, err := h.Catalog.Products(r.Context(), sf.Currency, catalog.Filter{Category: active.Slug, Query: q,
		Limit: catalog.PageSize, Offset: (page - 1) * catalog.PageSize})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if page > 1 && len(ps) == 0 {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	h.render(w, r, http.StatusOK, screens.CatalogPage(s, active, q, ps))
}

func (h *Handler) loadProduct(w http.ResponseWriter, r *http.Request) (screens.Shell, catalog.Product, bool) {
	sf, err := h.Storefronts.Storefront(r.Context(), h.StorefrontCode)
	if err != nil {
		h.fail(w, r, err)
		return screens.Shell{}, catalog.Product{}, false
	}
	p, err := h.Catalog.Product(r.Context(), sf.Currency, r.PathValue("slug"))
	if errors.Is(err, catalog.ErrNotFound) {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return screens.Shell{}, p, false
	}
	if err != nil {
		h.fail(w, r, err)
		return screens.Shell{}, p, false
	}
	s, _, err := h.shell(r.Context(), p.Title, "catalog")
	if err != nil {
		h.fail(w, r, err)
		return s, p, false
	}
	return s, p, true
}

func (h *Handler) product(w http.ResponseWriter, r *http.Request) {
	s, p, ok := h.loadProduct(w, r)
	if !ok {
		return
	}
	author, more := h.authorOf(r.Context(), p)
	s.Description = p.Excerpt
	h.render(w, r, http.StatusOK, screens.ProductPage(s, p, h.checkoutView(w, r), author, more))
}

// authorOf loads the product's active seller profile and up to four more
// of the author's items; a seller without a public profile yields none.
func (h *Handler) authorOf(ctx context.Context, p catalog.Product) (catalog.Seller, []catalog.Product) {
	if p.SellerSlug == "" {
		return catalog.Seller{}, nil
	}
	a, err := h.Catalog.Seller(ctx, p.SellerSlug)
	if err != nil {
		if !errors.Is(err, catalog.ErrNotFound) {
			h.Log.Warn("web: author", "slug", p.SellerSlug, "err", err)
		}
		return catalog.Seller{}, nil
	}
	sf, err := h.Storefronts.Storefront(ctx, h.StorefrontCode)
	if err != nil {
		return a, nil
	}
	more, err := h.Catalog.Products(ctx, sf.Currency, catalog.Filter{Seller: a.Slug, Exclude: p.Slug, Limit: 4})
	if err != nil {
		h.Log.Warn("web: more from author", "slug", a.Slug, "err", err)
		return a, nil
	}
	return a, more
}

// author is the public seller profile (ADR-0020): only active sellers.
func (h *Handler) author(w http.ResponseWriter, r *http.Request) {
	a, err := h.Catalog.Seller(r.Context(), r.PathValue("slug"))
	if errors.Is(err, catalog.ErrNotFound) {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, sf, err := h.shell(r.Context(), a.DisplayName, "")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ps, err := h.Catalog.Products(r.Context(), sf.Currency, catalog.Filter{Seller: a.Slug})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s.Description = a.Headline
	h.render(w, r, http.StatusOK, screens.AuthorPage(s, a, ps))
}
