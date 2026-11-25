package pb_test

import (
	"context"
	"testing"

	"github.com/pocketbase/dbx"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/migrations"
	"github.com/fastygo/market/internal/platform/pb"
)

func TestSearchFTS5(t *testing.T) {
	app := newApp(t)
	repo := &pb.Catalog{App: app}
	ctx := context.Background()
	p, err := app.FindFirstRecordByData("products", "slug", "aurora-theme")
	if err != nil {
		t.Fatal(err)
	}
	p.Set("title", "\u0414\u0438\u0437\u0430\u0439\u043D \u0421\u0438\u0441\u0442\u0435\u043C\u0430 \u0401\u043B\u043A\u0430")
	if err := app.Save(p); err != nil { // the update trigger re-indexes it
		t.Fatal(err)
	}
	count := func(q string) int {
		t.Helper()
		got, err := repo.Products(ctx, "RUB", catalog.Filter{Query: q})
		if err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		return len(got)
	}
	for q, want := range map[string]int{
		"\u0441\u0438\u0441\u0442\u0435\u043C\u0430":  1, // Cyrillic, case-insensitive (LIKE was ASCII-only)
		"\u0421\u0418\u0421\u0422":                    1, // word prefix
		"\u0451\u043B\u043A\u0430":                    1, // yo (U+0451) matches yo; yo = ie (U+0435) is not folded (unicode61 remove_diacritics covers Latin only)
		"\u0437\u0430\u0439\u043D \u0441\u0438\u0441": 1, // substring across words (trigram)
		"\u0441\u0438\u0441\u0442\u0435\u043C\u0430 \u0434\u0438\u0437\u0430\u0439\u043D": 1, // all words, any order
		"\u0441\u0438\u0441\u0442\u0435\u043C\u0430 \u043F\u043B\u0430\u0433\u0438\u043D": 0,
		`"`:             0, // quotes are never FTS syntax
		`aurora" OR "x`: 0,
		"themes":        2, // category title column
		"godu":          6, // seller name prefix
	} {
		if got := count(q); got != want {
			t.Errorf("query %q: %d products, want %d", q, got, want)
		}
	}

	// renaming a seller re-indexes its products (sellers trigger)
	s, _ := app.FindFirstRecordByData("sellers", "slug", "godudy")
	s.Set("display_name", "\u041C\u0430\u0441\u0442\u0435\u0440\u0441\u043A\u0430\u044F \u041F\u0438\u043A\u0441\u0435\u043B\u0435\u0439")
	if err := app.Save(s); err != nil {
		t.Fatal(err)
	}
	if got := count("\u043F\u0438\u043A\u0441\u0435\u043B"); got != 6 {
		t.Errorf("after seller rename: %d", got)
	}

	// a broken index (rowids renumbered, triggers dropped) is repaired at start
	if _, err := app.DB().NewQuery("DROP TRIGGER products_fts_ai").Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB().NewQuery("UPDATE products_fts SET id = 'zzz' WHERE rowid = (SELECT MIN(rowid) FROM products_fts)").Execute(); err != nil {
		t.Fatal(err)
	}
	if err := migrations.EnsureProductSearch(app, false); err != nil {
		t.Fatal(err)
	}
	var trig, bad int
	_ = app.DB().NewQuery("SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'products_fts_ai'").Row(&trig)
	_ = app.DB().NewQuery("SELECT COUNT(*) FROM products_fts WHERE id = 'zzz'").Bind(dbx.Params{}).Row(&bad)
	if trig != 1 || bad != 0 || count("\u0441\u0438\u0441\u0442\u0435\u043C\u0430") != 1 {
		t.Fatalf("repair: trigger %d, bad rows %d", trig, bad)
	}
}
