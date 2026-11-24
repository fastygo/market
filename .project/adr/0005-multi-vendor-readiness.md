# ADR-0005: Seller-scoped rows from day one, one database

- Status: Proposed

## Context

Launch with one seller (GoDudY), later register others (HinddY). fastygo
backend isolates tenants by host with a database per tenant (its ADR-0011).
A marketplace is the opposite: one storefront, shared catalog, many sellers.

## Decision

- One database, row-level scoping by `seller` on products, releases, offers,
  order items, entitlements (via product), reviews, payouts, ledger accounts.
- GoDudY is a real `sellers` row from the first migration; there is no
  "no-seller" code path (same idea as fastygo's implicit default tenant).
- Access: PocketBase API rules plus Go checks through `platform/authz`:
  seller staff act only where `seller_members(seller, user)` exists.
  Capabilities: `catalog.edit_own`, `catalog.publish`, `orders.read_own`,
  `payouts.read_own`, `moderation.review`, `refunds.issue`.
- Seller portal routes are prefixed `/seller/{slug}`; operator uses PocketBase
  admin.
- Separate databases per seller: rejected (shared catalog, search, orders).

## Consequences

- Every query in seller context must filter by seller; enforced by repository
  methods that take `SellerScope`, and by tests that try cross-seller access.
- Onboarding HinddY is data plus a contract, not a migration.
