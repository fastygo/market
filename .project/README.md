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

| ADR | Decision |
| --- | --- |
| [0001](adr/0001-pocketbase-extend-mode.md) | PocketBase as a Go framework (extend mode), one binary |
| [0002](adr/0002-platform-baas-layer.md) | A thin in-process `platform` layer over PocketBase, not a second BaaS |
| [0003](adr/0003-fastygo-reuse.md) | fastygo reuse: Codex yes (projection, canonical, codes), backend no, FormSet later |
| [0004](adr/0004-domain-model.md) | Domain model and data structures |
| [0005](adr/0005-multi-vendor-readiness.md) | Seller-scoped rows from day one, one database |
| [0006](adr/0006-money-and-ledger.md) | Integer kopecks, RUB only, append-only ledger |
| [0007](adr/0007-payment-gateway-port.md) | Payment gateway port with adapters, webhook inbox, outbox |
| [0008](adr/0008-rf-compliance.md) | 54-FZ via provider receipts, 152-FZ hosting and consents, VAT modes, 289-FZ later |
| [0009](adr/0009-licensing-and-downloads.md) | Entitlements, purchase codes, signed short-lived downloads |
| [0010](adr/0010-ssr-templ-ui8kit.md) | SSR with templ on UI8Kit templ primitives, Codex view models |
| [0011](adr/0011-goja-hooks-scope.md) | goja hooks only for non-money glue; money paths in Go |
| [0012](adr/0012-design-principles.md) | KISS and YAGNI first; DRY for rules; SOLID as Go interfaces; no OOP hierarchies |
| [0013](adr/0013-testing-strategy.md) | TDD for the domain, contract tests for gateways, golden tests for SSR |
| [0014](adr/0014-monorepo-layout.md) | Monorepo layout |

ADR format: Status, Context, Decision, Consequences. Status values: Proposed,
Accepted, Superseded. All ADRs start as Proposed until the architecture review
round is done.
