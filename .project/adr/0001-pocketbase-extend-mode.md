# ADR-0001: PocketBase as a Go framework (extend mode), one binary

- Status: Accepted (direction confirmed by review 2026-11-24)

## Context

The owner chose PocketBase + goja + templ SSR. PocketBase runs either as the
prebuilt binary with `pb_hooks` only, or as a Go library we extend. We need Go
for payments, ledger, templ rendering and tests.

## Decision

- Build our own binary `cmd/market` with `pocketbase.New()` (extend mode).
- Register `plugins/jsvm` (goja `pb_hooks`) and `plugins/migratecmd` with Go
  migrations (`internal/migrations`), automigrate off in production.
- Pin PocketBase `v0.40.x` (latest `v0.40.6`), Go 1.27. Upgrade one minor at a
  time behind the test suite; read the changelog for breaking notes.
- PocketBase admin UI (`/_/`) is the operator back office in phases 1 and 2,
  read-mostly for money: money and fulfilment collections are written only by
  Go, and Go hooks block ledger and published-release edits (ADR-0005).
- SQLite WAL on local disk; Litestream for continuous backup.

## Consequences

- One deployable, one process, simple ops.
- PocketBase is pre-1.0 and its releases are moving off GitHub; we contain it
  behind `internal/platform` (ADR-0002) and vendor-check each upgrade.
- Vertical scaling only (one SQLite writer). Fine for a storefront; exit path
  in ADR-0002.

## Review 2026-11-24 (BalunMY)

Direction accepted. Admin UI is read-mostly for money collections (P0-7).

## Review r2 2026-11-24 (BalunMY): SQLite write discipline

- Every write transaction starts with `BEGIN IMMEDIATE` (no deferred lock
  upgrade deadlocks) through one `platform.Tx` helper.
- `busy_timeout` is set explicitly in the DSN by `platform/pb` (start at
  5000 ms, tuned by measurement); lock waits are a metric; callers retry a
  bounded number of times through the helper, never forever.
- No network call, goja call or heavy CPU work inside a write transaction
  (ADR-0007 rule zero).
- Heavy maintenance (full backup, `ANALYZE`, large imports) is scheduled
  away from checkout peaks; WAL checkpoint and Litestream lag are watched
  (`slo.md`).
- Business keys used for lookup and dedupe are real columns with composite
  unique indexes created in migrations, never fields inside json.
