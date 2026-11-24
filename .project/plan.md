# Plan

Result first, then scale. Each step is small, ends green (`go test ./...`,
`go vet ./...`, templ generate clean, UI8Kit checks for blocks and layouts),
and is deployable. Status: all steps Proposed until the architecture review.

## Phase 0. Skeleton (1 week)

1. `go.mod` (Go 1.27), `cmd/market/main.go`: `pocketbase.New()`, `jsvm`,
   `migratecmd` (Go migrations), pinned PocketBase `v0.40.x`.
2. `internal/platform`: ports (`Tx`, `Clock`, `IDs`, `Outbox`, `Audit`),
   `money` (minor units + ISO 4217 currency table), PocketBase adapters, test helpers on a temp data dir.
3. UI8Kit: copy `ui8kit/contract` `.cursor/rules` and `.cursor/skills` as is;
   install templ primitives with the ui8kit CLI into `ui/kit` (kit zone, never
   edited); `ui/dict/en.json`; Tailwind build with UI8Kit tokens.
4. `scripts/verify.sh` (exists: secret, legal-id, PII, Cyrillic greps and
   Go checks); add `templ generate` diff, ui8px gate on `ui/blocks` and
   `ui/layouts`, literal-text and raw-native-tag checks.
5. `AGENTS.md` for the repo; `deploy/env.example` with placeholders only.
6. Consumer-side interfaces `TaxPolicy`, `DocumentIssuer`, `ConsentPolicy`,
   `LegalProfile`, fake pack and fake gateway for tests (ADR-0015).
7. `internal/modules`: manifest loader, capability registry, load order,
   config validation, goja plugin sandbox with timeouts and contract-test
   harness (ADR-0017); `locale-en` dictionary in core (ADR-0018).

## Phase 1. RU deployment MVP: RU pack + one RU provider (4 to 6 weeks)

1. Migrations: storefronts, operators, sellers (GoDudY seed as `draft`,
   placeholder legal data), categories, tags, products,
   product_releases, license_types, offers, users fields, consents; uniques
   and indexes from `architecture.md`; API rules closing money collections;
   Go write-protection hooks.
2. Catalog SSR: home, category, product page, search (SQLite FTS5), legal
   pages as content records, footer requisites rendered from operator data
   entered in admin.
3. Auth: signed-in buyers only (no guest checkout); email OTP or password
   per owner answer; account page.
4. Checkout "buy now" (one offer per order, items table already plural) by
   the protocol in ADR-0007: required idempotency key, tx first, then
   provider, then store the provider id; return URL; one provider adapter
   chosen by config, implementing `Gateway` and `InlineDocumentGateway`;
   card and SBP through that provider. Discount is 0.
5a. `modules/region-ru` plugin (tax table 22% / registry exempt / none,
   receipt field mapping, consent documents, INN/OGRN/KPP rules, checkout
   fields) + 54-FZ issuing through the provider Go module; `locale-ru`
   strings in the database (ADR-0018). Storefront runs in `single_store`
   mode by default; the reference brand GoDudY is a skin + config.
5. Inbox worker and outbox worker (leases, one each), timeout cancel via the
   provider, reconciliation (4 selections), transition tables.
6. Entitlements, purchase codes, signed downloads with re-check, purchase
   email.
7. Ledger entries with deterministic txn ids in the same transaction as the
   transition; posting rules for both `principal` and `agent` (agent rules
   tested now, active only with the `marketplace` module).
8. Full refunds and disputes as operator commands (no partial refunds).
9. Crash and race test suite of ADR-0013 green before live keys.
10. Ops: Caddy, systemd, daily PocketBase backup + continuous Litestream to
   object storage in the deployment's region (RU), `/metrics`, external
   probes, SLO dashboard.
11. Go-live checklist (admin/env, not code): operator legal data, GoDudY
   `sale_model` and legal data, provider credentials, RU texts and legal
   pages, accountant and lawyer sign-off (`open-questions.md` C).
12. Launch with sandbox then live keys.

Exit: the success definition in `intent.md` on production.

## Phase 2. Harden and grow (4 weeks)

1. Cart (several offers), coupons with the discount allocation rule of
   ADR-0006, refunds attached to order items.
2. Reviews (only for entitlement holders), ratings, seller reply.
3. Second provider and method routing (fallback if one is down).
4. Release update notifications, license verification API
   (`GET /api/v1/purchase-codes/{code}` for sellers' products).
5. SLO burn-rate alerts, error budget policy in force.

## Phase 3. Marketplace module (HinddY) (6 to 8 weeks, after legal review)

Enabled per storefront by `commerce_mode = marketplace`; no migration. If
GoDudY must sell as `agent` at go-live, the minimal part (agent sale model,
agent documents, `seller_payable`) moves into phase 1 as config of this
module.

1. Seller onboarding: application, INN check (EGRUL/EGRIP), agency or
   license-resale contract acceptance, moderation queue.
2. Seller portal (SSR): products, releases, sales, balance.
3. Agent model operations (posting rules already exist from phase 1): receipts with agent
   and supplier attributes, commission, `seller_payable` ledger, monthly
   agent report, payouts (unique period, amount from ledger, approval
   required). No `npd` sellers before the legal answer.
4. Category attribute schemas (Codex RecordType + FormSet when tagged).
5. 289-FZ obligations if the platform qualifies (open question).

## Phase 4. Second region (EU pack, when a deployment is planned)

1. `region/eu`: VAT by buyer country (OSS) with country evidence, invoices
   and credit notes with sequential numbering, GDPR consents, export and
   erasure, VAT id validation (reverse charge later).
2. Stripe and PayPal adapters (`Gateway`), webhook verification, contract
   tests.
3. EU deployment: config, EU hosting, EUR storefront, offers in EUR.

## Phase 5. Scale (when metrics say so)

Object storage + CDN for files and images, search engine if FTS5 is not
enough, cache for hot pages, read-heavy pages pre-rendered. Exit plan from
SQLite only if write load demands it (domain is behind ports).

## Review 2026-11-24 (BalunMY)

Phase 1 now has the owner blocker as step 0, the ADR-0007 checkout protocol,
two workers with leases, wider reconciliation, full refunds and disputes, and
the crash and race tests before live keys.

## Revision 2026-11-24 (owner: region-neutral core)

Phase 1 is the RU pack plus one RU provider; no owner blocker remains for
code (GoDudY sale model and legal data are go-live configuration); EU pack
added as phase 4.

## Revision 2026-11-24 (owner: engine, modules, commerce mode)

Phase 0 adds the module system and locale-en; phase 1 runs the RU region
plugin and `locale-ru` in `single_store` mode; phase 3 is the `marketplace`
module.
