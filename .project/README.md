# .project

Operator notes for `fastygo/market`: the digital goods storefront of IT Garage LLC
(first seller GoDudY, multi-vendor ready). Docs are English; no Cyrillic in the repo.

## Documents

- [Intent](intent.md): what we build, for whom, and what we do not build
- [Plan](plan.md): phases, small steps, result first, then scale
- [Architecture](architecture.md): layers, packages, domain model, state machines
- [SLO](slo.md): objectives, SLIs, error budgets, how measured
- [Open questions](open-questions.md): owner and legal decisions still needed
- [Research notes](research.md): what was read in fastygo and ui8kit, with commits

## ADRs (one decision each)

| ADR | Decision | Status |
| --- | --- | --- |
| [0001](adr/0001-pocketbase-extend-mode.md) | PocketBase as a Go framework (extend mode), one binary | Accepted |
| [0002](adr/0002-platform-baas-layer.md) | A thin in-process `platform` layer over PocketBase, not a second BaaS | Accepted |
| [0003](adr/0003-fastygo-reuse.md) | fastygo reuse: Codex yes (projection, canonical, codes), backend no, FormSet later | Accepted |
| [0004](adr/0004-domain-model.md) | Domain model and data structures | Proposed, sale model blocked |
| [0005](adr/0005-multi-vendor-readiness.md) | Seller-scoped rows, one database, Go-only money writes | Proposed |
| [0006](adr/0006-money-and-ledger.md) | Kopecks, VAT-inclusive prices, deterministic append-only ledger | Proposed, posting blocked |
| [0007](adr/0007-payment-gateway-port.md) | Gateway port, checkout protocol, inbox/outbox, reconciliation | Proposed |
| [0008](adr/0008-rf-compliance.md) | 54-FZ receipts SoT, sale model branches, VAT, 152-FZ, 289-FZ | Proposed, blocked |
| [0009](adr/0009-licensing-and-downloads.md) | Entitlements, purchase codes, re-checked signed downloads | Proposed |
| [0010](adr/0010-ssr-templ-ui8kit.md) | SSR with templ on UI8Kit templ primitives, Codex view models | Proposed |
| [0011](adr/0011-goja-hooks-scope.md) | goja hooks only for non-money glue; money paths in Go | Accepted |
| [0012](adr/0012-design-principles.md) | KISS and YAGNI first; DRY for rules; SOLID as Go interfaces; no OOP hierarchies | Accepted |
| [0013](adr/0013-testing-strategy.md) | TDD for the domain, crash and race tests, contract and golden tests | Proposed |
| [0014](adr/0014-monorepo-layout.md) | Monorepo layout | Proposed |

ADR format: Status, Context, Decision, Consequences. Status values: Proposed,
Accepted, Superseded. "Accepted" means the direction was confirmed by the
architecture review; "blocked" means an owner or legal answer is required
before code.

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
