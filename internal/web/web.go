// Package web is the storefront HTTP layer: plain net/http handlers that
// render templ screens. PocketBase mounts them in cmd/market; tests use
// them directly with fake readers.
package web

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
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
	mux            *http.ServeMux
}

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
	m.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(Static()))))
	h.mux = m
	return &h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

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
	return screens.Shell{
		Dict: h.Dict, Lang: h.Dict.Locale, Brand: sf.BrandName, Year: h.Now().Year(),
		Title: title, Current: current, CSSHref: "/static/app.css", Categories: cats,
	}, sf, nil
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	var buf bytes.Buffer
	if err := c.Render(r.Context(), &buf); err != nil {
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
	ps, err := h.Catalog.Products(r.Context(), sf.Currency, catalog.Filter{Category: active.Slug})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, screens.CatalogPage(s, active, ps))
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
	h.render(w, r, http.StatusOK, screens.ProductPage(s, p))
}

// download is the plain product type delivery entry. Phase 0 has no
// entitlements yet, so it answers 501 with a notice (ADR-0009, ADR-0019).
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.loadProduct(w, r); !ok {
		return
	}
	h.notice(w, r, http.StatusNotImplemented, "ui.product.download_soon")
}
