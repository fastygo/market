# ADR-0001: PocketBase as a Go framework (extend mode), one binary

- Status: Proposed

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
- PocketBase admin UI (`/_/`) is the operator back office in phases 1 and 2.
- SQLite WAL on local disk; Litestream for continuous backup.

## Consequences

- One deployable, one process, simple ops.
- PocketBase is pre-1.0 and its releases are moving off GitHub; we contain it
  behind `internal/platform` (ADR-0002) and vendor-check each upgrade.
- Vertical scaling only (one SQLite writer). Fine for a storefront; exit path
  in ADR-0002.
