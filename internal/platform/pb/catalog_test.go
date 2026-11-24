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
	_ "github.com/fastygo/market/internal/migrations"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/web"
	"github.com/fastygo/market/ui/dict"
)

// newApp boots a PocketBase app in a temp dir and applies all migrations
// (system + ours, including the placeholder seed).
func newApp(t *testing.T) core.App {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
	return app
}

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
	if seller.GetString("status") != "draft" || seller.GetString("legal_name") != "" {
		t.Errorf("seed seller must be draft without legal data (ADR-0016)")
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
		{"/", 200, "Featured products"},
		{"/catalog", 200, "Aurora theme"},
		{"/catalog/plugins", 200, "SEO toolkit"},
		{"/products/aurora-theme", 200, `data-action="download"`},
		{"/products/aurora-theme", 200, `data-action="preview"`},
		{"/products/aurora-theme/download", 501, "phase 1"},
		{"/products/missing", 404, "Page not found."},
		{"/static/skins/godudy.css", 200, "Istok Web"},
		{"/", 200, `href="/static/skins/godudy.css"`},
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
