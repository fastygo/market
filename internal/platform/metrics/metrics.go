// Package metrics is the Prometheus side of the SLOs (slo.md "How
// measured"): HTTP duration per route, templ render time and SQLite query
// time measured separately, queries per request, write transaction
// duration, write lock wait and SQLITE_BUSY, Go runtime and process
// metrics. The endpoint (and pprof) is served on a loopback-only listener,
// never on the public port.
package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Buckets: SSR pages are expected in single milliseconds, SQLite queries
// in tens of microseconds; the SLO thresholds (300 ms / 800 ms) sit on
// bucket boundaries.
var (
	httpBuckets = []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .3, .5, .8, 1.5, 3}
	dbBuckets   = []float64{.00005, .0001, .00025, .0005, .001, .0025, .005, .01, .025, .05, .1, .25, 1}
	txBuckets   = []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .2, .5, 1, 2.5, 5}
	qBuckets    = []float64{1, 2, 3, 4, 5, 7, 10, 15, 20, 30, 50, 100}
)

// Metrics holds the collectors; the zero value is not usable, use New.
type Metrics struct {
	Registry *prometheus.Registry

	httpDur    *prometheus.HistogramVec
	renderDur  *prometheus.HistogramVec
	reqQueries *prometheus.HistogramVec
	reqDBTime  *prometheus.HistogramVec
	dbDur      *prometheus.HistogramVec
	dbErrors   *prometheus.CounterVec
	txDur      *prometheus.HistogramVec
	txWait     *prometheus.HistogramVec
	txRetries  *prometheus.CounterVec
	txErrors   *prometheus.CounterVec

	dbOnce sync.Once
}

// New registers the market collectors plus Go runtime (GC, memory,
// scheduler incl. goroutines and GOMEMLIMIT) and process (RSS) metrics.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		Registry: reg,
		httpDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "market_http_request_duration_seconds", Help: "Storefront request duration (server time) by route pattern and status class.",
			Buckets: httpBuckets}, []string{"route", "code"}),
		renderDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "market_ssr_render_seconds", Help: "templ render time to buffer, without DB time.",
			Buckets: httpBuckets}, []string{"route"}),
		reqQueries: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "market_http_request_db_queries", Help: "SQLite statements per storefront request (N+1 detector).",
			Buckets: qBuckets}, []string{"route"}),
		reqDBTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "market_http_request_db_seconds", Help: "Sum of SQLite statement time per storefront request.",
			Buckets: httpBuckets}, []string{"route"}),
		dbDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "market_db_statement_seconds", Help: "SQLite statement time (query: until rows are ready; exec: until done).",
			Buckets: dbBuckets}, []string{"pool", "kind"}),
		dbErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_db_statement_errors_total", Help: "SQLite statement errors; reason=busy is SQLITE_BUSY / database is locked."},
			[]string{"pool", "kind", "reason"}),
		txDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "market_tx_duration_seconds", Help: "Write transaction time incl. lock wait and BUSY retries (pb.RunTx).",
			Buckets: txBuckets}, []string{"name"}),
		txWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "market_tx_lock_wait_seconds", Help: "Time to get the SQLite write lock (BEGIN IMMEDIATE) per attempt.",
			Buckets: txBuckets}, []string{"name"}),
		txRetries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_tx_busy_retries_total", Help: "Write transaction attempts repeated after SQLITE_BUSY."}, []string{"name"}),
		txErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_tx_errors_total", Help: "Failed write transactions; reason=busy means SQLITE_BUSY reached the caller."},
			[]string{"name", "reason"}),
	}
	reg.MustRegister(m.httpDur, m.renderDur, m.reqQueries, m.reqDBTime, m.dbDur, m.dbErrors,
		m.txDur, m.txWait, m.txRetries, m.txErrors,
		collectors.NewGoCollector(collectors.WithGoCollectorRuntimeMetrics(
			collectors.MetricsGC, collectors.MetricsMemory, collectors.MetricsScheduler)),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// request-scoped statement counter (set by Start, filled by the DB hooks)
type reqStats struct {
	n  atomic.Int64
	ns atomic.Int64
}

type reqKey struct{}

// Start implements web.Observer: it tags the request context so DB
// statements run with it are counted; done records the request.
func (m *Metrics) Start(r *http.Request) (*http.Request, func(route string, status int)) {
	st := &reqStats{}
	start := time.Now()
	r = r.WithContext(context.WithValue(r.Context(), reqKey{}, st))
	return r, func(route string, status int) {
		if route == "" {
			route = "unmatched"
		}
		m.httpDur.WithLabelValues(route, strconv.Itoa(status/100)+"xx").Observe(time.Since(start).Seconds())
		m.reqQueries.WithLabelValues(route).Observe(float64(st.n.Load()))
		m.reqDBTime.WithLabelValues(route).Observe(time.Duration(st.ns.Load()).Seconds())
	}
}

// Render implements web.Observer.
func (m *Metrics) Render(route string, d time.Duration) {
	m.renderDur.WithLabelValues(route).Observe(d.Seconds())
}

// QueryCount returns the statements counted for ctx so far (tests).
func QueryCount(ctx context.Context) int {
	if st, ok := ctx.Value(reqKey{}).(*reqStats); ok {
		return int(st.n.Load())
	}
	return 0
}

func (m *Metrics) stmt(ctx context.Context, pool, kind string, d time.Duration, err error) {
	m.dbDur.WithLabelValues(pool, kind).Observe(d.Seconds())
	if err != nil && err != sql.ErrNoRows {
		reason := "other"
		if IsBusy(err) {
			reason = "busy"
		}
		m.dbErrors.WithLabelValues(pool, kind, reason).Inc()
	}
	if ctx == nil {
		return
	}
	if st, ok := ctx.Value(reqKey{}).(*reqStats); ok {
		st.n.Add(1)
		st.ns.Add(int64(d))
	}
}

// InstrumentDB hooks the statement timers into PocketBase's two pools
// ("concurrent" readers, "nonconcurrent" = the single writer connection)
// and exports database/sql pool stats: for the writer pool,
// go_sql_wait_duration_seconds_total is the time spent queueing for the
// one write connection. Existing log funcs (dev mode) are kept.
func (m *Metrics) InstrumentDB(pools map[string]*dbx.DB) {
	for name, db := range pools {
		if db == nil {
			continue
		}
		name := name
		prevQ, prevE := db.QueryLogFunc, db.ExecLogFunc
		db.QueryLogFunc = func(ctx context.Context, t time.Duration, s string, rows *sql.Rows, err error) {
			m.stmt(ctx, name, "query", t, err)
			if prevQ != nil {
				prevQ(ctx, t, s, rows, err)
			}
		}
		db.ExecLogFunc = func(ctx context.Context, t time.Duration, s string, res sql.Result, err error) {
			m.stmt(ctx, name, "exec", t, err)
			if prevE != nil {
				prevE(ctx, t, s, res, err)
			}
		}
	}
	m.dbOnce.Do(func() {
		for name, db := range pools {
			if db != nil {
				m.Registry.MustRegister(collectors.NewDBStatsCollector(db.DB(), name))
			}
		}
	})
}

// Tx matches pb.TxStats.
func (m *Metrics) Tx(name string, took time.Duration, attempts int, err error) {
	m.txDur.WithLabelValues(name).Observe(took.Seconds())
	if attempts > 1 {
		m.txRetries.WithLabelValues(name).Add(float64(attempts - 1))
	}
	if err != nil {
		reason := "other"
		if IsBusy(err) {
			reason = "busy"
		}
		m.txErrors.WithLabelValues(name, reason).Inc()
	}
}

// TxLockWait matches pb.TxLockWait.
func (m *Metrics) TxLockWait(name string, d time.Duration) {
	m.txWait.WithLabelValues(name).Observe(d.Seconds())
}

// IsBusy reports SQLITE_BUSY / SQLITE_LOCKED style errors.
func IsBusy(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "SQLITE_BUSY") || strings.Contains(s, "database is locked") || strings.Contains(s, "SQLITE_LOCKED")
}

// Handler serves /metrics and /debug/pprof/ (for the loopback listener only).
func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

// CheckLoopback rejects listen addresses that are not on a loopback
// interface: metrics and pprof must never be public (use an SSH tunnel).
func CheckLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("metrics: %q is not a loopback address (use 127.0.0.1:PORT)", addr)
	}
	return nil
}

// Listen starts the loopback-only metrics and pprof server; Shutdown it
// on terminate.
func (m *Metrics) Listen(addr string) (*http.Server, error) {
	if err := CheckLoopback(addr); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: m.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute}
	go func() { _ = srv.Serve(ln) }()
	return srv, nil
}
