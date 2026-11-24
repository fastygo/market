# ADR-0002: A thin in-process platform layer, not a second BaaS

- Status: Proposed

## Context

The owner asked: reuse fastygo, or build an intermediate `platform-baas` over
the PocketBase core. PocketBase already is a BaaS (auth, collections, files,
rules, admin). A second BaaS (fastygo/backend) would duplicate users, files
and storage.

## Decision

Create `internal/platform` ("platform-baas") as a library inside the binary,
not a service. It holds only what every domain package needs:

- ports: `Tx` (run in transaction), `Clock`, `IDs`, `Outbox`, `Audit`,
  repository interfaces declared by each domain package;
- `platform/pb`: PocketBase adapters for those ports (the only place besides
  `cmd/market` that imports PocketBase);
- `money` (int64 kopecks), `authz` (capabilities, modeled on
  `fastygo/backend internal/domain/authz`), `seller` scope helper, HMAC
  signer, idempotency helper.

Rules: no generic CRUD framework, no own schema engine, no own auth. A helper
enters `platform` only when a second domain package needs it (rule of two).

## Consequences

- Domain packages are pure Go and unit-testable without PocketBase.
- PocketBase upgrades touch `platform/pb` only.
- If SQLite ever limits us, the adapters can move to Postgres without rewriting
  the domain; PocketBase would then stay for admin/auth or be replaced.
