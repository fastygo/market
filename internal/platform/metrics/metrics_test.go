package metrics_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/platform/metrics"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/pb/pbtest"
)

func TestCheckLoopback(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:9091": true, "[::1]:9091": true, "localhost:9091": true,
		"0.0.0.0:9091": false, ":9091": false, "10.0.0.5:9091": false, "example.com:9091": false, "9091": false,
	} {
		if err := metrics.CheckLoopback(addr); (err == nil) != ok {
			t.Errorf("CheckLoopback(%q) err=%v, want ok=%v", addr, err, ok)
		}
	}
	if _, err := metrics.New().Listen("0.0.0.0:0"); err == nil {
		t.Fatal("Listen on a public address must fail")
	}
}

func TestDBAndTxMetrics(t *testing.T) {
	app := pbtest.New(t)
	m := metrics.New()
	m.InstrumentDB(map[string]*dbx.DB{
		"concurrent":    app.ConcurrentDB().(*dbx.DB),
		"nonconcurrent": app.NonconcurrentDB().(*dbx.DB),
	})
	r, done := m.Start(httptest.NewRequest("GET", "/", nil))
	var n int
	for i := 0; i < 3; i++ {
		if err := app.DB().NewQuery("SELECT count(*) FROM products").WithContext(r.Context()).Row(&n); err != nil {
			t.Fatal(err)
		}
	}
	if got := metrics.QueryCount(r.Context()); got != 3 {
		t.Fatalf("QueryCount = %d, want 3", got)
	}
	done("GET /{$}", 200)
	m.Render("GET /{$}", time.Millisecond)

	old, oldW := pb.TxStats, pb.TxLockWait
	pb.TxStats, pb.TxLockWait = m.Tx, m.TxLockWait
	t.Cleanup(func() { pb.TxStats, pb.TxLockWait = old, oldW })
	_ = pb.RunTx(context.Background(), app, "test_ok", func(ctx context.Context, tx core.App) error { return nil })
	_ = pb.RunTx(context.Background(), app, "test_fail", func(ctx context.Context, tx core.App) error { return errors.New("x") })

	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	res, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	for _, want := range []string{
		`market_http_request_duration_seconds_count{code="2xx",route="GET /{$}"} 1`,
		`market_http_request_db_queries_sum{route="GET /{$}"} 3`,
		`market_ssr_render_seconds_count{route="GET /{$}"} 1`,
		`market_db_statement_seconds_count{kind="query",pool="concurrent"}`,
		`market_tx_duration_seconds_count{name="test_ok"} 1`,
		`market_tx_lock_wait_seconds_count{name="test_ok"} 1`,
		`market_tx_errors_total{name="test_fail",reason="other"} 1`,
		`go_sql_wait_duration_seconds_total{db_name="nonconcurrent"}`,
		`go_sched_goroutines_goroutines`,
		`go_gc_gomemlimit_bytes`,
		`process_resident_memory_bytes`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics output lacks %s", want)
		}
	}
	res, err = http.Get(srv.URL + "/debug/pprof/goroutine?debug=1")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("pprof: %v %v", err, res)
	}
	res.Body.Close()
}
