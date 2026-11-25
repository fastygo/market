package pb_test

import (
	"fmt"
	"testing"

	"github.com/pocketbase/dbx"

	"github.com/fastygo/market/internal/platform/pb"
)

// After a bulk insert the planner stats still describe the seed catalog;
// OptimizeDB brings them up to date.
func TestOptimizeDBRefreshesStaleStats(t *testing.T) {
	app := newApp(t)
	db := app.DB()
	if _, err := db.NewQuery("ANALYZE").Execute(); err != nil {
		t.Fatal(err)
	}
	var seller string
	if err := db.NewQuery("SELECT id FROM sellers LIMIT 1").Row(&seller); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400; i++ {
		if _, err := db.NewQuery(`INSERT INTO products (id, seller, slug, title, status, product_type) VALUES ({:id}, {:s}, {:id}, 'x', 'published', 'plain')`).
			Bind(dbx.Params{"id": fmt.Sprintf("bulk%011d", i), "s": seller}).Execute(); err != nil {
			t.Fatal(err)
		}
	}
	rows := func() int {
		var stat string
		if err := db.NewQuery(`SELECT stat FROM sqlite_stat1 WHERE idx = 'idx_products_slug'`).Row(&stat); err != nil {
			t.Fatal(err)
		}
		var n int
		fmt.Sscan(stat, &n)
		return n
	}
	if n := rows(); n > 100 {
		t.Fatalf("stats already fresh before optimize: %d", n)
	}
	if err := pb.OptimizeDB(app); err != nil {
		t.Fatal(err)
	}
	if n := rows(); n < 400 {
		t.Fatalf("stats after optimize describe %d rows, want >= 400", n)
	}
}

// Readers keep the planner stats they loaded; OptimizeDB drops the idle
// ones so the next query opens a connection with the new stats.
func TestOptimizeDBRecyclesIdleReaders(t *testing.T) {
	app := newApp(t)
	r := app.ConcurrentDB().(*dbx.DB).DB()
	var n int
	if err := app.ConcurrentDB().NewQuery("SELECT COUNT(*) FROM products").Row(&n); err != nil {
		t.Fatal(err)
	}
	before := r.Stats()
	if before.Idle == 0 {
		t.Fatal("expected an idle reader")
	}
	if err := pb.OptimizeDB(app); err != nil {
		t.Fatal(err)
	}
	after := r.Stats()
	if after.Idle != 0 || after.MaxIdleClosed <= before.MaxIdleClosed || after.MaxOpenConnections != before.MaxOpenConnections {
		t.Fatalf("idle readers not recycled: before %+v after %+v", before, after)
	}
}
