# ADR-0002: A thin in-process platform layer, not a second BaaS

- Status: Accepted (direction confirmed by review 2026-11-24)

## Context

The owner asked: reuse fastygo, or build an intermediate `platform-baas` over
the PocketBase core. PocketBase already is a BaaS (auth, collections, files,
rules, admin). A second BaaS (fastygo/backend) would duplicate users, files
and storage.

## Decision

Create `internal/platform` ("platform-baas") as a library inside the binary,
not a service. It holds only what every domain package needs:

- shared ports with two or more consumers: `Tx` (run in transaction),
  `Clock`, `IDs`, `Outbox`, `Audit`. Repository interfaces are NOT here:
  each domain package declares the small interfaces it consumes;
- `platform/pb`: PocketBase adapters for those ports (the only place besides
  `cmd/market` that imports PocketBase);
- `money` (int64 minor units + ISO 4217 currency), `authz` (capabilities, modeled on
  `fastygo/backend internal/domain/authz`), `seller` scope helper, HMAC
  signer, idempotency helper.

Rules: no generic CRUD framework, no own schema engine, no own auth. A helper
enters `platform` only when a second domain package needs it (rule of two).

Use cases live in their domain package, not in `platform` and not in a new
application layer: checkout is a function in `internal/ordering` that takes
`payments.Gateway`, order and payment repositories and `Clock`; `internal/web`
only parses HTTP and calls it. `ctx` goes to every repository, worker and use
case.

## Consequences

- Domain packages are pure Go and unit-testable without PocketBase.
- PocketBase upgrades touch `platform/pb` only.
- If SQLite ever limits us, the adapters can move to Postgres without rewriting
  the domain; PocketBase would then stay for admin/auth or be replaced.

## Review 2026-11-24 (BalunMY)

Direction accepted. Repositories stay out of `platform`; checkout orchestration lives in `internal/ordering`; `ctx` everywhere.
