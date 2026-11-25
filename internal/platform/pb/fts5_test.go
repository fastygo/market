package pb_test

import (
	"testing"

	"github.com/pocketbase/dbx"

	"github.com/fastygo/market/internal/platform/pb/pbtest"
)

// TestFTS5Available pins the fact that the SQLite build PocketBase uses
// (modernc.org/sqlite) has FTS5 with the unicode61 and trigram tokenizers:
// unicode61 for whole words (Cyrillic folded to lower case, no stemmer),
// trigram for substrings (SQLite >= 3.34).
func TestFTS5Available(t *testing.T) {
	app := pbtest.New(t)
	db := app.DB()
	var ver string
	if err := db.NewQuery("SELECT sqlite_version()").Row(&ver); err != nil {
		t.Fatal(err)
	}
	t.Logf("sqlite %s", ver)
	for _, q := range []string{
		`CREATE VIRTUAL TABLE t_words USING fts5(title, tokenize = "unicode61 remove_diacritics 2")`,
		`CREATE VIRTUAL TABLE t_tri USING fts5(title, tokenize = "trigram")`,
		"INSERT INTO t_words(title) VALUES ('\u0414\u0438\u0437\u0430\u0439\u043D \u0421\u0438\u0441\u0442\u0435\u043C\u0430 UI8Kit')",
		"INSERT INTO t_tri(title) VALUES ('\u0414\u0438\u0437\u0430\u0439\u043D \u0421\u0438\u0441\u0442\u0435\u043C\u0430 UI8Kit')",
	} {
		if _, err := db.NewQuery(q).Execute(); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, c := range []struct{ table, match string }{
		{"t_words", "\u0441\u0438\u0441\u0442\u0435\u043C\u0430"}, // case folded Cyrillic word
		{"t_words", "\u0434\u0438\u0437\u0430*"},                  // prefix
		{"t_tri", "\u0438\u0441\u0442\u0435\u043C"},               // substring (>= 3 chars)
		{"t_tri", "i8k"},
	} {
		var n int
		if err := db.NewQuery("SELECT count(*) FROM " + c.table + " WHERE " + c.table + " MATCH {:m}").
			Bind(dbx.Params{"m": c.match}).Row(&n); err != nil {
			t.Fatalf("%s MATCH %q: %v", c.table, c.match, err)
		}
		if n != 1 {
			t.Errorf("%s MATCH %q = %d rows, want 1", c.table, c.match, n)
		}
	}
}
