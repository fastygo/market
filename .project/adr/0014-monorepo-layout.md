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
internal/storefront/     storefront and operator config, LegalProfile consumer
internal/taxdocs/        tax document DTO (receipt, invoice, credit note), pure builder, status machine (imports no domain package)
internal/modules/        module registry, manifests, load order, config validation, goja plugin sandbox
internal/policy/         Go adapters TaxPolicy, DocumentIssuer, ConsentPolicy, LegalProfile that call plugins and validate
internal/marketplace/    marketplace module: onboarding, commission, seller_payable, payouts, seller portal
modules/region-ru/       plugin module: tax table, receipt mapping, consent docs, legal id rules, checkout fields
modules/region-eu/       plugin module stub: VAT/OSS, invoice mapping, GDPR docs, VAT id rules
modules/locale-ru/       locale module manifest (dictionary in DB unless owner allows it here, ADR-0018)
internal/payments/stripe/ | paypal/   EU adapters (later)
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
deploy/                  Caddyfile, systemd unit, litestream.yml, env.example (placeholders only)
.cursor/                 rules and skills copied from ui8kit/contract
.project/                plan, ADRs, SLO
```

- Import direction: `web -> ordering -> {payments, taxdocs, licensing,
  ledger}`, `payments -> taxdocs`; `taxdocs`, `licensing`, `ledger` import no
  domain package. Core never imports `marketplace`, a plugin or a payment adapter;
  `cmd/market` wires them from config. No cycles: `payments` never imports `ordering`.
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

## Revision 2026-11-24 (owner: region-neutral core)

`fiscal` -> `taxdocs`; added `storefront`, region pack slots, Stripe
and PayPal adapter slots; `scripts/verify.sh` exists with hygiene checks.

## Revision 2026-11-24 (owner: engine, ADR-0017, ADR-0018)

`internal/region/*` replaced by plugin modules under `modules/` and
`internal/policy` adapters; added `internal/modules` and
`internal/marketplace`.
