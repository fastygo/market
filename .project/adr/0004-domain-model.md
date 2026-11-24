# ADR-0004: Domain model and data structures

- Status: Proposed (main input for the architecture review)

## Context

A digital goods marketplace needs products with versions, license types,
prices, orders, payments, receipts, license grants and downloads, and must be
ready for many sellers. Full field list: `../architecture.md`.

## Decision

Bounded contexts, one Go package each, with these aggregates:

| Context | Aggregate root | Inside / references |
| --- | --- | --- |
| sellers | `Seller` | members, contract version, sale model |
| catalog | `Product` | releases, offers (product x license type), category, tags |
| ordering | `Order` | items (price, VAT, commission snapshots) |
| payments | `Payment` | provider events, refunds |
| fiscal | `Receipt` | snapshot of items sent to the provider |
| licensing | `Entitlement` | purchase code, downloads |
| ledger | `Txn` | balanced entries |
| trust | `Review` | requires an entitlement |

Key choices:

1. Offer is the SKU: `(product, license_type) -> price_minor, vat_mode`.
2. Order items snapshot everything needed later (title, price, VAT mode,
   seller, commission); orders never read live prices after creation.
   `snapshot_digest` = Codex JCS sha256 of the snapshot.
3. Releases are immutable once published (yank, never edit the file).
   Entitlement grants the product, not one release: buyers get updates.
4. Every seller-owned row has `seller`; `order_items.seller` makes split
   orders possible without schema change.
5. Payment status is normalized to our enum; provider raw payload is kept
   redacted for audit.
6. Webhooks land in `payment_events` (unique provider + event_id) before any
   processing.
7. Go types are plain structs with methods for invariants; enums are typed
   strings with `Valid()`; state transitions are pure functions.

```go
type Money struct{ Minor int64; Currency string } // RUB only
type Offer struct{ ID, ProductID, SellerID, LicenseType string; Price Money; VAT VATMode; SupportMonths int; Active bool }
type OrderItem struct{ SellerID, ProductID, OfferID, LicenseType, Title string; UnitPrice Money; Qty int; VAT VATMode; VATAmount, Commission Money; Subject PaymentSubject }
type Order struct{ ID, Number, BuyerID, BuyerEmail string; Status OrderStatus; Items []OrderItem; Total Money; IdempotencyKey string }
type Payment struct{ ID, OrderID string; Provider ProviderCode; Method Method; ProviderID string; Status PaymentStatus; Amount Money }
type Entitlement struct{ ID, UserID, OrderItemID, ProductID, LicenseType, PurchaseCode string; Status EntitlementStatus; SupportUntil time.Time }
```

## Consequences

- More tables than a toy shop, but no migration is needed for multi-seller,
  cart, partial refunds or a second provider.
- Snapshots duplicate data on purpose (legal and audit need).
