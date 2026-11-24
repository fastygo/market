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

1. Migrations: sellers (GoDudY seed), categories, tags, products,
   product_releases, license_types, offers, users fields, consents.
2. Catalog SSR: home, category, product page, search (SQLite FTS5), legal
   pages (offer, privacy, refund policy, requisites of IT Garage LLC).
3. Auth: email OTP or password via PocketBase auth; account page.
4. Checkout "buy now" (one offer per order, items table already plural):
   order + payment in one tx, one provider adapter (owner picks, see open
   questions), card and SBP through that provider, provider cloud receipts.
5. Webhook inbox, outbox worker, reconciliation cron, order state machine.
6. Entitlements, purchase codes, signed downloads, purchase email.
7. Ledger entries for every paid and refunded payment (principal model).
8. Ops: Caddy, systemd, daily PocketBase backup + continuous Litestream to
   RF object storage, `/metrics`, external probes, SLO dashboard.
9. Launch with sandbox then live keys; manual refunds via admin.

Exit: the success definition in `intent.md` on production.

## Phase 2. Harden and grow (4 weeks)

1. Cart (several offers), coupons, refunds from the operator UI.
2. Reviews (only for entitlement holders), ratings, seller reply.
3. Second provider and method routing (fallback if one is down).
4. Release update notifications, license verification API
   (`GET /api/v1/purchase-codes/{code}` for sellers' products).
5. SLO burn-rate alerts, error budget policy in force.

## Phase 3. Multi-vendor (HinddY) (6 to 8 weeks, after legal review)

1. Seller onboarding: application, INN check (EGRUL/EGRIP), agency or
   license-resale contract acceptance, moderation queue.
2. Seller portal (SSR): products, releases, sales, balance.
3. Agent model: receipts with agent and supplier attributes, commission,
   `seller_payable` ledger, monthly agent report, payouts.
4. Category attribute schemas (Codex RecordType + FormSet when tagged).
5. 289-FZ obligations if the platform qualifies (open question).

## Phase 4. Scale (when metrics say so)

Object storage + CDN for files and images, search engine if FTS5 is not
enough, cache for hot pages, read-heavy pages pre-rendered. Exit plan from
SQLite only if write load demands it (domain is behind ports).
