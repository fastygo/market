package web_test

import (
	"strings"
	"testing"
	"time"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/platform/money"
	"github.com/fastygo/market/internal/web"
	"github.com/fastygo/market/ui/dict"
)

func TestRobotsSitemapFavicon(t *testing.T) {
	h := handlerWith(t, "https://market.example.com")
	rec := get(h, "/robots.txt")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Sitemap: https://market.example.com/sitemap.xml") {
		t.Fatalf("robots: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Disallow: /_/") {
		t.Fatal("robots should disallow the admin UI")
	}
	rec = get(h, "/sitemap.xml")
	body := rec.Body.String()
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/xml; charset=utf-8" {
		t.Fatalf("sitemap: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, loc := range []string{
		"https://market.example.com/",
		"https://market.example.com/catalog",
		"https://market.example.com/catalog/themes",
		"https://market.example.com/products/p1",
		"https://market.example.com/authors/seller",
	} {
		if !strings.Contains(body, loc) {
			t.Fatalf("sitemap missing %s in %s", loc, body)
		}
	}
	for _, path := range []string{"/favicon.svg", "/favicon.ico", "/apple-touch-icon.png"} {
		rec = get(h, path)
		if rec.Code != 200 || rec.Body.Len() < 16 {
			t.Fatalf("%s: %d len=%d", path, rec.Code, rec.Body.Len())
		}
	}
}

func TestServerTimingAndImmutableCSS(t *testing.T) {
	h := handler(t)
	rec := get(h, "/")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	st := rec.Header().Get("Server-Timing")
	if !strings.HasPrefix(st, "render;dur=") {
		t.Fatalf("Server-Timing %q", st)
	}
	href := rec.Body.String()
	i := strings.Index(href, `/static/skins/`)
	if i < 0 {
		t.Fatal("no skin href")
	}
	j := strings.Index(href[i:], `"`)
	cssURL := href[i : i+j]
	rec = get(h, cssURL)
	if rec.Code != 200 {
		t.Fatalf("css %s: %d", cssURL, rec.Code)
	}
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "immutable") || !strings.Contains(cc, "31536000") {
		t.Fatalf("fingerprinted css cache-control %q", cc)
	}
	// without a matching digest the cache stays short
	rec = get(h, strings.Split(cssURL, "?")[0])
	cc = rec.Header().Get("Cache-Control")
	if strings.Contains(cc, "immutable") {
		t.Fatalf("unfingerprinted css should not be immutable: %q", cc)
	}
}

func TestCatalogHeadingAndLCPHints(t *testing.T) {
	en, err := i18n.Parse(dict.EN)
	if err != nil {
		t.Fatal(err)
	}
	f := fake{products: []catalog.Product{{
		Slug: "p1", Title: "Product one", Excerpt: "Short.", ProductType: "plain",
		CoverURL: "/api/files/col/id/cover.png", CoverSrcset: "/media/covers/id/abc123abc123/w800.webp 800w",
		Category:   catalog.Category{Slug: "themes", Title: "Themes"},
		SellerName: "Seller", SellerSlug: "seller", Price: money.Money{Minor: 150000, Currency: "RUB"}, HasOffer: true,
	}}}
	h := web.New(web.Handler{Catalog: f, Storefronts: f, StorefrontCode: "default", Dict: en,
		Now: func() time.Time { return time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC) }})

	rec := get(h, "/catalog")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if !strings.Contains(body, `fetchpriority="high"`) || !strings.Contains(body, `loading="eager"`) {
		t.Fatal("first catalog card should be the LCP image")
	}
	if !strings.Contains(body, `type="image/webp"`) || !strings.Contains(body, `w800.webp`) {
		t.Fatal("catalog should expose the WebP srcset")
	}
	// page h1 is the catalog title; with an empty grid heading, card titles are h2
	if !strings.Contains(body, `>Product one</`) {
		t.Fatal("missing product title")
	}
	if !strings.Contains(body, "rel=\"preload\"") || !strings.Contains(body, "font/woff2") {
		t.Fatal("missing font preload")
	}

	home := get(h, "/").Body.String()
	if strings.Contains(home, `fetchpriority="high"`) {
		t.Fatal("home cards are below the fold and must stay lazy")
	}

	prod := get(h, "/products/p1").Body.String()
	if !strings.Contains(prod, `fetchpriority="high"`) || !strings.Contains(prod, `loading="eager"`) {
		t.Fatal("product hero should be the LCP image")
	}
}

func TestNewRoutesAreForwarded(t *testing.T) {
	joined := strings.Join(web.AllPatterns(), "\n")
	for _, p := range []string{
		"GET /robots.txt", "GET /sitemap.xml", "GET /favicon.ico",
		"GET /favicon.svg", "GET /apple-touch-icon.png",
		"GET /media/covers/{id}/{hash}/{file}",
	} {
		if !strings.Contains(joined, p) {
			t.Fatalf("%s missing in AllPatterns (PocketBase router)", p)
		}
	}
}
