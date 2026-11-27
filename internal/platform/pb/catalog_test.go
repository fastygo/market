package pb_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/pb/pbtest"
	"github.com/fastygo/market/internal/web"
	"github.com/fastygo/market/ui/dict"
)

func newApp(t *testing.T) core.App { return pbtest.New(t) }

func TestMigrationsAndSeed(t *testing.T) {
	app := newApp(t)
	for _, name := range []string{"storefronts", "sellers", "categories", "products", "product_releases", "offers"} {
		c, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			t.Fatalf("collection %s: %v", name, err)
		}
		if c.ListRule != nil || c.ViewRule != nil || c.CreateRule != nil {
			t.Errorf("%s: API rules must be closed (superuser only)", name)
		}
	}
	seller, err := app.FindFirstRecordByData("sellers", "slug", "godudy")
	if err != nil {
		t.Fatal(err)
	}
	// ADR-0020 slice 1: GoDudY is an active seller with a pseudonymous
	// public profile and still no legal data (ADR-0016)
	if seller.GetString("status") != "active" || seller.GetString("legal_name") != "" ||
		seller.GetString("display_name") != "GoDudY" || seller.GetString("headline") == "" {
		t.Errorf("seed seller must be active, pseudonymous, without legal data: %v", seller.FieldsData())
	}
	// unique (product, license_type, currency) on offers
	off, err := app.FindFirstRecordByFilter("offers", "license_type = 'regular'")
	if err != nil {
		t.Fatal(err)
	}
	dup := core.NewRecord(off.Collection())
	dup.Load(map[string]any{"product": off.GetString("product"), "seller": off.GetString("seller"),
		"license_type": "regular", "currency": "RUB", "tax_category": "standard", "price_minor": 1, "active": true})
	if err := app.Save(dup); err == nil {
		t.Error("duplicate offer saved; unique index missing")
	}
}

func TestCatalogRepository(t *testing.T) {
	app := newApp(t)
	repo := &pb.Catalog{App: app}
	ctx := context.Background()

	sf, err := repo.Storefront(ctx, "default")
	if err != nil || sf.Currency != "RUB" || sf.CommerceMode != "single_store" {
		t.Fatalf("storefront: %+v %v", sf, err)
	}
	all, err := repo.Products(ctx, sf.Currency, catalog.Filter{})
	if err != nil || len(all) != 6 {
		t.Fatalf("products: %d %v", len(all), err)
	}
	themes, _ := repo.Products(ctx, sf.Currency, catalog.Filter{Category: "themes"})
	if len(themes) != 2 {
		t.Errorf("themes: %d", len(themes))
	}
	p, err := repo.Product(ctx, sf.Currency, "aurora-theme")
	if err != nil || !p.HasOffer || p.Price.Minor != 290000 || p.ProductType != "plain" {
		t.Fatalf("product: %+v %v", p, err)
	}
	if _, err := repo.Product(ctx, "EUR", "aurora-theme"); err != nil {
		t.Fatal(err)
	} // product exists, no EUR offer
	if _, err := repo.Product(ctx, sf.Currency, "missing"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("missing product: %v", err)
	}
	if p.SellerSlug != "godudy" || p.SellerName != "GoDudY" {
		t.Errorf("product author: %q %q", p.SellerSlug, p.SellerName)
	}

	// search over title, excerpt, category and author name; LIKE wildcards are literal
	for q, want := range map[string]int{"aurora": 1, "THEME": 2, "godudy": 6, "plugins": 2, "%": 0, "_": 0, "  ": 6} {
		got, err := repo.Products(ctx, sf.Currency, catalog.Filter{Query: q})
		if err != nil || len(got) != want {
			t.Errorf("query %q: %d products, want %d (%v)", q, len(got), want, err)
		}
	}
	more, _ := repo.Products(ctx, sf.Currency, catalog.Filter{Seller: "godudy", Exclude: "aurora-theme", Limit: 3})
	if len(more) != 3 {
		t.Errorf("more from author: %d", len(more))
	}
	for _, m := range more {
		if m.Slug == "aurora-theme" {
			t.Error("excluded product listed")
		}
	}
	if none, _ := repo.Products(ctx, sf.Currency, catalog.Filter{Seller: "nobody"}); len(none) != 0 {
		t.Errorf("unknown seller: %d", len(none))
	}

	s, err := repo.Seller(ctx, "godudy")
	if err != nil || s.DisplayName != "GoDudY" || s.ItemsCount != 6 || s.JoinedAt.IsZero() || s.Bio == "" {
		t.Fatalf("seller: %+v %v", s, err)
	}
	if _, err := repo.Seller(ctx, "missing"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("missing seller: %v", err)
	}
	rec, _ := app.FindFirstRecordByData("sellers", "slug", "godudy")
	rec.Set("status", "suspended")
	if err := app.Save(rec); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Seller(ctx, "godudy"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("suspended seller must not have a public profile: %v", err)
	}
}

func TestPagesEndToEnd(t *testing.T) {
	app := newApp(t)
	repo := &pb.Catalog{App: app}
	en, err := i18n.Parse(dict.EN)
	if err != nil {
		t.Fatal(err)
	}
	h := web.New(web.Handler{Catalog: repo, Storefronts: repo, StorefrontCode: "default", Dict: en})
	cases := []struct {
		path string
		code int
		want string
	}{
		{"/", 200, "New in the catalog"},
		{"/", 200, `href="/authors/godudy"`},
		{"/", 200, `role="search"`},
		{"/catalog?q=seo", 200, "SEO toolkit"},
		{"/catalog?q=seo", 200, `value="seo"`},
		{"/catalog/plugins?q=forms", 200, "Forms lite"},
		{"/products/aurora-theme", 200, `href="/authors/godudy"`},
		{"/products/aurora-theme", 200, "More from the author"},
		{"/authors/godudy", 200, "<title>GoDudY | StorEEE Market</title>"},
		{"/authors/godudy", 200, "6 items"},
		{"/authors/godudy", 200, "Shop starter"},
		{"/authors/missing", 404, "Page not found."},
		{"/catalog", 200, "Aurora theme"},
		{"/catalog/plugins", 200, "SEO toolkit"},
		{"/products/aurora-theme", 200, `id="gallery-main"`},
		{"/products/aurora-theme", 200, `href="/checkout/aurora-theme`},
		{"/products/aurora-theme/download", 501, "not available"},
		{"/products/missing", 404, "Page not found."},
		{"/static/skins/godudy.css", 200, "Istok Web"},
		{"/static/skins/storeee.css", 200, "Istok Web"},
		{"/", 200, `href="/static/skins/storeee.css?v=`},
		{"/", 200, `<meta property="og:site_name" content="StorEEE Market">`},
		{"/", 200, "Easy to find, Easy to learn, Easy to Sell"},
		{"/products/aurora-theme", 200, "<title>Aurora theme | StorEEE Market</title>"},
		{"/static/fonts/istok-web-400-latin.woff2", 200, ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s: code %d, want %d containing %q", c.path, rec.Code, c.code, c.want)
		}
	}
}
