# .project

Operator notes for `fastygo/market`: a white-label commerce engine for digital
goods (single store or marketplace). First storefront: StorEEE Market
(brand config); GoDudY is one of its sellers.
First deployment: Russia (region-ru module). Docs are English; no Cyrillic in
the repo.

## Documents

- [Intent](intent.md): what we build, for whom, and what we do not build
- [Plan](plan.md): phases, small steps, result first, then scale
- [Architecture](architecture.md): layers, packages, domain model, state machines
- [SLO](slo.md): objectives, SLIs, error budgets, how measured
- [Open questions](open-questions.md): owner and legal decisions still needed
- [Research notes](research.md): what was read in fastygo and ui8kit, with commits
- [Medusa reference](reference/medusa/README.md) and [Medusa mapping](reference/medusa-mapping.md): commerce structures from Medusa v2.23.0 (MIT) mapped to our collections, with proposed model changes

## ADRs (one decision each)

| ADR | Decision | Status |
| --- | --- | --- |
| [0001](adr/0001-pocketbase-extend-mode.md) | PocketBase as a Go framework (extend mode), one binary | Accepted |
| [0002](adr/0002-platform-baas-layer.md) | A thin in-process `platform` layer over PocketBase, not a second BaaS | Accepted |
| [0003](adr/0003-fastygo-reuse.md) | fastygo reuse: Codex yes (projection, canonical, codes), backend no, FormSet later | Accepted |
| [0004](adr/0004-domain-model.md) | Domain model and data structures (region-neutral) | Proposed |
| [0005](adr/0005-multi-vendor-readiness.md) | Seller-scoped rows, one database, Go-only money writes | Proposed |
| [0006](adr/0006-money-and-ledger.md) | Minor units + ISO 4217, tax as data, deterministic append-only ledger | Proposed |
| [0007](adr/0007-payment-gateway-port.md) | Provider-neutral gateway port, checkout protocol, inbox/outbox, reconciliation | Proposed |
| [0008](adr/0008-region-pack-ru.md) | RU region pack: 54-FZ, VAT, 152-FZ, INN rules, 289-FZ | Proposed |
| [0009](adr/0009-licensing-and-downloads.md) | Entitlements, purchase codes, re-checked signed downloads | Proposed |
| [0010](adr/0010-ssr-templ-ui8kit.md) | SSR with templ on UI8Kit templ primitives, Codex view models | Proposed |
| [0011](adr/0011-goja-hooks-scope.md) | goja: hooks glue only, policy plugins sandboxed, money paths in Go | Accepted |
| [0012](adr/0012-design-principles.md) | KISS and YAGNI first; DRY for rules; SOLID as Go interfaces; no OOP hierarchies | Accepted |
| [0013](adr/0013-testing-strategy.md) | TDD, crash and race tests, plugin contract tests, region and mode conformance | Proposed |
| [0014](adr/0014-monorepo-layout.md) | Monorepo layout | Proposed |
| [0015](adr/0015-region-packs-and-multi-currency.md) | Region packs behind consumer-side interfaces; multi-currency | Proposed |
| [0016](adr/0016-no-secrets-or-personal-data-in-repo.md) | No secrets, legal-entity or personal data in the repo; verify.sh greps | Accepted |
| [0017](adr/0017-module-system.md) | Module system (Go modules for money, goja plugins for policy); commerce mode | Proposed |
| [0018](adr/0018-i18n-and-white-label.md) | Locale modules and white-label branding | Proposed |
| [0019](adr/0019-product-types-as-modules.md) | Product types as modules; plain product page by default | Accepted |
| [0020](adr/0020-vendor-model.md) | Vendor model (Envato style): seller lifecycle, profiles, portfolio, discovery; brand as config | Proposed |
| [0021](adr/0021-seller-earnings-and-payouts.md) | Commission, seller earnings ledger, hold period, refunds and disputes, payouts | Proposed |
| [0022](adr/0022-moderation-releases-and-licensing.md) | Item submission, moderation queue, releases, licensing | Proposed |
| [0023](adr/0023-referral-attribution-seam.md) | Referral attribution and affiliate commissions (seam only) | Proposed |
| [0024](adr/0024-digital-refund-and-anti-abuse-policy.md) | Digital goods refund and anti-abuse policy behind a region `RefundPolicy`; evidence logs, watermark seam | Proposed |
| [0025](adr/0025-yookassa-adapter.md) | YooKassa adapter: redirect one-stage payments, 54-FZ receipts, verified notifications, refunds | Accepted |
| [0026](adr/0026-mail-transport.md) | Transactional mail over HTTP APIs (Unisender Go for RU), outbox retries, file mailer for dev | Accepted |
| [0027](adr/0027-buyer-cabinet.md) | Buyer cabinet: magic link and password sign-in, cookie session, CSRF, SSR checkout | Accepted |

ADR format: Status, Context, Decision, Consequences. Status values: Proposed,
Accepted, Superseded. "Accepted" means the direction was confirmed by the
architecture review or set by the owner.

## Review 2026-11-24 (BalunMY)

Architecture review of `29cd771` (verdict: direction sound, money paths had
gaps). Changes:

- P0: checkout protocol with required idempotency key (ADR-0007); cancel only
  after the provider confirms, late success revives the order,
  `CancelPayment`; ledger and entitlement in the same transaction as the
  transition, outbox only for external effects, deterministic `txn_id`,
  leases, one inbox and one outbox worker; wider reconciliation on the same
  `Transition`; `fiscal_receipts` single receipt SoT with
  `RegisterReceipt`/`FetchReceipt` and page alerts; money invariants
  (VAT-inclusive, totals, zero discount in phase 1, remainders); Go-only
  writes to money collections; GoDudY sale model left open with two branches.
- P1: disputes, full refunds only, no guest checkout, return URL, fuller item
  snapshot, download re-check, uniques and `status_version`, storage-level
  immutability, `vat_payable`, consistent SLOs, crash and race tests, seller
  on every money row, commission rounding, payout uniqueness, tax regime vs
  VAT invariant.
- P2: `trust` package, no `waiting_for_capture`, `legal_offer_version`,
  storefront order number, all-yanked rule, counters as cache, shutdown,
  indexes, global slug.
- Layout: checkout and money-flow use cases in `internal/ordering`, receipt
  DTO in `internal/fiscal`, no repositories in `platform`, goja kept strict.

## Revision 2026-11-24 (owner: engine, region-neutral, modular)

Owner direction (three steps): reusable for other markets (for example the
EU with Stripe or PayPal), no hardcoded region or personal data; a
Medusa-like white-label commerce engine with a reference storefront; single
store and marketplace as a mode. Changes:

- Region-neutral core: money is minor units + ISO 4217 per order; tax is
  line data resolved by a region `TaxPolicy`; fiscal receipts became region
  tax documents (`taxdocs`, `tax_documents`); operator and storefront are
  data; seller legal ids are json validated per region.
- Region packs (ADR-0015) and the RU pack (ADR-0008, renamed from
  `0008-rf-compliance.md`); EU pack described as a stub with Stripe and
  PayPal adapters; all review P0 guarantees stay provider-neutral (ADR-0007).
- Module system (ADR-0017): manifests, versioned capabilities, load order,
  config; Go modules for money-critical ports, sandboxed goja plugins for
  policy as data; commerce mode `single_store` / `marketplace`.
- Locale modules and white label (ADR-0018).
- No secrets or real legal/personal data in the repo (ADR-0016) enforced by
  `scripts/verify.sh`.
- Blockers removed from ADR-0004/0006/0008: the GoDudY sale model and legal
  data are go-live configuration; `open-questions.md` reclassified.

## Review r2 2026-11-24 (BalunMY): data structures agreed

- Medusa proposals decided item by item (`reference/medusa-mapping.md`
  decision table): 1, 3 rejected; 2, 5, 11 deferred; 6, 8 rejected until a
  consumer; 4, 7, 9, 10 trimmed. No Medusa-style tables in phase 1.
- P0-A: no network or goja inside a write transaction; claim, provider call
  and apply are separate transactions; `BEGIN IMMEDIATE`, explicit
  `busy_timeout` (ADR-0001, ADR-0007).
- P0-B: `ledger_entries` + `Transition` are the single money SoT; order and
  payment amounts are same-tx snapshots (ADR-0004, ADR-0006).
- P0-C: phase 0-1 region policy is the Go module `internal/region/ru` behind
  the same interfaces; the goja policy plugin runtime stays the planned
  later phase, triggered by a second region, policy changes without a
  release, or the owner (ADR-0017; owner decision A3).
- P1: Stripe/PayPal as a phase 4 note in ADR-0007; `refunds.provider`,
  `reason_code`, `note`; auto-refund of paid-but-uncompletable orders;
  `agent` sellers rejected in `single_store`; ledger indexes and on-demand
  balances; `storefronts.countries`; line `product_slug`, `license_title`.

## Decisions 2026-11-24 (owner t223u)

A3 decided (Go region rules in phase 1, goja plugins at their trigger); A1
stays default. ADR-0019 product types as modules. Target host profile,
memory budget and load-test plan in `slo.md`; release files via pre-signed
S3 redirects (ADR-0009).

## Direction 2026-11-24 (owner t229u, t230u)

StorEEE Market is the storefront brand (config, dictionary, skin); GoDudY
is a seller. Envato-style vendor model in ADR-0020 to ADR-0022, referral
seam for a later affiliate partner site in ADR-0023; owner questions in
`open-questions.md` E, F, G; phase 3 rewritten in `plan.md`.

## Decision 2026-11-24 (owner t234u)

ADR-0024: digital goods refund and anti-abuse policy (region
`RefundPolicy`, RU defaults marked needs counsel), evidence logs for
chargebacks, watermark seam; open questions C8 to C13.

## Decision 2026-11-24 (owner t235u, t236u)

ADR-0025 YooKassa adapter (live keys pending; fake gateway emulates the
flow), ADR-0026 mail transport (Unisender Go recommended), ADR-0027 buyer
cabinet and SSR checkout.
