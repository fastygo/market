# Plan

Result first, then scale. Each step is small, ends green (`go test ./...`,
`go vet ./...`, templ generate clean, UI8Kit checks for blocks and layouts),
and is deployable. Status: all steps Proposed until the architecture review.

## Target host (owner t223u)

1 vCPU 3.3 GHz, 2 GB RAM, 30 GB NVMe, 100 Mbit/s. One Go binary
(PocketBase + templ SSR), no Node, Postgres or Redis at runtime; built
off-server and deployed as a binary; release files in S3-compatible RU
storage behind pre-signed redirects; backups to external storage. Memory
budget and load-test plan: `slo.md`.

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
7. `internal/modules`: registry of Go modules by name, manifest struct,
   config validation (ADR-0017); no goja plugin sandbox in phase 0-1;
   `locale-en` dictionary in core (ADR-0018).
8. `platform.Tx`: `BEGIN IMMEDIATE`, explicit `busy_timeout`, bounded retry,
   "in tx" flag used by tests to assert no provider call inside a write
   transaction (ADR-0001, ADR-0007 rule zero).

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
5a. `internal/region/ru` Go module (dated tax table 22% / registry exempt /
   none, batch `ResolveLines`, receipt field mapping, consent documents,
   INN/OGRN/KPP rules, checkout fields, table tests that later become the
   plugin contract vectors) + 54-FZ issuing through the provider Go module; `locale-ru`
   strings in the database (ADR-0018). Storefront runs in `single_store`
   mode by default; the storefront brand (StorEEE Market, ADR-0020) is a
   skin + config; GoDudY is a seller.
5. Inbox worker and outbox worker (leases, one each), timeout cancel via the
   provider, reconciliation (5 selections, including the auto-refund of
   paid-but-uncompletable orders), transition tables; claim, provider call
   and apply in separate transactions.
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
   `sale_model` and legal data (if the lawyer says `agent`, the launch
   cannot be `single_store`: pull the minimal marketplace part from phase 3
   into phase 1 first; the admin invariant rejects `agent` in
   `single_store`), provider credentials, RU texts and legal
   pages, accountant and lawyer sign-off (`open-questions.md` C); the
   public offer, license and refund policy texts (ADR-0024) are reviewed by
   a practicing lawyer before they are published.
12. Launch with sandbox then live keys.

Exit: the success definition in `intent.md` on production.

## Phase 2. Harden and grow (4 weeks)

0. Refund policy (ADR-0024): `RefundPolicy` port with the RU pack values
   as config, buyer refund requests (automatic for pre-download and
   cooling-off, queue for defects and goodwill), `access_evidence` log with
   retention job, evidence pack command, risk signals for manual review;
   offer and checkout notices. Watermark delivery after the technique
   decision (open question C12). Until then phase 1 refunds stay operator
   commands applied by the same rules by hand.

1. Cart (several offers), coupons with the discount allocation rule of
   ADR-0006, refunds attached to order items.
2. Reviews (only for entitlement holders), ratings, seller reply.
3. Second provider and method routing (fallback if one is down).
4. Release update notifications, license verification API
   (`GET /api/v1/purchase-codes/{code}` for sellers' products).
5. SLO burn-rate alerts, error budget policy in force.

## Phase 3. Marketplace module: StorEEE vendor model (10 to 14 weeks, after legal review)

Design: ADR-0020 (sellers, profiles, discovery), ADR-0021 (commission,
earnings, payouts), ADR-0022 (moderation, releases, licensing), ADR-0023
(referral seam). Enabled per storefront by `commerce_mode = marketplace`;
no migration for single store, whose behaviour stays unchanged. If GoDudY
must sell as `agent` at go-live, the minimal part (agent sale model, agent
documents, seller share accounts) moves into phase 1 as config of this
module. Owner answers needed first: `open-questions.md` E (and C2, C3).

3a. Money base: `commission_rules`, `price_rules`, order item snapshots
    (`commission_bps`, `earnings_minor`, `hold_until`), accounts
    `seller_pending`/`seller_available` (rename of `seller_payable`),
    monthly earnings close cron (`earnings_close_day`, `hold_days` 0),
    refund and dispute attribution (`bearer` seller or platform goodwill),
    seller-borne agent fees, optional buyer fee and handling fee (default
    0), payout minimum and payout fee payer, negative balance rule; ledger
    tests with odd amounts, close-day boundaries, refund before and after
    close and after payout (negative balance), goodwill refund leaving the
    seller balance untouched, lost dispute after release.
3b. Seller lifecycle: applications, KYC-lite documents (private storage),
    region verification module (RU registry and self-employed checks behind
    a port), approval, suspension, closure, audit; 289-FZ notice and appeal
    records if the region requires them.
3c. Submissions and moderation queue with leases, rejection reasons,
    pre-checks, release review, pre-signed uploads; malware scan port.
3d. Public author pages `/authors/{slug}`, portfolio, followers, levels and
    badges cron, item comments, reports, featured slots, search facets and
    author search, seller analytics (`item_stats_daily`, batched view
    counter).
3e. Payouts: batches, approval, `PayoutGateway` manual adapter (bank export
    + confirmation), encrypted payout methods, region seller reports (RU
    monthly agent report), seller documents before `paid` where the region
    requires them.
3f. Category attribute schemas (Codex RecordType + FormSet when tagged).

Later modules (seams only, not scheduled): `affiliates` with a separate
partner site (ADR-0023); `suppliers` with YML/XML feed import (ADR-0020).

## Phase 4. Second region (EU pack, when a deployment is planned)

0. Trigger for the ADR-0017 goja policy plugin runtime (sandbox, manifests,
   contract tests); RU policy ported to the plugin form must pass the
   `internal/region/ru` vectors. May start earlier on an owner decision
   (open question A3).
1. EU policy: VAT by buyer country (OSS) with country evidence, invoices
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

## Review r2 2026-11-24 (BalunMY)

Phase 0 is narrower: no goja policy sandbox, `platform.Tx` with
`BEGIN IMMEDIATE` and `busy_timeout`. Phase 1 RU policy is the Go module
`internal/region/ru`; no Medusa-style tables (see `reference/medusa-mapping.md`
decisions). Reconciliation gets the auto-refund branch; go-live checklist
has the agent-vs-single-store rule. The plugin runtime moves to its trigger
(phase 4 or owner decision).

## Decisions 2026-11-24 (owner t223u)

A3 decided (Go region rules in phase 1). Product types are modules with a
plain default page (ADR-0019); phase 0 builds only the plain type with
Download and Preview actions. Host profile, memory budget and load-test plan
added (`slo.md`). Release files never pass through the app (ADR-0009).

## Phase 0 status 2026-11-24

Done: `go.mod` (PocketBase v0.40.6, templ v0.3.1070), `cmd/market`,
`internal/platform/money`, `internal/platform/pb` (catalog and storefront
readers), Go migrations for storefronts, sellers, categories, products,
product_releases, offers with a placeholder seed, templ SSR pages (home,
catalog, category, plain product page with Download and Preview), locale-en
dictionary, Tailwind build, tests (money, i18n, migrations and seed,
repository, handlers end to end). Measured on the dev box (production
build, `GOMEMLIMIT` default 512 MiB): RSS 43 MB idle, 54 MB after 3000
product page requests at 16 in parallel (peak 56 MB); binary 28 MB.
Phase 0 closed (t224u): `pb.RunTx` + `platform/tx` (`BEGIN IMMEDIATE` via
`_txlock=immediate`, explicit `busy_timeout` 5 s, bounded `SQLITE_BUSY`
retry, "in tx" context flag and open-tx counter for rule zero),
`internal/modules` (manifest, mode check, required and singular
capabilities, load order, cycles), `deploy/` (systemd unit, env example
with placeholders), Go guards (REST writes to money and fulfilment
collections denied even for superusers, ledger append-only, published
releases immutable, `agent` seller rejected in `single_store`).

### Skin note 2026-11-24 (t224u)

- GoDudY skin is the owner's CSS, verbatim (`ui/styles/skins/godudy.css`);
  the old wp-fasty-derived tokens are gone. The neutral UI8Kit theme is now
  `skins/default.css`. Each storefront picks its skin via `brand.skin`;
  `make css` builds one stylesheet per skin.
- Fonts are self-hosted (OFL, latin + cyrillic subsets, about 300 KB) so the
  storefront works offline and sends no visitor IPs to a font CDN.
- `--spacing`: the owner's file had `0.24rem` (3.84 px steps); ui8px lint
  v0.3.0 did not flag it (its grid check assumes a 4 px base). Owner decision
  t225u: `--spacing: 0.25rem`, so Tailwind steps match the 8+4 grid again.
  This is the only change to the owner's skin file. Remaining ui8px lint
  findings are skin-independent (`border-t`/`border-b`, `no-underline`
  unknown; `max-w-xs`, `min-h-screen` not allowed in layout scope;
  `outline-ring/50` in the base layer).

## Phase 1 status 2026-11-24 (t224u)

Done, each step in its own commit with `verify.sh` and `go test -race`
green: `internal/region/ru` (dated VAT table, batch `ResolveLines`),
`internal/ordering` (pricing invariants, transitions, posting rules,
checkout, apply, inbox and outbox workers with leases, reconciliation),
`internal/payments` (Gateway port) + `fakegw`, collections (users fields,
consents, orders, order_items, payments, payment_events, entitlements,
downloads, outbox, ledger_entries), `internal/licensing` (HMAC `/dl`
tokens, re-check, download log) with `LocalPresigner` and
`platform/s3` (SigV4 presign, AWS documentation vector), HTTP routes
(`POST /checkout`, `GET /checkout/return/{order}`, `POST
/hooks/pay/{provider}`, dev-only `POST /fakepay/{id}/{event}`, `GET
/products/{slug}/download`, `GET /dl/{token}`).

Added (t225u): order numbers `M-YYYY-NNNNNN`; full refunds as a domain
command (`POST /admin/orders/{order}/refund`, superuser token; reasons
`buyer_request`, `not_completable`, `operator`; the engine also records
`dispute_lost` and `provider_initiated`) through `Gateway.RefundPayment`
with a per-attempt idempotency key, entitlement `refunded`, ledger
reversal `pay:{id}:refund:{refund}`; disputes (payment and order
`disputed`, entitlements suspended, alert; won -> `paid`/`active`; lost
-> entitlements `revoked`, ledger `pay:{id}:dispute:{n}`, refund row
`dispute_lost`); the auto-refund branch (paid but not completable or
amount mismatch -> `refund_pending` + automatic refund + alert); the
timeout cron (every minute: outbox `CancelPayment`, the order is cancelled
only on the provider's confirmation, a late success becomes `paid`); tax
documents through the `taxdocs.Issuer` port with a fake register (RU
receipts `income` and `income_return`, outbox retries, attempt cap ->
`failed` + alert, async registers fetched by reconciliation); `alerts`
collection (locked, superusers only). Dev routes `POST
/fakepay/{id}/succeed|dispute|win|lose`.

ADR-0013 tests in place: #1 to #10, rule zero, sale model invariant,
guards, fake gateway refund and fake issuer idempotency.

Not done (next): SSR checkout and account pages with cookie session and
CSRF (checkout is JSON with bearer tokens), a real mail transport (outbox
`email.purchase` only logs), a real payment provider and a real
`DocumentIssuer` (receipts are wired only with the fake gateway), operators
collection (VAT payer status is read from the seller tax profile), partial
refunds (phase 2). Open: the 54-FZ payment subject and method codes are
placeholders (`service`, `full_payment`) until the owner confirms them.

Schema notes: lease and schedule times (`locked_until`, `next_attempt_at`,
`next_reconcile_at`) are unix milliseconds; consents carry `order_key`.

## Dark theme 2026-11-24 (t228u)

Both skins already carry a `.dark` token block (owner's GoDudY file and the
UI8Kit default), used as is. The head of `layout.templ` has a small inline
script (recorded shell exception, like html/head/body) that sets `.dark` on
`<html>` before first paint from `localStorage.theme` or
`prefers-color-scheme`; the header toggle is `compose.Button` (ghost, icon,
`aria-label`, `aria-pressed`), hidden without JavaScript. Choosing the
system value again clears the stored choice. Contrast in GoDudY dark:
foreground 16.2:1, muted-foreground 5.2:1 on background but 4.4:1 on card
and muted (below AA 4.5:1 for small text), primary-foreground on primary
3.6:1 (same as light; AA only for large text), borders about 1.3 to 1.5:1
(decorative). Owner decision pending on those values.

## Direction 2026-11-24 (owner t229u, t230u): StorEEE Market, vendor model

The storefront brand is StorEEE Market (config and dictionary only); GoDudY
is a seller with a profile and portfolio. Phase 3 is rewritten around the
Envato-style vendor model (ADR-0020 to ADR-0022) with the referral seam for
a later three-level affiliate partner site (ADR-0023). Code steps for the
brand (no new design): storefront seed brand name and tagline, title and
meta from brand config, `MARKET_BASE_URL` for absolute links, skin id per
open question G2. Resource fit checked against `slo.md`: new jobs are
nightly or monthly batches through indexes, view counting is one batched
write per minute, no per-request ledger scans.

## Decision 2026-11-24 (owner t234u): refund and anti-abuse policy

ADR-0024 adds the digital goods refund policy behind a region
`RefundPolicy` port (RU defaults as pack config, marked needs counsel),
the evidence log for chargebacks, watermark seam and anti-abuse signals
that never refuse lawful refunds. Built in phase 2 step 0; the offer review
by a practicing lawyer is a go-live item.

## Decisions 2026-11-24 (owner t237u)

Answered: flat 50% commission (configurable default), monthly payouts, no
exclusivity (one common non-exclusive license), all seller forms accepted
(self-employed rules need counsel), GoDudY is a pseudonymous self-employed
seller (identity only in the operator contract and private runtime data),
purchase restriction for refund abusers after operator review (ADR-0024).
Plan consequences:

- Brand step (code, small): add skin `storeee` (`ui/styles/skins/storeee.css`
  as a copy of the `godudy` tokens, build entry, `make css`), set the
  storefront seed `brand.skin` to `storeee`; `godudy` stays as a reference
  skin.
- Go-live: GoDudY is a third-party seller, so the minimal marketplace part
  (agent or sublicense scheme per counsel, seller share accounts,
  self-employed receipts per counsel) moves into phase 1 before the launch
  (ADR-0005 invariant, step 11 above).
- Phase 2 step 0 adds `purchase_restrictions` and the checkout check.

## Decisions 2026-11-24 (owner t238u)

"As on Envato Market" for E3, E5, E6 (ADR-0021): no hold by default,
monthly close on the 8th and payout above a per-currency minimum (RUB
placeholder 5000), seller bears refunds, chargebacks and their agent fees
and payout fees by default, full sale reversal on a seller-borne refund,
goodwill refunds as platform expense, optional buyer fee and handling fee
(default 0, owner question E14). Affiliate commissions stay in the
platform share. Plan consequence: phase 3a scope above; the minimal
marketplace part for GoDudY at go-live uses the same rules.

## Vendor slice 1 status 2026-11-25 (owner t317u)

Done (ADR-0020 "Slice 1 implemented"): brand step (skin `storeee`, brand
config for titles, meta, `og:*` and JSON-LD, `MARKET_BASE_URL` alias),
migrations `1764000006` (seed-branded storefront -> StorEEE Market, skin
`storeee`) and `1764000007` (seller profile fields and statuses; GoDudY
`active` with a pseudonymous placeholder profile), public vendor UI (home
hero with search, categories, item cards with authors, catalog search,
product author block and more from the author, `/authors/{slug}`). Checkout,
cabinet and downloads unchanged.

Next slices, in order:

1. Seller applications: `seller_applications`, operator review queue,
   approval invariants (sale model, contract version, region legal data
   kept as private runtime data).
2. Seller dashboard: profile and avatar, items and releases, stats
   (`item_stats_daily`).
3. Commission ledger split and payouts (ADR-0021): seller share accounts,
   monthly close, payout minimum. The agent or payout scheme and
   self-employed receipts wait on legal counsel.
4. Reviews and item comments (entitlement required), reports.
5. Followers, curated features, FTS5 search, sitemap, suspended and closed
   profile pages, public file access for covers and avatars.
