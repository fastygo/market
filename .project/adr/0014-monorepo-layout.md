# ADR-0014: Monorepo layout

- Status: Proposed (revised after review 2026-11-24)

## Decision

```text
cmd/market/              main: PocketBase app, plugins, wiring
internal/platform/       Tx, Clock, IDs, Outbox, Audit, money, authz, signer (platform-baas; no repositories)
internal/platform/pb/    PocketBase adapters, repositories, write-protection hooks (only PocketBase importer besides main)
internal/migrations/     Go migrations (collections, indexes, seeds: GoDudY, license types)
internal/sellers/
internal/catalog/
internal/ordering/       order aggregate, transitions; money-flow use cases: checkout, return, inbox apply, reconciliation, refund
internal/payments/       Gateway port, registry, payment transitions, webhook verify + inbox insert
internal/payments/yookassa/ | tbank/ | cloudpayments/ | robokassa/
internal/fiscal/         receipt DTO, pure builder, receipt status machine (imports no domain package)
internal/licensing/      entitlements, purchase codes, download tokens
internal/ledger/         entries, deterministic txn ids, posting rules
internal/trust/          reviews (phase 2)
internal/web/            router, middleware (CSRF, metrics), handlers
internal/web/viewmodel/  records -> Codex records + view structs
internal/web/screens/    app layer templ pages
ui/kit/                  UI8Kit templ primitives (CLI installed, do not edit)
ui/blocks/               prototypes (8+4)
ui/layouts/              layouts (8+4)
ui/dict/en.json          dictionary
ui/styles/               tokens, Tailwind entry
pb_hooks/                goja hooks (ADR-0011)
scripts/verify.sh        local gate
deploy/                  Caddyfile, systemd unit, litestream.yml
.cursor/                 rules and skills copied from ui8kit/contract
.project/                plan, ADRs, SLO
```

- Import direction: `web -> ordering -> {payments, fiscal, licensing,
  ledger}`, `payments -> fiscal`; `fiscal`, `licensing`, `ledger` import no
  domain package. No cycles: `payments` never imports `ordering`.
  Each package declares the interfaces it consumes.
- One Go module at the root. A `package.json` only for UI tooling (Tailwind,
  ui8kit CLI, ui8px), with the `ui8kit` zones field.
- No Cyrillic in code, comments, docs or fixtures.

## Consequences

Domain packages are flat and discoverable; the layout scales to the seller
portal (`internal/web/screens/seller`) without moves.

## Review 2026-11-24 (BalunMY)

Added `internal/trust`; checkout use case in `ordering`; receipt DTO in
`fiscal`; no repositories in `platform`; import direction stated.
