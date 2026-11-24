# Architecture

`fastygo/market` is a commerce engine for digital goods (Medusa-like,
white-label) plus a reference storefront (GoDudY). The engine is region-,
locale- and brand-agnostic; regions, locales, payment providers and the
marketplace features are modules (ADR-0017, ADR-0018). It runs as a single
store or as a multi-vendor marketplace (commerce mode).

## Engine concepts compared with Medusa

| Medusa | Here | Note |
| --- | --- | --- |
| Commerce modules (product, cart, order, payment, tax, region...) | core bounded contexts (`catalog`, `ordering`, `payments`, `taxdocs`, `licensing`, `ledger`, `trust`, `storefront`) | Go packages in one binary, not separately deployable |
| Region (countries, currency, tax, payment providers) | storefront `region` + `currency` + region plugin module + enabled providers | one region per deployment for data residency |
| Sales channel | storefront (hosts, brand, currency, modules); offers per currency | phase 1: one storefront per instance |
| Payment / tax / notification providers | Go modules (`Gateway` adapters, document issuers) and region policy Go modules (phase 1); goja policy plugins later (ADR-0017) | money-critical code only in Go |
| Workflows with compensation | use cases in `ordering` + inbox/outbox + reconciliation | no workflow engine; explicit state machines |
| Module links | denormalized `seller` and references in PocketBase collections | one SQLite database |
| Marketplace recipe | native `seller` column + `marketplace` module | same schema for single store and marketplace |
| Admin dashboard | PocketBase admin (operator), seller portal in marketplace mode | |
| Digital products, licenses | first-class: releases, license types, entitlements, signed downloads | not core in Medusa |

## Runtime

One Go binary: PocketBase core + our domain packages + templ SSR. SQLite (WAL,
one writer, `busy_timeout`). One deployment per region (data residency: the
RU deployment and its backups stay in Russia, an EU deployment in the EU).
The code is region-neutral; region rules come from a region pack and
everything deployment-specific from config, admin data and env (ADR-0015,
ADR-0016).

```text
 browser ── HTTPS ── reverse proxy (Caddy) ── market binary
                                              ├─ web (templ SSR, handlers, view models -> Codex records)
                                              ├─ core domain: storefront, sellers, catalog, ordering, payments, taxdocs, licensing, ledger, trust
                                              ├─ modules: registry; Go modules: payment adapter (one RU provider), document issuing,
                                              │   region policy `internal/region/ru` (TaxPolicy, DocumentIssuer mapping, ConsentPolicy, LegalProfile);
                                              │   later: goja policy plugins in a sandbox (ADR-0017 trigger), marketplace module (phase 3)
                                              ├─ platform (Tx, Clock, IDs, Outbox, Audit, money, authz)
                                              ├─ platform/pb (PocketBase adapters; repositories implement domain-declared interfaces)
                                              ├─ PocketBase core (collections, auth, files, admin UI, cron)
                                              └─ pb_hooks (goja: non-money glue only, ADR-0011)
 provider ── webhook ──> /hooks/pay/{provider} -> payment_events (inbox) -> inbox worker -> Transition (one DB tx)
 outbox worker -> external effects only (email, tax document issue/retry, provider calls)
 reconciliation cron -> same Transition as the inbox worker
```

## Dependency rule

`web -> domain -> interfaces declared by the domain package`;
`platform/pb` implements them on PocketBase. Domain packages never import
PocketBase. Only `cmd/market` and `internal/platform/pb` know `core.App`.
`internal/platform` holds only things with two or more consumers (`Tx`,
`Clock`, `IDs`, `Outbox`, `Audit`, `money`, `authz`); no repository
interfaces there. `ctx` is passed to every repository, worker and use case;
pure `Transition` functions take no `ctx`. Core packages never import a
region pack or a payment adapter; `cmd/market` wires them by config.

## Write protection (ADR-0005)

Money and fulfilment collections are written only by Go code (custom routes,
workers, domain commands): `orders`, `order_items`, `payments`,
`payment_events`, `refunds`, `tax_documents`, `ledger_entries`,
`entitlements`, `downloads`, `outbox`, `payouts`, `audit_log`. Their
PocketBase API create/update/delete rules are `null` (superuser only), list
and view rules expose only the buyer's own rows where needed (orders,
entitlements). A Go hook (not goja) rejects any update/delete on
`ledger_entries`, and any change of file, sha256 or size on a release in
`published` or `yanked`, including from the admin UI. Refunds and
entitlement revocation happen only through domain commands, never by editing
rows.

## Domain model (PocketBase collections)

All ids are PocketBase ids (15 chars). Money is `amount_minor int64` +
`currency` (ISO 4217); the minor-unit exponent comes from the currency
table (2 for RUB, EUR, USD; 0 for JPY). An order has exactly one currency,
the storefront's. Tax is data on each line (rate, mode, category, code),
resolved by the region `TaxPolicy`. Times are UTC. Seller is denormalized onto every
seller-relevant row so scoping never needs a join (ADR-0005).

### Storefront, operator, identity and sellers

| Collection | Key fields | Unique / invariants |
| --- | --- | --- |
| `storefronts` | code, hosts, commerce_mode (`single_store`,`marketplace`), region (`ru`,`eu`,...), countries (ISO 3166 list where it sells), currency, default_locale, locales, tax_display (`inclusive`,`exclusive`), operator, modules (json: module id -> config), enabled_providers (json: method -> provider), brand (name, logo, favicon, skin, token overrides, email sender name), legal_docs (relations to pages), order_number_prefix | code; one row in phase 1 |
| `operators` | legal_name, country, legal_ids (json, e.g. `{"inn":...}` or `{"vat_id":...}`), address, support_email, tax_registered (bool), tax_registrations (json) | entered in admin only; validated by the region `LegalProfile`; never seeded with real data |
| `users` (auth) | email, name, locale, `pd_consent_version`, `marketing_opt_in` | API: self update of profile fields only |
| `sellers` | slug, display_name, is_operator (bool), country, legal_name, legal_ids (json), tax_profile (json, region-defined, e.g. `{"regime":"usn"}`), sale_model (`principal`,`agent`), commission_bps, status (`draft`,`pending_review`,`active`,`suspended`), contract_version | slug unique; cannot become `active` without sale_model and the legal data the region `LegalProfile` requires; invariant: `sale_model = agent` only when the storefront runs `marketplace` (Go hook) |
| `seller_members` | seller, user, role (`owner`,`editor`,`support`) | (seller, user) |
| `consents` | user or email, document (code from the region `ConsentPolicy`, e.g. `terms`, `privacy`, `marketing`, `seller_contract`), version, accepted_at, ip_hash | |

Commerce mode (ADR-0017): in `single_store` the only seller is the implicit
operator seller (`is_operator = true`), always `principal`; onboarding,
seller portal, commission and payouts are not registered. In `marketplace`
(module `marketplace` enabled) sellers are onboarded and each has its own
sale model. The schema and the `seller` column are the same in both modes;
switching to marketplace needs no migration.

Sale model is per seller data. Both `principal` (the operator sells the
goods as its own) and `agent` (the operator sells on behalf of the seller for
a commission) are supported by the ledger posting rules and the tax document
payload from day one. Which one GoDudY uses is a seed/admin decision before
go-live, not an architecture decision. Seeds carry only slug and display
name with placeholder legal data; real data is entered in admin.

### Catalog

| Collection | Key fields | Unique / invariants |
| --- | --- | --- |
| `categories` | slug, parent, title, sort, `attribute_schema` (json, phase 3) | slug |
| `tags` | slug, title | slug |
| `products` | seller, slug, kind (`site`,`template`,`design`,`extension`,`module`), category, tags, title, excerpt, description, status (`draft`,`in_review`,`published`,`unlisted`,`archived`), cover, gallery, demo_url, attributes (json), rating_avg, rating_count, sales_count, published_at | slug unique globally (one storefront); counters are cache, recomputed by Go cron, never used for money |
| `product_releases` | product, seller, version (semver), changelog, file or storage_key, size, sha256, status (`draft`,`published`,`yanked`), released_at | (product, version); file/sha256/size immutable once published |
| `license_types` | code (`regular`,`extended`), title, terms_version, terms, allows_end_product_sale, max_end_products | code |
| `offers` | product, seller, license_type, price_minor, currency, tax_category (region-defined code, e.g. `standard`, `software_exempt`, `digital_service`), support_months, active | (product, license_type, currency); price in the storefront's `tax_display` convention |

### Ordering and payments

| Collection | Key fields | Unique / invariants |
| --- | --- | --- |
| `orders` | number (storefront prefix from config, e.g. `M-2026-000123`), buyer (user, required), buyer_email (snapshot), status, currency, storefront, region, subtotal_minor, discount_minor, tax_minor, total_minor, tax_display, buyer_country (evidence, if the region needs it), idempotency_key, terms_version, snapshot_digest, paid_at, cancelled_at, cancel_reason (`timeout_requested`,`provider_canceled`,`buyer`,`operator`) | number; idempotency_key; money invariants below |
| `order_items` | order, seller, product, offer, license_type, title_snapshot, product_slug, license_title, unit_price_minor, unit_discount_minor (0 in phase 1), qty (1), line_minor, tax_category, tax_rate_bp, tax_mode (`inclusive`,`exclusive`,`exempt`,`reverse_charge`,`none`), tax_code (region code for documents), tax_minor, sale_model, support_months, license_terms_version, currency, commission_minor, agent_info (json: seller legal snapshot for agent documents) | (order, offer) in phase 1; qty >= 1; never recomputed from live offers |
| `payments` | order, seller (single-seller order), provider, method (provider-neutral code from config, e.g. `card`, `sbp`, `paypal`, `wallet`), provider_payment_id, status, amount_minor, idempotency_key, confirmation_url, status_version, next_reconcile_at, raw (redacted) | idempotency_key; (provider, provider_payment_id) when set; at most one `succeeded` per order |
| `payment_events` (inbox) | provider, event_id, type, payload, verified, status (`received`,`processing`,`processed`,`ignored`,`dead`), locked_until, attempts, received_at, processed_at, error | (provider, event_id) |
| `refunds` | payment, seller, amount_minor, provider, reason_code (`buyer_request`,`not_completable`,`dispute_lost`,`operator`), note, idempotency_key, provider_refund_id, status (`pending`,`succeeded`,`failed`), requested_by | idempotency_key; (provider, provider_refund_id) when set; phase 1: amount = payment amount (full only) |
| `tax_documents` | payment xor refund, seller, region, kind (`receipt`,`receipt_return`,`invoice`,`credit_note`), issuer (region pack + provider), external_id, number, status (`pending`,`issued`,`failed`,`not_required`), lines (json, after discount allocation), attrs (json, region-specific: e.g. RU fiscal fn/fd/fpd, EU invoice number), attempts, last_error, issued_at | (issuer, external_id) when set; at most one live document per payment and per refund for each kind. Single source of truth for document state. Rows exist only if the region `DocumentIssuer` requires them (`not_required` otherwise) |

### Fulfilment and trust

| Collection | Key fields | Unique / invariants |
| --- | --- | --- |
| `entitlements` | user, seller, order_item, product, license_type, purchase_code (UUIDv4), status (`active`,`revoked`,`refunded`,`disputed`), support_months_snapshot, support_until, granted_at | order_item; purchase_code; created only by the `paid` transition |
| `downloads` | entitlement, release, at, ip_hash, ua_hash | |
| `reviews` | product, seller, user, entitlement (required), rating (1..5), body, status (`pending`,`published`,`hidden`), seller_reply | (product, user) |

### Money and operations

| Collection | Key fields | Unique / invariants |
| --- | --- | --- |
| `ledger_entries` | txn_id (deterministic), account, seller (nullable), amount_minor (signed), currency, ref_type, ref_id, created | (txn_id, account); sum per txn_id = 0; no update/delete |
| `payouts` (phase 3) | seller, period_from, period_to, amount_minor (from ledger), status (`draft`,`approved`,`sent`,`failed`), agent_report, bank_ref | (seller, period_from, period_to) |
| `outbox` | topic, idempotency_key, payload, status (`pending`,`processing`,`done`,`dead`), locked_until, attempts, next_attempt_at, last_error | idempotency_key (`email:entitlement:{id}`, `taxdoc:payment:{id}`, ...) |
| `audit_log` | actor, action, ref_type, ref_id, data, at | |

Indexes (in migrations): orders(buyer, status), payments(status,
next_reconcile_at), payment_events(status, locked_until),
outbox(status, next_attempt_at), entitlements(user), every `seller` column;
ledger_entries (txn_id, account) unique, (account, currency, created),
(seller, currency, created), (ref_type, ref_id). Balances are computed on
demand (payout, report, reconciliation), never per page request.

Money source of truth (review r2 P0-B): `ledger_entries` plus the
`Transition` that wrote them. Amounts and statuses on orders, items and
payments are snapshots written in the same transaction. No rollup tables
(`payment_collections`, `order_transactions`); paid/refunded totals are
queries.

SQLite write discipline (ADR-0001, ADR-0007 rule zero): `BEGIN IMMEDIATE`,
explicit `busy_timeout`, short transactions, no network or goja inside a
write transaction; claim, call and apply are separate steps.

### Money invariants (ADR-0006, ADR-0015)

- All amounts of an order share its currency; offers in other currencies are
  not sold on that storefront.
- `tax_display = inclusive` (RU, EU B2C): the price is what the buyer pays,
  tax is extracted. `exclusive`: tax is added per line. Set per storefront.
- `line_minor = (unit_price_minor - unit_discount_minor) * qty`.
- `subtotal_minor = sum(unit_price_minor * qty)`,
  `discount_minor = sum(unit_discount_minor * qty)` (= 0 in phase 1, non-zero
  rejected); inclusive: `total_minor = subtotal_minor - discount_minor =
  sum(line_minor)`; exclusive: `total_minor = sum(line_minor + tax_minor)`.
- `payments.amount_minor = orders.total_minor` at creation.
- Per line, `TaxPolicy` returns `tax_rate_bp`, `tax_mode`, `tax_code`;
  `tax_minor` = inclusive: `round_half_up(line * rate / (10000 + rate))`,
  exclusive: `round_half_up(line * rate / 10000)` (then added to the total);
  `orders.tax_minor = sum(order_items.tax_minor)`; tax documents use the
  same line values, so document total = payment amount. Rates are data from
  the region pack, never constants in core.

## Modules, region packs and configuration (ADR-0015 to ADR-0018)

Interfaces are declared by the consuming core package. Phase 1 implements
them in the Go module `internal/region/ru`; the goja plugin form is a later
phase with the same contract (ADR-0017). The EU column is a design target
only (compile-time fake in tests, phase 4):

| Interface | Declared in | RU (phase 1: Go module `internal/region/ru` + one RU provider module) | EU (phase 4 target: EU policy + Stripe/PayPal modules) |
| --- | --- | --- | --- |
| `TaxPolicy` (batch `ResolveLines`) | `ordering` | VAT 22% inclusive, `software_exempt` 0% (registry software), `none` for non-VAT operators/sellers | VAT by buyer country (OSS), evidence of country, reverse charge for valid VAT ids (later) |
| `DocumentIssuer` | `taxdocs` | 54-FZ receipts through the provider cloud register; agent attributes for `agent` lines | invoices and credit notes with sequential numbering (own generator or provider invoicing) |
| `ConsentPolicy` | `web` / `sellers` | 152-FZ consent documents and versions | GDPR: terms, privacy, marketing opt-in, cookie consent; export and erasure requests |
| `LegalProfile` | `storefront` / `sellers` | INN/OGRN/KPP format and checksum, required fields, operator footer data | VAT id, company number, address requirements |

Payment adapters implement `payments.Gateway` and are enabled per storefront
(`storefronts.enabled_providers`); credentials come from env only.
Adapters that can carry a tax document inside the payment (RU cloud
registers) also implement the optional `payments.InlineDocumentGateway`.

When the plugin runtime exists, plugins never write collections, call
providers or see secrets, and run outside write transactions with timeouts
(ADR-0011, ADR-0017).
Locales are modules: `en` in core, `ru` as `locale-ru` with strings in the
database (ADR-0018). Brand is storefront config plus a UI8Kit skin.

Deployment-specific data lives in three places only: env (secrets,
provider credentials, region code), admin data (`storefronts`, `operators`,
sellers' legal data, legal pages, Russian or other locale texts), and
`deploy/*.example` files with placeholders.

### Content for the storefront

`pages`, `posts`, `menus`, `settings` follow the Codex v0.4 entry shape and
are projected to `content.Entry` for UI8Kit blocks (ADR-0010).

## State machines

Order:

```text
pending -> awaiting_payment -> paid -> fulfilled
                |   ^    |       |-> refunded            (full refund, phase 1)
                |   |    '-> refund_pending -> refunded  (provider succeeded but order not completable: auto full refund)
                |   | late       |-> disputed -> refunded | paid (dispute won)
                v   | succeeded
         cancelled -'  (only after the provider confirms cancel; a later
                        provider `succeeded` moves it to paid + effects)
```

Payment (normalized from every provider; status comes only from
`FetchPayment` / provider responses, never from a webhook body):

```text
created -> pending -> succeeded -> refunded
              |           '-> disputed -> refunded | succeeded
              '-> canceled | failed
```

`waiting_for_capture` is not used: phase 1 creates payments with immediate
capture. If a provider ever needs two-step capture, add `CapturePayment`
first (YAGNI until then).

Transitions are pure functions `Transition(state, event) (state, []Effect,
error)`, table-tested. They are applied with a conditional update
(`WHERE status = expected AND status_version = n`); a lost race is a no-op.
Effects split in two classes (ADR-0007):

- in the same DB transaction as the status change: entitlement insert,
  ledger entries (deterministic `txn_id`), inbox row `processed`;
- in the outbox, written in that same transaction: only external effects
  (purchase email, tax document issue/retry, provider cancel/refund calls).

## Request paths

- Catalog SSR: handler -> catalog read -> view model (Codex records +
  dictionary) -> templ page. No writes.
- Checkout (`POST /checkout`, CSRF, signed-in user, required
  `Idempotency-Key`; use case in `internal/ordering`):
  0. price the cart: offers in the storefront currency, `TaxPolicy` per line,
     consents required by `ConsentPolicy`, provider chosen by config for the
     method;
  1. one DB tx: order + items + payment `created`, keys stored; no network;
  2. `Gateway.CreatePayment` with the payment key;
  3. store `provider_payment_id`, `confirmation_url`, payment `pending`;
  4. redirect. A retry with the same key reuses the order: `awaiting_payment`
     -> repeat 2-3 (the provider returns the same payment); `paid` -> show
     the order; never a second order or payment.
- Return (`GET /checkout/return/{order}`): `FetchPayment` (no tx) -> apply
  tx with `Transition` (same path as the inbox worker); query parameters are
  ignored.
- Webhook (`POST /hooks/pay/{provider}`): verify -> one insert into
  `payment_events` -> 2xx; unique conflict = already accepted, 2xx.
- Inbox worker (one per process), three steps: claim tx (`received` ->
  `processing`, `locked_until`, commit); `FetchPayment` outside any tx;
  apply tx (`Transition`, in-tx effects, `processed`, commit). Expired locks
  return to `received`; after N attempts -> `dead` + alert.
- Outbox worker (one per process): same claim/lease protocol, external
  effects only, keyed by `idempotency_key`.
- Timeout (cron): `awaiting_payment` older than 30 min -> outbox
  `CancelPayment`; local `cancelled` only after the provider confirms.
- Reconciliation (cron, every 5 min, same `Transition`):
  1. payments `created`/`pending` older than 10 min -> `FetchPayment`;
  2. orders `awaiting_payment` or `cancelled` with a provider payment id ->
     `FetchPayment` (late payment);
  3. orders `paid` without entitlement or without ledger `txn_id` -> replay
     the in-tx effects (idempotent);
  4. tax documents not `issued`/`not_required` -> `DocumentIssuer.Fetch`,
     then retry `Issue` via outbox, up to the attempt cap, then page alert
     and operator task;
  5. provider `succeeded` but the order cannot be completed (offer removed,
     invariant fails, amount mismatch) -> order `refund_pending` -> the same
     full-refund command as the operator refund, plus an alert.
- Refund (operator command or auto-refund, phase 1 full only): tx: row
  `refunds` `pending` (provider, key, reason code, note), commit ->
  `Gateway.Refund` with its key -> on confirmation one tx: payment/order
  `refunded`, ledger reversal, entitlement `refunded`, return tax document
  (`receipt_return` or `credit_note`) `pending` + outbox issue, if the region
  requires one.
- Download: account link -> check entitlement `active` and release
  `published` -> 302 to `/dl/{token}` (HMAC, 10 min) -> `/dl/{token}`
  re-checks both -> stream file, log `downloads`.
- Shutdown: workers stop claiming on `OnTerminate`, finish or release their
  leases within the context deadline.
