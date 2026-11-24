# ADR-0005: Seller-scoped rows from day one, one database, Go-only money writes

- Status: Proposed (revised after review 2026-11-24)

## Context

Launch with one seller (GoDudY), later register others (HinddY). fastygo
backend isolates tenants by host with a database per tenant (its ADR-0011).
A marketplace is the opposite: one storefront, shared catalog, many sellers.

## Decision

- One database, row-level scoping. `seller` is a column (not only a join) on
  products, releases, offers, order items, payments, refunds, tax documents,
  entitlements, ledger entries (nullable for platform accounts), reviews,
  payouts. Scoping never requires a join.
- GoDudY is a real `sellers` row from the first migration; there is no
  "no-seller" code path. Seeds create it as `draft` with placeholder legal
  data; `sale_model` and legal data are set in admin (ADR-0015, ADR-0016).
- Seller staff act only where `seller_members(seller, user)` exists; checks in
  PocketBase API rules and in Go through `platform/authz`. Capabilities:
  `catalog.edit_own`, `catalog.publish`, `orders.read_own`,
  `payouts.read_own`, `moderation.review`, `refunds.issue`.
- Money and fulfilment collections (`orders`, `order_items`, `payments`,
  `payment_events`, `refunds`, `tax_documents`, `ledger_entries`,
  `entitlements`, `downloads`, `outbox`, `payouts`, `audit_log`): API
  create/update/delete rules are `null`; only Go writes them. Buyers can list
  and view their own orders and entitlements; nothing else.
- Admin protection: Go hooks (not goja) reject update/delete of
  `ledger_entries` and any change to file, sha256 or size of a `published`
  or `yanked` release. Refund and entitlement revocation exist only as domain
  commands.
- Seller portal routes are prefixed `/seller/{slug}`; operator uses PocketBase
  admin.
- `products.slug` is globally unique: one storefront namespace.
- Separate databases per seller: rejected.

## Consequences

- Repository methods in seller context take `SellerScope`; tests try
  cross-seller access and buyer writes to money collections and expect denial.
- Onboarding HinddY is data plus a contract, not a migration.

## Review 2026-11-24 (BalunMY)

Applied P0-7 (Go-only money writes, admin hooks), P1-8 (storage-level
protection), P1-12 (seller on every money row), P2 (global slug).

## Revision 2026-11-24 (owner: region-neutral core)

Seller legal data is json validated by the region `LegalProfile`; the GoDudY
sale model is admin data, not a blocker.

## Revision 2026-11-24 (owner: commerce mode, ADR-0017)

The engine runs as `single_store` (implicit operator seller, principal only,
no onboarding or payouts) or `marketplace` (module enabled). The `seller`
column and scoping rules are identical in both modes.

## Review r2 2026-11-24 (BalunMY)

- Commerce mode and sale model are both admin data, with an enforced
  invariant: in `single_store` every seller is `principal`; a seller cannot
  be saved or activated as `agent` unless the storefront runs
  `marketplace` (Go hook + domain check, test in ADR-0013).
- Consequently, if the legal answer for GoDudY is "agent", the RU go-live
  cannot be `single_store`: the minimal marketplace part (agent sale model,
  agent tax documents, `seller_payable`) moves into phase 1 (plan go-live
  checklist).
- `marketplace` routes and modules are not registered in phase 1.
