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
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	pathpkg "path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/editorial"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/media"
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
	"GET /blog",
	"GET /blog/{slug}",
	"GET /pages/{slug}",
	"GET /collections",
	"GET /collections/{slug}",
	"GET /static/{path...}",
	"GET /robots.txt",
	"GET /sitemap.xml",
	"GET /favicon.ico",
	"GET /favicon.svg",
	"GET /apple-touch-icon.png",
	"GET /media/covers/{id}/{hash}/{file}",
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
	// Covers serves resized WebP product covers; nil hides /media/covers.
	Covers *media.Store
	// Editorial serves the blog, legal pages, collections and reviews
	// (ADR-0035); nil hides those routes.
	Editorial editorial.Store
	mux       *http.ServeMux
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
	if h.Dict != nil {
		screens.SetMoneyLocale(h.Dict.Locale)
	}
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
	h.mountContent(m)
	h.mountCommerce(m)
	root := Static()
	m.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(noDirListing(root, http.FileServerFS(root)))))
	m.HandleFunc("GET /robots.txt", h.robots)
	m.HandleFunc("GET /sitemap.xml", h.sitemap)
	m.HandleFunc("GET /favicon.ico", h.faviconICO)
	m.HandleFunc("GET /favicon.svg", h.faviconSVG)
	m.HandleFunc("GET /apple-touch-icon.png", h.faviconPNG)
	if h.Covers != nil {
		m.Handle("GET /media/covers/{id}/{hash}/{file}", h.Covers.Handler())
	}
	h.mux = m
	return &h
}

type pathKey struct{}

// ServeHTTP keeps the request path in the context for the canonical link.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(context.WithValue(r.Context(), pathKey{}, r.URL.Path))
	r, nonce := withNonce(r)
	r = withRequest(w, r)
	h.captureRef(w, r)
	setSecurityHeaders(w)
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
		// A matching ?v=<12-hex> digest (skins and fingerprinted fonts) is
		// content-addressed, so it can be cached for a year as immutable.
		v := r.URL.Query().Get("v")
		name := strings.TrimPrefix(pathpkg.Clean("/"+r.URL.Path), "/")
		if len(v) == 12 && assetVersion(name) == v {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		next.ServeHTTP(w, r)
	})
}

func noDirListing(fsys fs.FS, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(pathpkg.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "." {
			http.NotFound(w, r)
			return
		}
		info, err := fs.Stat(fsys, name)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
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
	sh := screens.Shell{
		Dict: h.Dict, Lang: h.Dict.Locale, Brand: sf.BrandName, Year: h.Now().Year(),
		Title: title, Current: current, CSSHref: skinHref(sf.Skin), Categories: cats,
		BrandInfo: sf.Brand, BaseURL: strings.TrimRight(h.BaseURL, "/"), Path: path,
		CSPNonce: nonceFrom(ctx), FontPreloads: fontPreloads(), AppJS: "/static/app.js?v=" + assetVersion("app.js"),
	}
	h.decorate(ctx, &sh)
	return sh, sf, nil
}

// skinHref is the compiled stylesheet of a storefront skin; unknown or
// empty skins fall back to the neutral default. The query is a digest of
// the embedded file, so a deploy never pairs new markup with a stylesheet
// cached under the same URL.
func skinHref(skin string) string {
	if skin != "" {
		if v := assetVersion("skins/" + skin + ".css"); v != "" {
			return "/static/skins/" + skin + ".css?v=" + v
		}
	}
	return "/static/skins/default.css?v=" + assetVersion("skins/default.css")
}

var assetVersions sync.Map // embedded path -> first 12 hex chars of its SHA-256 ("" when missing)

func assetVersion(name string) string {
	if v, ok := assetVersions.Load(name); ok {
		return v.(string)
	}
	v := ""
	if b, err := fs.ReadFile(Static(), name); err == nil {
		sum := sha256.Sum256(b)
		v = hex.EncodeToString(sum[:6])
	}
	assetVersions.Store(name, v)
	return v
}

// fontPreloads are the Istok Web files used on first paint (latin and
// cyrillic, 400 and 700). Other families load on demand via unicode-range.
func fontPreloads() []string {
	names := []string{
		"fonts/istok-web-400-latin.woff2",
		"fonts/istok-web-400-cyrillic.woff2",
		"fonts/istok-web-700-latin.woff2",
		"fonts/istok-web-700-cyrillic.woff2",
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if v := assetVersion(n); v != "" {
			out = append(out, "/static/"+n+"?v="+v)
		}
	}
	return out
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
	ms := float64(time.Since(start).Microseconds()) / 1000
	w.Header().Set("Server-Timing", fmt.Sprintf("render;dur=%.2f", ms))
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
	v := screens.HomeView{Latest: ps}
	if h.Editorial != nil {
		if cs, err := h.Editorial.PublishedCollections(r.Context()); err == nil {
			v.Collections = cs
		}
		if as, err := h.Editorial.PublishedArticles(r.Context(), 3); err == nil {
			v.Articles = as
		}
	}
	// the promoted author is the seller with the most items on the page
	count := map[string]int{}
	best := ""
	for _, p := range ps {
		if p.SellerSlug == "" {
			continue
		}
		count[p.SellerSlug]++
		if count[p.SellerSlug] > count[best] {
			best = p.SellerSlug
		}
	}
	if best != "" {
		if a, err := h.Catalog.Seller(r.Context(), best); err == nil {
			v.Author = a
			v.Works, _ = h.Catalog.Products(r.Context(), sf.Currency, catalog.Filter{Seller: best, Limit: 3})
		}
	}
	s.JSONLD = screens.SiteJSONLD(s)
	h.render(w, r, http.StatusOK, screens.HomePage(s, v))
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
	qv := r.URL.Query()
	f := catalog.Filter{Category: active.Slug, Query: q, Limit: catalog.PageSize + 1, Offset: (page - 1) * catalog.PageSize,
		Free: qv.Get("free") == "1"}
	switch so := qv.Get("sort"); so {
	case "price", "-price", "popular", "rating":
		f.Sort = so
	}
	f.MinPrice, _ = parseMinor(qv.Get("min"))
	f.MaxPrice, _ = parseMinor(qv.Get("max"))
	ps, err := h.Catalog.Products(r.Context(), sf.Currency, f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if page > 1 && len(ps) == 0 {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	if len(ps) > 0 && ps[0].CoverURL != "" {
		s.LCPImage = ps[0].CoverURL
		s.LCPSrcset = ps[0].CoverSrcset
		s.LCPSizes = media.CardSizes
	}
	more := len(ps) > catalog.PageSize
	if more {
		ps = ps[:catalog.PageSize]
	}
	h.render(w, r, http.StatusOK, screens.CatalogPage(s, screens.CatalogView{Active: active, Query: q, Sort: f.Sort,
		Min: qv.Get("min"), Max: qv.Get("max"), Free: f.Free, Products: ps, Page: page, More: more}))
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
	if p.CoverURL != "" {
		s.LCPImage = p.CoverURL
		s.LCPSrcset = p.CoverSrcset
		s.LCPSizes = media.HeroSizes
		s.Image = p.CoverURL
	}
	h.render(w, r, http.StatusOK, screens.ProductPage(s, h.productView(w, r, p, author, more)))
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

func (h *Handler) robots(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString("User-agent: *\nAllow: /\nDisallow: /account\nDisallow: /checkout\nDisallow: /seller\nDisallow: /studio\nDisallow: /partner\nDisallow: /editor\nDisallow: /_/\n")
	if u := strings.TrimRight(h.BaseURL, "/"); u != "" {
		b.WriteString("Sitemap: " + u + "/sitemap.xml\n")
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(b.String()))
}

type sitemapURL struct {
	XMLName xml.Name `xml:"url"`
	Loc     string   `xml:"loc"`
}

func (h *Handler) sitemap(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(h.BaseURL, "/")
	if base == "" && r.Host != "" {
		scheme := "https"
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
			scheme = "http"
		}
		base = scheme + "://" + r.Host
	}
	s, sf, err := h.shell(r.Context(), "", "")
	locs := []string{"/", "/catalog"}
	locs = append(locs, h.contentLocs(r.Context())...)
	if err == nil {
		for _, c := range s.Categories {
			if c.Slug != "" {
				locs = append(locs, "/catalog/"+c.Slug)
			}
		}
		ps, err := h.Catalog.Products(r.Context(), sf.Currency, catalog.Filter{Limit: 5000})
		if err == nil {
			for _, p := range ps {
				locs = append(locs, "/products/"+p.Slug)
				if p.SellerSlug != "" {
					locs = append(locs, "/authors/"+p.SellerSlug)
				}
			}
		}
	}
	seen := map[string]bool{}
	var urls []sitemapURL
	for _, loc := range locs {
		if seen[loc] {
			continue
		}
		seen[loc] = true
		urls = append(urls, sitemapURL{Loc: base + loc})
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write([]byte(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`))
	for _, u := range urls {
		b, err := xml.Marshal(u)
		if err != nil {
			continue
		}
		_, _ = w.Write(b)
	}
	_, _ = w.Write([]byte(`</urlset>`))
}

func (h *Handler) faviconSVG(w http.ResponseWriter, r *http.Request) {
	h.writeStatic(w, "favicon.svg", "image/svg+xml")
}

func (h *Handler) faviconICO(w http.ResponseWriter, r *http.Request) {
	h.writeStatic(w, "favicon.png", "image/png")
}

func (h *Handler) faviconPNG(w http.ResponseWriter, r *http.Request) {
	h.writeStatic(w, "favicon-180.png", "image/png")
}

func (h *Handler) writeStatic(w http.ResponseWriter, name, contentType string) {
	b, err := fs.ReadFile(Static(), name)
	if err != nil {
		http.NotFound(w, &http.Request{})
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(b)
}
