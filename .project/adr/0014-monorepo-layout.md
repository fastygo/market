# ADR-0014: Monorepo layout

- Status: Proposed

## Decision

```text
cmd/market/              main: PocketBase app, plugins, wiring
internal/platform/       ports, money, authz, signer, idempotency (platform-baas)
internal/platform/pb/    PocketBase adapters (only PocketBase importer besides main)
internal/migrations/     Go migrations (collections, indexes, seeds: GoDudY, license types)
internal/sellers/
internal/catalog/
internal/ordering/
internal/payments/       port, registry, state machine
internal/payments/yookassa/ | tbank/ | cloudpayments/ | robokassa/
internal/fiscal/         receipt builder (54-FZ fields)
internal/licensing/      entitlements, purchase codes, download tokens
internal/ledger/
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

- One Go module at the root. A `package.json` only for UI tooling (Tailwind,
  ui8kit CLI, ui8px), with the `ui8kit` zones field.
- No Cyrillic in code, comments, docs or fixtures.

## Consequences

Domain packages are flat and discoverable; the layout scales to the seller
portal (`internal/web/screens/seller`) without moves.
