# Plan

Result first, then scale. Each step is small, ends green (`go test ./...`,
`go vet ./...`, templ generate clean, UI8Kit checks for blocks and layouts),
and is deployable. Status: all steps Proposed until the architecture review.

## Phase 0. Skeleton (1 week)

1. `go.mod` (Go 1.27), `cmd/market/main.go`: `pocketbase.New()`, `jsvm`,
   `migratecmd` (Go migrations), pinned PocketBase `v0.40.x`.
2. `internal/platform`: ports (`Tx`, `Clock`, `IDs`, `Outbox`, `Audit`),
   `money` (kopecks), PocketBase adapters, test helpers on a temp data dir.
3. UI8Kit: copy `ui8kit/contract` `.cursor/rules` and `.cursor/skills` as is;
   install templ primitives with the ui8kit CLI into `ui/kit` (kit zone, never
   edited); `ui/dict/en.json`; Tailwind build with UI8Kit tokens.
4. `scripts/verify.sh`: gofmt, vet, test, `templ generate` diff, ui8px gate on
   `ui/blocks` and `ui/layouts`, literal-text and raw-native-tag checks.
5. `AGENTS.md` for the repo.

## Phase 1. GoDudY MVP storefront (4 to 6 weeks)

0. BLOCKER: owner question #1 (GoDudY brand or separate legal entity) and the
   accountant's VAT answer. Catalog work may start; the GoDudY seed
   `sale_model`, ledger postings and receipts wait for the answers.
1. Migrations: sellers (GoDudY seed), categories, tags, products,
   product_releases, license_types, offers, users fields, consents; uniques
   and indexes from `architecture.md`; API rules closing money collections;
   Go write-protection hooks.
2. Catalog SSR: home, category, product page, search (SQLite FTS5), legal
   pages (offer, privacy, refund policy, requisites of IT Garage LLC).
3. Auth: signed-in buyers only (no guest checkout); email OTP or password
   per owner answer; account page.
4. Checkout "buy now" (one offer per order, items table already plural) by
   the protocol in ADR-0007: required idempotency key, tx first, then
   provider, then store the provider id; return URL; one provider adapter
   with `CancelPayment`, `Refund`, `RegisterReceipt`, `FetchReceipt`; card
   and SBP through that provider; provider cloud receipts. Discount is 0.
5. Inbox worker and outbox worker (leases, one each), timeout cancel via the
   provider, reconciliation (4 selections), transition tables.
6. Entitlements, purchase codes, signed downloads with re-check, purchase
   email.
7. Ledger entries with deterministic txn ids in the same transaction as the
   transition (branch A or B of ADR-0006).
8. Full refunds and disputes as operator commands (no partial refunds).
9. Crash and race test suite of ADR-0013 green before live keys.
10. Ops: Caddy, systemd, daily PocketBase backup + continuous Litestream to
   RF object storage, `/metrics`, external probes, SLO dashboard.
11. Launch with sandbox then live keys.

Exit: the success definition in `intent.md` on production.

## Phase 2. Harden and grow (4 weeks)

1. Cart (several offers), coupons with the discount allocation rule of
   ADR-0006, refunds attached to order items.
2. Reviews (only for entitlement holders), ratings, seller reply.
3. Second provider and method routing (fallback if one is down).
4. Release update notifications, license verification API
   (`GET /api/v1/purchase-codes/{code}` for sellers' products).
5. SLO burn-rate alerts, error budget policy in force.

## Phase 3. Multi-vendor (HinddY) (6 to 8 weeks, after legal review)

1. Seller onboarding: application, INN check (EGRUL/EGRIP), agency or
   license-resale contract acceptance, moderation queue.
2. Seller portal (SSR): products, releases, sales, balance.
3. Agent model (unless already in phase 1 via branch B): receipts with agent
   and supplier attributes, commission, `seller_payable` ledger, monthly
   agent report, payouts (unique period, amount from ledger, approval
   required). No `npd` sellers before the legal answer.
4. Category attribute schemas (Codex RecordType + FormSet when tagged).
5. 289-FZ obligations if the platform qualifies (open question).

## Phase 4. Scale (when metrics say so)

Object storage + CDN for files and images, search engine if FTS5 is not
enough, cache for hot pages, read-heavy pages pre-rendered. Exit plan from
SQLite only if write load demands it (domain is behind ports).

## Review 2026-11-24 (BalunMY)

Phase 1 now has the owner blocker as step 0, the ADR-0007 checkout protocol,
two workers with leases, wider reconciliation, full refunds and disputes, and
the crash and race tests before live keys.
