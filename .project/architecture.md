# Architecture

One Go binary: PocketBase core + our domain packages + templ SSR. SQLite (WAL)
on a server located in Russia. Files on local disk, later S3-compatible storage
in Russia.

```text
 browser ── HTTPS ── reverse proxy (Caddy) ── market binary
                                              ├─ web (templ SSR, handlers, view models -> Codex records)
                                              ├─ domain: catalog, ordering, payments, fiscal, licensing, ledger, sellers
                                              ├─ platform (ports + PocketBase adapters, tx, outbox, authz, money, ids)
                                              ├─ PocketBase core (collections, auth, files, admin UI, cron)
                                              └─ pb_hooks (goja: non-money glue only)
 payment provider ── webhook ──> /hooks/pay/{provider} -> inbox -> outbox worker -> domain
```

## Dependency rule

`web -> domain -> platform ports`; `platform/pb` implements the ports on
PocketBase. Domain packages never import PocketBase. Only `cmd/market` and
`internal/platform/pb` know `core.App`. This contains PocketBase upgrades
(pre-1.0, breaking changes per minor) and keeps the domain testable without IO.

## Domain model (PocketBase collections)

All ids are PocketBase ids (15 chars). Money is `int64` kopecks (`*_minor`),
currency always `RUB` (stored for future-proofing). Times are UTC. Seller-owned
rows carry `seller` from day one (ADR-0005).

### Identity and sellers

| Collection | Key fields |
| --- | --- |
| `users` (auth) | email, name, locale, `pd_consent_version`, `marketing_opt_in` |
| `sellers` | slug (unique), display_name, legal_name, inn, ogrn, kpp, tax_regime (`osn`,`usn`,`npd`), sale_model (`principal`,`agent`), commission_bps, status (`draft`,`pending_review`,`active`,`suspended`), contract_version |
| `seller_members` | seller, user, role (`owner`,`editor`,`support`); unique (seller, user) |
| `consents` | user or email, document (`offer`,`privacy`,`marketing`,`seller_contract`), version, accepted_at, ip_hash |

GoDudY is seeded as seller #1 with `sale_model = principal` (IT Garage sells
its own goods). Third-party sellers will be `agent` (ADR-0008).

### Catalog

| Collection | Key fields |
| --- | --- |
| `categories` | slug, parent, title, sort, `attribute_schema` (json, Codex RecordType, phase 3) |
| `tags` | slug, title |
| `products` | seller, slug (unique), kind (`site`,`template`,`design`,`extension`,`module`), category, tags, title, excerpt, description (markdown), status (`draft`,`in_review`,`published`,`unlisted`,`archived`), cover, gallery, demo_url, attributes (json), rating_avg, rating_count, sales_count, published_at |
| `product_releases` | product, seller, version (semver), changelog, file (protected) or storage_key, size, sha256, status (`draft`,`published`,`yanked`), released_at; unique (product, version) |
| `license_types` | code (`regular`,`extended`), title, terms_version, terms (markdown), allows_end_product_sale (bool), max_end_products |
| `offers` | product, seller, license_type, price_minor, currency, vat_mode (`vat22`,`exempt_149_26`,`none`), support_months, active; unique (product, license_type) |

An offer is the sellable SKU: product x license type -> price. Prices are
never computed from the product row.

### Ordering and payments

| Collection | Key fields |
| --- | --- |
| `orders` | number (`GD-2026-000123`), buyer, buyer_email, status, currency, subtotal_minor, discount_minor, total_minor, vat_minor, idempotency_key (unique), offer_version (consent), snapshot_digest (Codex JCS sha256), paid_at |
| `order_items` | order, seller, product, offer, license_type, title_snapshot, unit_price_minor, qty (1), vat_mode, vat_minor, payment_subject (`intellectual_activity`), commission_minor, agent_info (json, phase 3) |
| `payments` | order, provider (`yookassa`,`tbank`,`cloudpayments`,`robokassa`), method (`card`,`sbp`,`sberpay`,`tpay`,`mirpay`,`wallet`), provider_payment_id (unique per provider), status, amount_minor, idempotency_key, confirmation_url, receipt_status, raw (json, redacted) |
| `payment_events` | provider, event_id (unique per provider), type, payload (json), verified, received_at, processed_at, error |
| `refunds` | payment, amount_minor, reason, provider_refund_id, status, requested_by |
| `fiscal_receipts` | payment or refund, kind (`income`,`income_return`), provider, provider_receipt_id, status, items (json snapshot), fiscal_attrs (json: fn, fd, fpd), registered_at |

### Fulfilment and trust

| Collection | Key fields |
| --- | --- |
| `entitlements` | user, order_item (unique), product, license_type, purchase_code (UUIDv4, unique), status (`active`,`revoked`,`refunded`), support_until, granted_at |
| `downloads` | entitlement, release, at, ip_hash, ua_hash |
| `reviews` | product, user, entitlement (required), rating (1..5), body, status (`pending`,`published`,`hidden`), seller_reply; unique (product, user) |

### Money and operations

| Collection | Key fields |
| --- | --- |
| `ledger_entries` | txn_id, account (`provider_clearing`,`platform_revenue`,`seller_payable:{id}`,`vat_payable`,`refunds`), amount_minor (signed), currency, ref_type, ref_id, created; append-only, sum per txn_id = 0 |
| `payouts` (phase 3) | seller, period_from, period_to, amount_minor, status (`draft`,`approved`,`sent`,`failed`), agent_report (file), bank_ref |
| `outbox` | topic, payload (json), status (`pending`,`done`,`dead`), attempts, next_attempt_at, last_error |
| `audit_log` | actor, action, ref_type, ref_id, data (json), at |

### Content for the storefront

`pages`, `posts`, `menus`, `settings`: stored as PocketBase collections whose
fields follow the Codex v0.4 entry shape (id, locale, kind, status, slug,
title, content, excerpt, metadata). The web layer projects them to
`content.Entry` for UI8Kit blocks (ADR-0010).

## State machines

Order:

```text
pending -> awaiting_payment -> paid -> fulfilled
   |              |             |-> refunded | partially_refunded
   '-> cancelled  '-> cancelled (timeout 30 min or provider canceled)
```

Payment (normalized from every provider):

```text
created -> pending -> succeeded -> (refund) refunded | partially_refunded
               |-> waiting_for_capture -> succeeded | canceled
               '-> canceled | failed
```

Transitions live in Go as pure functions (`Transition(state, event) (state,
[]Effect, error)`), table-tested. Effects (grant entitlement, write ledger,
send email) go to the outbox in the same transaction as the state change.

## Request paths

- Catalog SSR: handler -> catalog query (read model) -> view model (Codex
  records + dictionary) -> templ page. No writes.
- Checkout: POST `/checkout` (CSRF, auth or email) -> create order + items +
  payment intent in one tx -> gateway `CreatePayment` with idempotency key and
  receipt -> redirect to `confirmation_url` (or SBP QR page).
- Webhook: POST `/hooks/pay/{provider}` -> verify -> insert `payment_events`
  (dedupe) -> 200 fast -> outbox worker fetches status from provider ->
  transition -> effects.
- Reconciliation cron (every 5 min): payments `pending` older than 10 min are
  re-fetched from the provider.
- Download: GET `/account/downloads/{entitlement}/{release}` -> check
  entitlement -> 302 to a signed URL valid 10 min -> stream file, log
  `downloads`.
