package web_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/platform/money"
	"github.com/fastygo/market/internal/web"
	"github.com/fastygo/market/ui/dict"
)

// benchHandler: a storefront with a full catalog page (catalog.PageSize cards).
func benchHandler(b *testing.B) *web.Handler {
	b.Helper()
	en, err := i18n.Parse(dict.EN)
	if err != nil {
		b.Fatal(err)
	}
	ps := make([]catalog.Product, catalog.PageSize)
	for i := range ps {
		ps[i] = catalog.Product{Slug: fmt.Sprintf("p%d", i), Title: fmt.Sprintf("Product %d", i), Excerpt: "Short.",
			ProductType: "plain", Category: catalog.Category{Slug: "themes", Title: "Themes"}, SellerName: "Seller",
			SellerSlug: "seller", Price: money.Money{Minor: int64(1000 * (i + 1)), Currency: "RUB"}, HasOffer: true}
	}
	f := fake{products: ps}
	return web.New(web.Handler{Catalog: f, Storefronts: f, StorefrontCode: "default", Dict: en,
		Now: func() time.Time { return time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC) }})
}

// BenchmarkRender: server render time of the main pages (ADR-0029 measures it before and after the semantic class mode).
func BenchmarkRender(b *testing.B) {
	h := benchHandler(b)
	for _, path := range []string{"/", "/catalog", "/products/p1", "/authors/seller"} {
		b.Run(path, func(b *testing.B) {
			b.ReportAllocs()
			size := 0
			for b.Loop() {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				if rec.Code != http.StatusOK {
					b.Fatalf("%s: %d", path, rec.Code)
				}
				size = rec.Body.Len()
			}
			b.ReportMetric(float64(size), "html-bytes")
		})
	}
}
