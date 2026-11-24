# ADR-0004: Domain model and data structures

- Status: Proposed (revised after review 2026-11-24; sale-model parts blocked
  on owner question #1)

## Context

A digital goods marketplace needs products with versions, license types,
prices, orders, payments, receipts, license grants and downloads, and must be
ready for many sellers. Full field list, uniques and indexes:
`../architecture.md`.

## Decision

Bounded contexts, one Go package each:

| Context | Aggregate root | Inside / references |
| --- | --- | --- |
| sellers | `Seller` | members, contract version, sale model |
| catalog | `Product` | releases, offers (product x license type), category, tags |
| ordering | `Order` | items (price, discount, VAT, commission, terms snapshots); checkout use case |
| payments | `Payment` | inbox events, refunds, disputes; `Gateway` port |
| fiscal | `Receipt` | receipt DTO and builder, status machine |
| licensing | `Entitlement` | purchase code, downloads |
| ledger | `Txn` | balanced entries, deterministic txn ids |
| trust | `Review` | requires an entitlement (phase 2) |

Key choices:

1. Offer is the SKU: `(product, license_type) -> price_minor (VAT-inclusive),
   vat_mode, support_months`.
2. Order items snapshot everything needed later: title, unit price, unit
   discount, line amount, VAT mode and amount, currency, seller, commission,
   `support_months`, `license_terms_version`. Orders never read live offers
   after creation. `snapshot_digest` = Codex JCS sha256 of the snapshot.
3. Money invariants (ADR-0006): `total = subtotal - discount = sum(line)`,
   `order.vat = sum(item.vat)`, `payment.amount = order.total`; phase 1
   discount is 0.
4. Releases are immutable once published (yank, never edit; a new file is a
   new version). Entitlement grants the product, not one release.
5. `seller` is denormalized on order items, payments, refunds, receipts,
   entitlements, ledger entries, reviews (ADR-0005).
6. Buyers are always users (no guest checkout in phase 1); `buyer_email` is a
   snapshot, not an identity.
7. Statuses change only through pure `Transition` functions applied with a
   conditional update (`status_version`). Uniques that make races safe:
   `orders.number`, `orders.idempotency_key`, `payments.idempotency_key`,
   `(provider, provider_payment_id)`, `(provider, event_id)`,
   `(provider, provider_refund_id)`, `(txn_id, account)`,
   `entitlements.order_item`, `purchase_code`, `outbox.idempotency_key`.
8. One payment row per provider attempt; a retry with the same key reuses the
   row (no `payment_attempts` table). A second provider (phase 2) is a new row;
   at most one `succeeded` payment per order.
9. Receipt DTO and pure builder belong to `fiscal`; `ordering -> payments ->
   fiscal`, `fiscal` imports neither (no cycle). Checkout passes the built
   receipt inside `CreatePaymentRequest`.
10. Naming: `orders.legal_offer_version` (public offer consent), not to be
    confused with the `offers` collection. Order numbers use a storefront
    prefix (`M-`), not a seller prefix.

```go
type Money struct{ Minor int64; Currency string } // RUB only
type Offer struct{ ID, ProductID, SellerID, LicenseType string; Price Money /* VAT-inclusive */; VAT VATMode; SupportMonths int; Active bool }
type OrderItem struct {
    SellerID, ProductID, OfferID, LicenseType, Title, LicenseTermsVersion string
    UnitPrice, UnitDiscount, Line, VATAmount, Commission Money
    Qty, SupportMonths int
    VAT VATMode
    Subject PaymentSubject
}
type Order struct{ ID, Number, BuyerID, BuyerEmail, IdempotencyKey, LegalOfferVersion string; Status OrderStatus; Items []OrderItem; Subtotal, Discount, Total, VAT Money; StatusVersion int }
type Payment struct{ ID, OrderID, SellerID, IdempotencyKey, ProviderID string; Provider ProviderCode; Method Method; Status PaymentStatus; Amount Money; StatusVersion int }
type Entitlement struct{ ID, UserID, SellerID, OrderItemID, ProductID, LicenseType, PurchaseCode string; Status EntitlementStatus; SupportMonths int; SupportUntil time.Time }
```

## Consequences

- No migration is needed for multi-seller, cart, a second provider or
  disputes. Partial refunds stay closed until a rule exists (ADR-0009).
- Snapshots duplicate data on purpose (legal and audit need).

## Review 2026-11-24 (BalunMY)

Applied P0-6 (money invariants), P0-8 (sale model blocked), P1-3 (no guest),
P1-5 (fuller snapshot), P1-7 (uniques, status_version), P1-12 (seller
denormalized), P2 (`legal_offer_version`, storefront order number, trust
context), package notes (receipt DTO in `fiscal`, checkout in `ordering`).
