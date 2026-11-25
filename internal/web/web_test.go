package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/platform/money"
	"github.com/fastygo/market/internal/storefront"
	"github.com/fastygo/market/internal/web"
	"github.com/fastygo/market/ui/dict"
)

type fake struct{ products []catalog.Product }

func (f fake) Storefront(context.Context, string) (storefront.Storefront, error) {
	return storefront.Storefront{Code: "default", BrandName: "Brand", Currency: "RUB", DefaultLocale: "en",
		Brand: storefront.Brand{Name: "Brand", Tagline: "Brand tagline", AlternateNames: []string{"Brnd"}}}, nil
}
func (f fake) Categories(context.Context) ([]catalog.Category, error) {
	return []catalog.Category{{Slug: "themes", Title: "Themes"}}, nil
}
func (f fake) Category(_ context.Context, slug string) (catalog.Category, error) {
	if slug == "themes" {
		return catalog.Category{Slug: "themes", Title: "Themes"}, nil
	}
	return catalog.Category{}, catalog.ErrNotFound
}
func (f fake) Products(context.Context, string, catalog.Filter) ([]catalog.Product, error) {
	return f.products, nil
}
func (f fake) Product(_ context.Context, _ string, slug string) (catalog.Product, error) {
	for _, p := range f.products {
		if p.Slug == slug {
			return p, nil
		}
	}
	return catalog.Product{}, catalog.ErrNotFound
}

func handler(t *testing.T) *web.Handler { return handlerWith(t, "") }

func handlerWith(t *testing.T, baseURL string) *web.Handler {
	t.Helper()
	en, err := i18n.Parse(dict.EN)
	if err != nil {
		t.Fatal(err)
	}
	f := fake{products: []catalog.Product{{
		Slug: "p1", Title: "Product one", Excerpt: "Short.", ProductType: "plain",
		DemoURL: "https://example.com/demo", Category: catalog.Category{Slug: "themes", Title: "Themes"},
		SellerName: "Seller", Price: money.Money{Minor: 150000, Currency: "RUB"}, HasOffer: true,
	}}}
	return web.New(web.Handler{Catalog: f, Storefronts: f, StorefrontCode: "default", Dict: en,
		Now: func() time.Time { return time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC) }, BaseURL: baseURL})
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestProductPageRendersPlainTypeActions(t *testing.T) {
	rec := get(handler(t), "/products/p1")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	for _, want := range []string{
		`<h1`, "Product one", `href="/products/p1/download"`, `href="https://example.com/demo"`,
		`target="_blank"`, `data-product-type="plain"`, `id="main-content"`, `aria-current="page"`,
		"\u00a9 2026 Brand.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if n := strings.Count(body, "<main"); n != 1 {
		t.Errorf("want one main landmark, got %d", n)
	}
	if strings.Contains(body, "ui.") && regexp.MustCompile(`>ui\.[a-z_.]+<`).MatchString(body) {
		t.Error("unresolved dictionary key in output")
	}
}

func TestNotFound(t *testing.T) {
	h := handler(t)
	for _, p := range []string{"/products/none", "/catalog/none"} {
		if rec := get(h, p); rec.Code != 404 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}

// Every dictionary key used in the screens exists in the English source.
func TestDictionaryCoversScreens(t *testing.T) {
	en, _ := i18n.Parse(dict.EN)
	files, _ := filepath.Glob("screens/*.templ")
	re := regexp.MustCompile(`"(ui\.[a-z_]+\.[a-z_.]+)"`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if _, ok := en.Messages[m[1]]; !ok {
				t.Errorf("%s: key %s missing in ui/dict/en.json", f, m[1])
			}
		}
	}
}

// Title, og:site_name and JSON-LD come from the brand config; absolute
// links only with a base URL (ADR-0020).
func TestBrandMetaFromConfig(t *testing.T) {
	body := get(handlerWith(t, "https://market.example.com/"), "/").Body.String()
	for _, want := range []string{
		"<title>Brand | Brand tagline</title>", `<meta property="og:site_name" content="Brand">`,
		`<link rel="canonical" href="https://market.example.com/">`, `type="application/ld+json"`,
		`"@type":"WebSite"`, `"slogan":"Brand tagline"`, `"alternateName":["Brnd"]`,
		`"target":"https://market.example.com/catalog?q={search_term_string}"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	dev := get(handler(t), "/products/p1").Body.String()
	if strings.Contains(dev, `rel="canonical"`) || !strings.Contains(dev, "<title>Product one | Brand</title>") {
		t.Error("development: no canonical without a base URL; product title with brand")
	}
}
