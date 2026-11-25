package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Product search on FTS5 (replaces LIKE '%q%', which reads every row).
// Two FTS5 tables over the same columns, kept in sync by triggers:
//
//   - products_fts, tokenizer unicode61 remove_diacritics 2: whole words
//     and word prefixes, case-insensitive for Cyrillic too (but ё and е
//     stay different: remove_diacritics covers Latin letters only). There
//     is no Russian stemmer in FTS5 (porter is English only), so word
//     forms are matched by prefix, not by stem.
//   - products_fts_tri, tokenizer trigram: substrings of 3+ characters,
//     the same semantics LIKE '%q%' had, but from an index.
//
// Columns: title, excerpt, seller display name, category title; the
// product id is stored UNINDEXED and the storefront joins on it, so a
// rowid renumbered by VACUUM (PocketBase backups use VACUUM INTO) can
// never return a wrong product. EnsureProductSearch runs at every start:
// it re-creates missing tables or triggers and rebuilds the index when it
// no longer matches products (row count or rowid/id pairs).
func init() {
	m.Register(func(app core.App) error { return EnsureProductSearch(app, true) }, downProductSearch)
}

var searchTables = []string{"products_fts", "products_fts_tri"}

const searchColumns = "id UNINDEXED, title, excerpt, seller, category"

var searchTokenizers = map[string]string{
	"products_fts":     "unicode61 remove_diacritics 2",
	"products_fts_tri": "trigram remove_diacritics 1",
}

// searchRow is the indexed text of one product (p = the products row).
const searchRow = `p.rowid, p.id, p.title, p.excerpt,
	COALESCE((SELECT display_name FROM sellers WHERE id = p.seller), ''),
	COALESCE((SELECT title FROM categories WHERE id = p.category), '')`

func searchTriggers() []string {
	var ins, del, setSeller, setCategory string
	for _, t := range searchTables {
		ins += fmt.Sprintf(`INSERT INTO %s (rowid, id, title, excerpt, seller, category)
			VALUES (new.rowid, new.id, new.title, new.excerpt,
			COALESCE((SELECT display_name FROM sellers WHERE id = new.seller), ''),
			COALESCE((SELECT title FROM categories WHERE id = new.category), ''));
		`, t)
		del += fmt.Sprintf("DELETE FROM %s WHERE rowid = old.rowid;\n", t)
		setSeller += fmt.Sprintf("UPDATE %s SET seller = new.display_name WHERE rowid IN (SELECT rowid FROM products WHERE seller = new.id);\n", t)
		setCategory += fmt.Sprintf("UPDATE %s SET category = new.title WHERE rowid IN (SELECT rowid FROM products WHERE category = new.id);\n", t)
	}
	return []string{
		"CREATE TRIGGER IF NOT EXISTS products_fts_ai AFTER INSERT ON products BEGIN\n" + ins + "END",
		"CREATE TRIGGER IF NOT EXISTS products_fts_ad AFTER DELETE ON products BEGIN\n" + del + "END",
		"CREATE TRIGGER IF NOT EXISTS products_fts_au AFTER UPDATE OF title, excerpt, seller, category ON products BEGIN\n" + del + ins + "END",
		"CREATE TRIGGER IF NOT EXISTS sellers_fts_au AFTER UPDATE OF display_name ON sellers BEGIN\n" + setSeller + "END",
		"CREATE TRIGGER IF NOT EXISTS categories_fts_au AFTER UPDATE OF title ON categories BEGIN\n" + setCategory + "END",
	}
}

var searchTriggerNames = []string{"products_fts_ai", "products_fts_ad", "products_fts_au", "sellers_fts_au", "categories_fts_au"}

// EnsureProductSearch creates the FTS tables and triggers if missing and
// rebuilds the index when forced or when it does not match products.
// Idempotent; safe at every start (one pass over the index, milliseconds
// at 10k products).
func EnsureProductSearch(app core.App, rebuild bool) error {
	return app.RunInTransaction(func(tx core.App) error {
		db := tx.DB()
		for _, t := range searchTables {
			if _, err := db.NewQuery(fmt.Sprintf("CREATE VIRTUAL TABLE IF NOT EXISTS %s USING fts5(%s, tokenize = '%s')",
				t, searchColumns, searchTokenizers[t])).Execute(); err != nil {
				return fmt.Errorf("search: create %s: %w", t, err)
			}
		}
		for _, q := range searchTriggers() {
			if _, err := db.NewQuery(q).Execute(); err != nil {
				return fmt.Errorf("search: trigger: %w", err)
			}
		}
		if !rebuild {
			ok, err := searchInSync(tx)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
			tx.Logger().Warn("product search index out of sync with products: rebuilding")
		}
		for _, t := range searchTables {
			if _, err := db.NewQuery("DELETE FROM " + t).Execute(); err != nil {
				return err
			}
			if _, err := db.NewQuery(fmt.Sprintf("INSERT INTO %s (rowid, id, title, excerpt, seller, category) SELECT %s FROM products p", t, searchRow)).Execute(); err != nil {
				return fmt.Errorf("search: rebuild %s: %w", t, err)
			}
		}
		return nil
	})
}

func searchInSync(app core.App) (bool, error) {
	var products int
	if err := app.DB().NewQuery("SELECT COUNT(*) FROM products").Row(&products); err != nil {
		return false, err
	}
	for _, t := range searchTables {
		var n, bad int
		if err := app.DB().NewQuery("SELECT COUNT(*) FROM " + t).Row(&n); err != nil {
			return false, err
		}
		if err := app.DB().NewQuery(fmt.Sprintf(`SELECT COUNT(*) FROM %s f LEFT JOIN products p ON p.rowid = f.rowid
			WHERE p.id IS NULL OR p.id != f.id`, t)).Row(&bad); err != nil {
			return false, err
		}
		if n != products || bad != 0 {
			return false, nil
		}
	}
	return true, nil
}

func downProductSearch(app core.App) error {
	for _, n := range searchTriggerNames {
		if _, err := app.DB().NewQuery("DROP TRIGGER IF EXISTS " + n).Execute(); err != nil {
			return err
		}
	}
	for _, t := range searchTables {
		if _, err := app.DB().NewQuery("DROP TABLE IF EXISTS " + t).Execute(); err != nil {
			return err
		}
	}
	return nil
}
