# Architecture

One Go binary: PocketBase core + our domain packages + templ SSR. SQLite (WAL,
one writer, `busy_timeout`) on a server located in Russia. Files on local
disk, later S3-compatible storage in Russia.

```text
 browser ── HTTPS ── reverse proxy (Caddy) ── market binary
                                              ├─ web (templ SSR, handlers, view models -> Codex records)
                                              ├─ domain: sellers, catalog, ordering, payments, fiscal, licensing, ledger, trust
                                              ├─ platform (Tx, Clock, IDs, Outbox, Audit, money, authz)
                                              ├─ platform/pb (PocketBase adapters; repositories implement domain-declared interfaces)
                                              ├─ PocketBase core (collections, auth, files, admin UI, cron)
                                              └─ pb_hooks (goja: non-money glue only, ADR-0011)
 provider ── webhook ──> /hooks/pay/{provider} -> payment_events (inbox) -> inbox worker -> Transition (one DB tx)
 outbox worker -> external effects only (email, receipt retry, provider calls)
 reconciliation cron -> same Transition as the inbox worker
```

## Dependency rule

`web -> domain -> interfaces declared by the domain package`;
`platform/pb` implements them on PocketBase. Domain packages never import
PocketBase. Only `cmd/market` and `internal/platform/pb` know `core.App`.
`internal/platform` holds only things with two or more consumers (`Tx`,
`Clock`, `IDs`, `Outbox`, `Audit`, `money`, `authz`); no repository
interfaces there. `ctx` is passed to every repository, worker and use case;
pure `Transition` functions take no `ctx`.

## Write protection (ADR-0005)

Money and fulfilment collections are written only by Go code (custom routes,
workers, domain commands): `orders`, `order_items`, `payments`,
`payment_events`, `refunds`, `fiscal_receipts`, `ledger_entries`,
`entitlements`, `downloads`, `outbox`, `payouts`, `audit_log`. Their
PocketBase API create/update/delete rules are `null` (superuser only), list
and view rules expose only the buyer's own rows where needed (orders,
entitlements). A Go hook (not goja) rejects any update/delete on
`ledger_entries`, and any change of file, sha256 or size on a release in
`published` or `yanked`, including from the admin UI. Refunds and
entitlement revocation happen only through domain commands, never by editing
rows.

## Domain model (PocketBase collections)

All ids are PocketBase ids (15 chars). Money is `int64` kopecks (`*_minor`),
currency always `RUB`. Times are UTC. Seller is denormalized onto every
seller-relevant row so scoping never needs a join (ADR-0005).

### Identity and sellers

| Collection | Key fields | Unique / invariants |
| --- | --- | --- |
| `users` (auth) | email, name, locale, `pd_consent_version`, `marketing_opt_in` | API: self update of profile fields only |
| `sellers` | slug, display_name, legal_name, inn, ogrn, kpp, tax_regime (`osn`,`usn`,`npd`), sale_model (`principal`,`agent`), commission_bps, status (`draft`,`pending_review`,`active`,`suspended`), contract_version | slug unique; tax_regime vs offer vat_mode invariant (ADR-0008) |
| `seller_members` | seller, user, role (`owner`,`editor`,`support`) | (seller, user) |
| `consents` | user or email, document (`offer`,`privacy`,`marketing`,`seller_contract`), version, accepted_at, ip_hash | |

GoDudY is seller #1. Its `sale_model` is BLOCKED on owner question #1 (brand
of IT Garage = `principal`, or separate legal entity = `agent`). Both branches
are described in ADR-0006 and ADR-0008; the seed migration is written only
after the answer.

### Catalog

| Collection | Key fields | Unique / invariants |
| --- | --- | --- |
| `categories` | slug, parent, title, sort, `attribute_schema` (json, phase 3) | slug |
| `tags` | slug, title | slug |
| `products` | seller, slug, kind (`site`,`template`,`design`,`extension`,`module`), category, tags, title, excerpt, description, status (`draft`,`in_review`,`published`,`unlisted`,`archived`), cover, gallery, demo_url, attributes (json), rating_avg, rating_count, sales_count, published_at | slug unique globally (one storefront); counters are cache, recomputed by Go cron, never used for money |
| `product_releases` | product, seller, version (semver), changelog, file or storage_key, size, sha256, status (`draft`,`published`,`yanked`), released_at | (product, version); file/sha256/size immutable once published |
| `license_types` | code (`regular`,`extended`), title, terms_version, terms, allows_end_product_sale, max_end_products | code |
| `offers` | product, seller, license_type, price_minor (VAT-inclusive), currency, vat_mode (`vat22`,`exempt_149_26`,`none`), support_months, active | (product, license_type) |

### Ordering and payments

| Collection | Key fields | Unique / invariants |
| --- | --- | --- |
| `orders` | number (`M-2026-000123`, storefront prefix), buyer (user, required), buyer_email (snapshot), status, currency, subtotal_minor, discount_minor, total_minor, vat_minor, idempotency_key, legal_offer_version, snapshot_digest, paid_at, cancelled_at, cancel_reason (`timeout_requested`,`provider_canceled`,`buyer`,`operator`) | number; idempotency_key; money invariants below |
| `order_items` | order, seller, product, offer, license_type, title_snapshot, unit_price_minor, unit_discount_minor (0 in phase 1), qty (1), line_minor, vat_mode, vat_minor, payment_subject (`intellectual_activity`), support_months, license_terms_version, currency, commission_minor, agent_info (json) | (order, offer) in phase 1; qty >= 1; never recomputed from live offers |
| `payments` | order, seller (single-seller order), provider, method (`card`,`sbp`,`sberpay`,`tpay`,`mirpay`,`wallet`), provider_payment_id, status, amount_minor, idempotency_key, confirmation_url, status_version, next_reconcile_at, raw (redacted) | idempotency_key; (provider, provider_payment_id) when set; at most one `succeeded` per order |
| `payment_events` (inbox) | provider, event_id, type, payload, verified, status (`received`,`processing`,`processed`,`ignored`,`dead`), locked_until, attempts, received_at, processed_at, error | (provider, event_id) |
| `refunds` | payment, seller, amount_minor, reason, idempotency_key, provider_refund_id, status (`pending`,`succeeded`,`failed`), requested_by | idempotency_key; (provider, provider_refund_id) when set; phase 1: amount = payment amount (full only) |
| `fiscal_receipts` | payment xor refund, seller, kind (`income`,`income_return`), provider, provider_receipt_id, status (`pending`,`registered`,`failed`,`not_required`), items (json, after discount allocation), fiscal_attrs (fn, fd, fpd), attempts, last_error, registered_at | (provider, provider_receipt_id) when set; at most one live `income` per payment, one `income_return` per refund. Single source of truth for receipt state (no `payments.receipt_status`) |

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
| `outbox` | topic, idempotency_key, payload, status (`pending`,`processing`,`done`,`dead`), locked_until, attempts, next_attempt_at, last_error | idempotency_key (`email:entitlement:{id}`, `fiscal:payment:{id}`, ...) |
| `audit_log` | actor, action, ref_type, ref_id, data, at | |

Indexes (in migrations): orders(buyer, status), payments(status,
next_reconcile_at), payment_events(status, locked_until),
outbox(status, next_attempt_at), entitlements(user), every `seller` column.

### Money invariants (ADR-0006)

- Prices are VAT-inclusive (`offers.price_minor` is what the buyer pays).
- `line_minor = (unit_price_minor - unit_discount_minor) * qty`.
- `subtotal_minor = sum(unit_price_minor * qty)`,
  `discount_minor = sum(unit_discount_minor * qty)` (= 0 in phase 1, non-zero
  rejected), `total_minor = subtotal_minor - discount_minor = sum(line_minor)`.
- `payments.amount_minor = orders.total_minor` at creation.
- `vat_minor` per line extracted from `line_minor` (vat22: 22/122, half up);
  `orders.vat_minor = sum(order_items.vat_minor)`; the receipt uses the same
  line values, so receipt total = payment amount.

### Content for the storefront

`pages`, `posts`, `menus`, `settings` follow the Codex v0.4 entry shape and
are projected to `content.Entry` for UI8Kit blocks (ADR-0010).

## State machines

Order:

```text
pending -> awaiting_payment -> paid -> fulfilled
                |   ^            |-> refunded            (full refund, phase 1)
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
  (purchase email, receipt registration retry, provider cancel/refund calls).

## Request paths

- Catalog SSR: handler -> catalog read -> view model (Codex records +
  dictionary) -> templ page. No writes.
- Checkout (`POST /checkout`, CSRF, signed-in user, required
  `Idempotency-Key`; use case in `internal/ordering`):
  1. one DB tx: order + items + payment `created`, keys stored; no network;
  2. `Gateway.CreatePayment` with the payment key;
  3. store `provider_payment_id`, `confirmation_url`, payment `pending`;
  4. redirect. A retry with the same key reuses the order: `awaiting_payment`
     -> repeat 2-3 (the provider returns the same payment); `paid` -> show
     the order; never a second order or payment.
- Return (`GET /checkout/return/{order}`): `FetchPayment` -> `Transition`
  (same path as the inbox worker); query parameters are ignored.
- Webhook (`POST /hooks/pay/{provider}`): verify -> one insert into
  `payment_events` -> 2xx; unique conflict = already accepted, 2xx.
- Inbox worker (one per process): claim `received` rows (-> `processing`,
  `locked_until`), `FetchPayment`, apply `Transition` and in-tx effects,
  mark `processed`; expired locks return to `received`; after N attempts ->
  `dead` + alert.
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
  4. receipts not `registered`/`not_required` -> `FetchReceipt`, then retry
     `RegisterReceipt` via outbox, up to the attempt cap, then page alert and
     operator task.
- Refund (operator command, phase 1 full only): row `refunds` `pending` ->
  `Gateway.Refund` with its key -> on confirmation one tx: payment/order
  `refunded`, ledger reversal, entitlement `refunded`, receipt
  `income_return` `pending` + outbox registration.
- Download: account link -> check entitlement `active` and release
  `published` -> 302 to `/dl/{token}` (HMAC, 10 min) -> `/dl/{token}`
  re-checks both -> stream file, log `downloads`.
- Shutdown: workers stop claiming on `OnTerminate`, finish or release their
  leases within the context deadline.
