package pb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/platform/tx"
)

// BusyTimeout is the explicit SQLite busy_timeout (ADR-0001, ADR-0007).
const BusyTimeout = 5 * time.Second

// DefaultMaxOpenConns caps the reader pool (PocketBase default: 120).
// SQLite memory lives outside the Go heap, so GOMEMLIMIT does not see it:
// every connection keeps its own page cache (CacheSizeKiB) and in-memory
// temp b-trees. With 120 connections a 16-request burst on a 10k catalog
// took RSS to 600 MB while the Go heap was 13 MB (load test 2026-11-25).
// On a 1-2 vCPU host more readers than this only queue on the CPU.
const DefaultMaxOpenConns = 8

// CacheSizeKiB is the SQLite page cache per connection; worst case is
// (DefaultMaxOpenConns + 1 writer) x 16 MiB = 144 MiB, and the OS page
// cache holds the database file anyway.
const CacheSizeKiB = 16000

// DBConnect opens the data databases with an explicit busy_timeout and
// `_txlock=immediate`, so every transaction starts with BEGIN IMMEDIATE and
// takes the write lock up front (no deferred-to-write upgrade that
// busy_timeout cannot retry). Other pragmas match PocketBase defaults
// except the per-connection cache size (CacheSizeKiB).
func DBConnect(dbPath string) (*dbx.DB, error) {
	pragmas := fmt.Sprintf("?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=journal_size_limit(200000000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-%d)&_defensive=1&_txlock=immediate", BusyTimeout.Milliseconds(), CacheSizeKiB)
	return dbx.Open("sqlite", dbPath+pragmas)
}

type txAppKey struct{}

// TxStats is called after every write transaction with its name, the time
// spent waiting for the write lock plus running, and the error.
var TxStats = func(name string, took time.Duration, attempts int, err error) {}

// TxLockWait is called once per attempt with the time from the attempt
// start to the transaction body: queueing for the single writer
// connection plus BEGIN IMMEDIATE waiting on busy_timeout.
var TxLockWait = func(name string, wait time.Duration) {}

// MaxTxAttempts bounds retries of a transaction that failed with SQLITE_BUSY.
const MaxTxAttempts = 3

// RunTx runs fn in one short write transaction (ADR-0007 rule zero): the
// context passed to fn is flagged as "in tx" so network adapters refuse to
// run, and the process-wide counter of open write transactions is kept.
// A nested call reuses the outer transaction. SQLITE_BUSY is retried a
// bounded number of times; any other error rolls back and is returned.
func RunTx(ctx context.Context, app core.App, name string, fn func(ctx context.Context, txApp core.App) error) error {
	if outer, ok := ctx.Value(txAppKey{}).(core.App); ok && tx.InTx(ctx) {
		return fn(ctx, outer)
	}
	start := time.Now()
	var err error
	attempt := 0
	for attempt = 1; attempt <= MaxTxAttempts; attempt++ {
		attemptStart := time.Now()
		err = app.RunInTransaction(func(txApp core.App) error {
			TxLockWait(name, time.Since(attemptStart))
			tx.Enter()
			defer tx.Leave()
			inner := context.WithValue(tx.Mark(ctx), txAppKey{}, txApp)
			return fn(inner, txApp)
		})
		if err == nil || !isBusy(err) || ctx.Err() != nil {
			break
		}
		time.Sleep(time.Duration(attempt) * 25 * time.Millisecond)
	}
	TxStats(name, time.Since(start), attempt, err)
	return err
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "SQLITE_BUSY") || strings.Contains(s, "database is locked")
}

// ErrConflict is returned by conditional updates that matched no row (a
// lost race or a stale status_version); callers treat it as a no-op.
var ErrConflict = errors.New("pb: conditional update matched no row")

// OptimizeDB refreshes SQLite planner stats where they are stale
// (`PRAGMA optimize=0x10002`, bounded by analysis_limit) on the writer
// connection. PocketBase does it nightly and after schema changes; a bulk
// import between those leaves sqlite_stat1 describing a 6-row catalog and
// the planner picks full scans (load test 2026-11-25), so the binary also
// runs it at start and hourly. Tables whose size did not change much are
// skipped, so a run is cheap.
//
// New stats reach only connections that load the schema afterwards: an
// open reader keeps the plans it had (a 10k catalog page took 250 ms
// instead of 10 ms on a reader opened before the optimize). So the reader
// pool's idle connections are dropped after the optimize, and
// ReaderMaxLifetime recycles busy ones.
func OptimizeDB(app core.App) error {
	db := app.NonconcurrentDB()
	if _, err := db.NewQuery("PRAGMA analysis_limit=1000").Execute(); err != nil {
		return err
	}
	if _, err := db.NewQuery("PRAGMA optimize=0x10002").Execute(); err != nil {
		return err
	}
	if r, ok := app.ConcurrentDB().(*dbx.DB); ok {
		n := r.DB().Stats().MaxOpenConnections
		r.DB().SetMaxIdleConns(0) // closes idle readers; they reopen with fresh stats
		r.DB().SetMaxIdleConns(n)
	}
	return nil
}

// ReaderMaxLifetime bounds how long a reader connection keeps the planner
// stats and schema it loaded (see OptimizeDB).
const ReaderMaxLifetime = 15 * time.Minute
