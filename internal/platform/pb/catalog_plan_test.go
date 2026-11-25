package pb_test

import (
	"context"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/platform/pb"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
)

// The catalog list queries read products in index order (no temp b-tree
// sort of the whole catalog) with products as the outer loop, whatever
// the planner stats say (migration 1764000008, load test 2026-11-25).
func TestCatalogQueryPlans(t *testing.T) {
	app := newApp(t)
	const sel = `SELECT p.id FROM products p CROSS JOIN sellers s ON s.id = p.seller
		LEFT JOIN categories c ON c.id = p.category WHERE p.status = 'published'`
	const order = ` ORDER BY p.published_at DESC, p.created DESC LIMIT 100`
	for name, c := range map[string]struct{ sql, index string }{
		"newest":   {sel + order, "idx_products_status_published"},
		"category": {sel + ` AND p.category = (SELECT id FROM categories WHERE slug = 'themes')` + order, "idx_products_status_category_published"},
		"author":   {sel + ` AND p.seller = (SELECT id FROM sellers WHERE slug = 'godudy')` + order, "idx_products_seller_status_published"},
	} {
		var plan []struct {
			Detail string `db:"detail"`
		}
		if err := app.DB().NewQuery("EXPLAIN QUERY PLAN " + c.sql).Bind(dbx.Params{}).All(&plan); err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, p := range plan {
			lines = append(lines, p.Detail)
		}
		got := strings.Join(lines, "\n")
		if !strings.HasPrefix(got, "SEARCH p USING INDEX "+c.index) || strings.Contains(got, "TEMP B-TREE") {
			t.Errorf("%s plan:\n%s\nwant products first via %s and no temp b-tree", name, got, c.index)
		}
	}
}

func TestListsSkipDescription(t *testing.T) {
	app := newApp(t)
	repo := &pb.Catalog{App: app}
	ps, err := repo.Products(context.Background(), "RUB", catalog.Filter{})
	if err != nil || len(ps) == 0 {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Description != "" {
			t.Fatalf("list returned a description for %s", p.Slug)
		}
	}
	p, err := repo.Product(context.Background(), "RUB", ps[0].Slug)
	if err != nil || p.Description == "" {
		t.Fatalf("product page lost its description: %v", err)
	}
}
