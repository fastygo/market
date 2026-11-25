package web_test

import (
	"context"
	"fmt"
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

type fake struct {
	products []catalog.Product
	last     *catalog.Filter // the last product filter, for assertions
}

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
func (f fake) Products(_ context.Context, _ string, flt catalog.Filter) ([]catalog.Product, error) {
	if f.last != nil {
		*f.last = flt
	}
	return f.products, nil
}
func (f fake) Seller(_ context.Context, slug string) (catalog.Seller, error) {
	if slug == "seller" {
		return catalog.Seller{ID: "s1", Slug: "seller", DisplayName: "Seller", Headline: "Seller headline",
			Bio: "First paragraph.\n\nSecond paragraph.", ItemsCount: len(f.products),
			JoinedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}, nil
	}
	return catalog.Seller{}, catalog.ErrNotFound
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

var lastFilter = &catalog.Filter{}

func handlerWith(t *testing.T, baseURL string) *web.Handler {
	t.Helper()
	en, err := i18n.Parse(dict.EN)
	if err != nil {
		t.Fatal(err)
	}
	f := fake{products: []catalog.Product{{
		Slug: "p1", Title: "Product one", Excerpt: "Short.", ProductType: "plain",
		DemoURL: "https://example.com/demo", Category: catalog.Category{Slug: "themes", Title: "Themes"},
		SellerName: "Seller", SellerSlug: "seller", Price: money.Money{Minor: 150000, Currency: "RUB"}, HasOffer: true,
	}}, last: lastFilter}
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

func TestHomeShowsSearchCategoriesAndAuthors(t *testing.T) {
	body := get(handler(t), "/").Body.String()
	for _, want := range []string{
		`<search`, `action="/catalog"`, `name="q"`, `type="search"`, "Brand tagline", "Browse categories",
		`href="/catalog/themes"`, "New items", `data-slot="item-card"`, `href="/authors/seller"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestCatalogSearchKeepsQuery(t *testing.T) {
	rec := get(handler(t), "/catalog/themes?q=%20dark%20theme%20")
	body := rec.Body.String()
	if rec.Code != 200 || lastFilter.Query != "dark theme" || lastFilter.Category != "themes" {
		t.Fatalf("code %d filter %+v", rec.Code, *lastFilter)
	}
	for _, want := range []string{`value="dark theme"`, `href="/catalog/themes?q=dark+theme"`, "Results for: dark theme"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestProductPageAuthorBlock(t *testing.T) {
	body := get(handler(t), "/products/p1").Body.String()
	for _, want := range []string{`data-slot="author-card"`, `href="/authors/seller"`, "Seller headline", "More from Seller", "1 item"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if lastFilter.Seller != "seller" || lastFilter.Exclude != "p1" || lastFilter.Limit != 4 {
		t.Errorf("more-from filter %+v", *lastFilter)
	}
}

func TestAuthorProfile(t *testing.T) {
	h := handler(t)
	rec := get(h, "/authors/seller")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	for _, want := range []string{
		"<title>Seller | Brand</title>", `<h1`, "Seller headline", "First paragraph.", "Second paragraph.",
		"Portfolio", "1 item", "Member since 2026-11-01", `href="/products/p1"`, `<meta name="description" content="Seller headline">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if n := strings.Count(body, "<main"); n != 1 {
		t.Errorf("want one main landmark, got %d", n)
	}
	if regexp.MustCompile(`>ui\.[a-z_.]+<`).MatchString(body) {
		t.Error("unresolved dictionary key in output")
	}
	if rec := get(h, "/authors/nobody"); rec.Code != 404 {
		t.Errorf("unknown author: %d", rec.Code)
	}
}

func TestAuthorRouteIsForwarded(t *testing.T) {
	found := false
	for _, p := range web.AllPatterns() {
		found = found || p == "GET /authors/{slug}"
	}
	if !found {
		t.Error("GET /authors/{slug} missing in web.AllPatterns (PocketBase router)")
	}
}

type obs struct {
	routes  []string
	codes   []int
	renders []string
}

func (o *obs) Start(r *http.Request) (*http.Request, func(string, int)) {
	return r, func(route string, code int) { o.routes, o.codes = append(o.routes, route), append(o.codes, code) }
}
func (o *obs) Render(route string, _ time.Duration) { o.renders = append(o.renders, route) }

func TestObserverGetsRoutePatternAndStatus(t *testing.T) {
	h := handler(t)
	o := &obs{}
	h.Observe = o
	get(h, "/products/missing")
	get(h, "/catalog/themes")
	if want := []string{"GET /products/{slug}", "GET /catalog/{slug}"}; strings.Join(o.routes, ",") != strings.Join(want, ",") {
		t.Fatalf("routes = %v, want %v", o.routes, want)
	}
	if o.codes[0] != http.StatusNotFound || o.codes[1] != http.StatusOK {
		t.Fatalf("codes = %v", o.codes)
	}
	if len(o.renders) != 2 || o.renders[1] != "GET /catalog/{slug}" {
		t.Fatalf("renders = %v", o.renders)
	}
}

func TestCatalogPages(t *testing.T) {
	h := handler(t)
	if rec := get(h, "/catalog?page=1"); rec.Code != http.StatusOK || lastFilter.Offset != 0 || lastFilter.Limit != catalog.PageSize {
		t.Fatalf("page 1: %d %+v", rec.Code, *lastFilter)
	}
	get(h, "/catalog?page=x")
	if lastFilter.Offset != 0 {
		t.Fatalf("bad page number: offset %d", lastFilter.Offset)
	}
	// the fake returns the same product for any page, so page 3 renders
	if rec := get(h, "/catalog?page=3"); rec.Code != http.StatusOK || lastFilter.Offset != 2*catalog.PageSize {
		t.Fatalf("page 3: %d offset %d", rec.Code, lastFilter.Offset)
	}
	if rec := get(h, fmt.Sprintf("/catalog?page=%d", catalog.MaxPage+1)); rec.Code != http.StatusNotFound {
		t.Fatalf("page over the cap: %d", rec.Code)
	}
}
