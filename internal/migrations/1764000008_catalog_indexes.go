package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Catalog list indexes (load test 2026-11-25, EXPLAIN in
// .project/loadtest/2026-11-25.md). Each one serves one storefront query:
// equality columns first, then the sort columns (published_at, created),
// so SQLite reads rows in order and stops at LIMIT instead of sorting the
// whole catalog in a temp b-tree.
//
//	home, catalog, search      WHERE status = ?                ORDER BY published_at DESC, created DESC
//	category                   WHERE status = ? AND category = ? ...
//	author, "more", item count WHERE seller = ? AND status = ?   ...
//
// The two old indexes are prefixes of the new ones and are replaced, so
// the writer pays for one extra index, not three. idx_products_seller's
// role (seller lookups) is kept by the (seller, ...) prefix.
func init() {
	m.Register(upCatalogIndexes, downCatalogIndexes)
}

func upCatalogIndexes(app core.App) error {
	col, err := app.FindCollectionByNameOrId("products")
	if err != nil {
		return err
	}
	col.RemoveIndex("idx_products_status_category")
	col.RemoveIndex("idx_products_seller")
	col.AddIndex("idx_products_status_published", false, "status, published_at, created", "")
	col.AddIndex("idx_products_status_category_published", false, "status, category, published_at, created", "")
	col.AddIndex("idx_products_seller_status_published", false, "seller, status, published_at, created", "")
	return app.Save(col)
}

func downCatalogIndexes(app core.App) error {
	col, err := app.FindCollectionByNameOrId("products")
	if err != nil {
		return err
	}
	col.RemoveIndex("idx_products_status_published")
	col.RemoveIndex("idx_products_status_category_published")
	col.RemoveIndex("idx_products_seller_status_published")
	col.AddIndex("idx_products_status_category", false, "status, category", "")
	col.AddIndex("idx_products_seller", false, "seller", "")
	return app.Save(col)
}
