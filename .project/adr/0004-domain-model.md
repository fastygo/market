# ADR-0004: Domain model and data structures

- Status: Proposed (revised 2026-11-24: review r1, region-neutral core,
  review r2: data structures agreed for phase 1)

## Context

A digital goods marketplace needs products with versions, license types,
prices, orders, payments, tax documents, license grants and downloads, and must be
ready for many sellers. Full field list, uniques and indexes:
`../architecture.md`.

## Decision

Bounded contexts, one Go package each:

| Context | Aggregate root | Inside / references |
| --- | --- | --- |
| storefront | `Storefront` | operator (legal entity), region, currency, providers, legal docs |
| sellers | `Seller` | members, contract version, sale model, legal data (json) |
| catalog | `Product` | releases, offers (product x license type), category, tags |
| ordering | `Order` | items (price, discount, tax, commission, terms snapshots); checkout use case |
| payments | `Payment` | inbox events, refunds, disputes; `Gateway` port |
| taxdocs | `TaxDocument` | region-neutral document DTO (receipt, invoice, credit note), status machine; issued by the region `DocumentIssuer` |
| licensing | `Entitlement` | purchase code, downloads |
| ledger | `Txn` | balanced entries, deterministic txn ids |
| trust | `Review` | requires an entitlement (phase 2) |

Key choices:

0. Money source of truth: `ledger_entries` plus the `Transition` that wrote
   them. Amounts and statuses on `orders`, `order_items`, `payments` are
   snapshots written in the same transaction as the transition. There is no
   `payment_collections`, `order_transactions` or other rollup aggregate;
   "how much was paid or refunded" is a query over `payments`, `refunds` or
   the ledger, never a separately stored number.
1. Offer is the SKU: `(product, license_type, currency) -> price_minor,
   tax_category, support_months`. The price lives on the offer (no `prices`
   table); a second currency is a second offer. Rates are not stored on
   offers; the region `TaxPolicy` resolves them at checkout (batch
   `ResolveLines`) and the line snapshots the result in its `tax_*` columns
   (one tax per line; no `order_item_tax_lines` table).
2. Order items snapshot everything needed later: title, unit price, unit
   discount, line amount, tax category, rate, mode, code and amount,
   currency, seller, sale model, commission, `support_months`,
   `license_terms_version`, `product_slug`, `license_title`. Orders never read live offers
   after creation. `snapshot_digest` = Codex JCS sha256 of the snapshot.
3. Money invariants (ADR-0006): one currency per order; inclusive
   `total = subtotal - discount = sum(line)`, exclusive adds line tax;
   `order.tax = sum(item.tax)`, `payment.amount = order.total`; phase 1
   discount is 0.
4. Releases are immutable once published (yank, never edit; a new file is a
   new version). Entitlement grants the product, not one release.
5. `seller` is denormalized on order items, payments, refunds, tax documents,
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
9. Tax document DTO and pure builder belong to `taxdocs`; `ordering ->
   payments -> taxdocs`, `taxdocs` imports neither (no cycle). Checkout
   passes the built document to adapters that issue it inline.
10. Naming: `orders.terms_version` (buyer terms consent), not to be confused
    with the `offers` collection. Order number prefix comes from storefront
    config, never from a seller.
11. Sale model is per seller data: `principal` and `agent` are both
    implemented from day one (ledger posting, agent data in tax documents).
    The choice for a seller is admin data, not code.
12. Legal and tax identity of the operator and sellers is `legal_ids` and
    `tax_profile` json validated by the region `LegalProfile`; no
    country-specific columns (no `inn`, `ogrn`, `kpp` columns).

```go
type Money struct{ Minor int64; Currency string } // ISO 4217; exponent from the currency table
type Offer struct{ ID, ProductID, SellerID, LicenseType, TaxCategory string; Price Money; SupportMonths int; Active bool }
type TaxLine struct{ RateBP int; Mode TaxMode; Code string; Amount Money } // from TaxPolicy
type OrderItem struct {
    SellerID, ProductID, OfferID, LicenseType, Title, LicenseTermsVersion string
    UnitPrice, UnitDiscount, Line, Commission Money
    Qty, SupportMonths int
    Tax TaxLine
    SaleModel SaleModel
    Subject PaymentSubject
}
type Order struct{ ID, Number, StorefrontID, Region, BuyerID, BuyerEmail, IdempotencyKey, TermsVersion string; Status OrderStatus; Items []OrderItem; Subtotal, Discount, Tax, Total Money; StatusVersion int }
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

## Revision 2026-11-24 (owner: region-neutral core)

Removed RUB, VAT 22% and RU field names from the core model; added
`storefront` and operator data; tax as resolved line data; `fiscal`
renamed `taxdocs` (receipts, invoices); sale model as seller data with both
models implemented. No longer blocked on the GoDudY sale model (ADR-0015).

## Review r2 2026-11-24 (BalunMY)

Data structures for phase 1 agreed with the reviewer:

- P0-B: ledger + `Transition` are the single money SoT; Medusa-style
  `payment_collections` and `order_transactions` rejected.
- Mapping decisions (`../reference/medusa-mapping.md`): `prices` rejected
  until a consumer; `order_changes`/`order_summaries` rejected (order
  versioning only when post-payment order edits exist); tax lines as rows
  trimmed to a batch `ResolveLines` with columns on the line; `translations`
  rejected as a second contour (Codex per-locale records + `ui_messages`);
  line snapshot trimmed to `product_slug` and `license_title`; refund reason
  as a typed code + note, not a collection; storefront gets `countries`
  only.
- Not added in phase 1: `payment_collections`, `order_changes`,
  `order_change_actions`, `order_summaries`, `order_transactions`, `prices`,
  `product_storefronts`, `translations`, `order_item_tax_lines`,
  `refund_reasons`, `api_keys`. Business keys used for lookup or dedupe are
  real columns, never fields inside json.
