# Medusa v2 -> our PocketBase model

Reference: Medusa `v2.23.0` (`6369161`), summary in `medusa/` (MIT, see
`medusa/README.md`). Our model: `../architecture.md`, ADR-0004. Codex
(WordPress-shaped) stays the content contract (pages, posts, menus,
settings, taxonomies); Medusa is the reference for commerce structures and
contracts. Proposals were decided in review r2 (see the decision table at the end).

Verdicts: **same** (we already have it), **rename/align** (adopt Medusa
naming or shape), **add** (new collection or fields proposed), **later**
(phase 2+), **not needed** (physical goods or out of scope).

## Model mapping

| Medusa module.model | Ours | Verdict | Note |
| --- | --- | --- | --- |
| store.Store, StoreCurrency, StoreLocale | `storefronts` (currency, locales) | same | one currency per storefront now; Medusa allows several per store |
| sales-channel.SalesChannel (+ link product_sales_channel, publishable_api_key_sales_channel) | `storefronts` | add | add `product_storefronts` link so one catalog can be published per white-label storefront |
| region.region, RegionCountry (+ link region_payment_provider) | `storefronts.region`, `enabled_providers` | rename/align | add `countries` to the region config; providers per region = our enabled_providers |
| currency.currency | `platform/money` currency table | same | code, symbol, decimal digits as code data |
| translation.locale, translation | `ui_messages` (ADR-0018) | add | adopt `translations {reference, reference_id, locale_code, translations json}` for entity field translations (product title, license terms) in the DB; fits the locale-module and no-Cyrillic rules |
| product.Product | `products` | same | status draft/proposed/published/rejected ~ our draft/in_review/published; add `discountable`, `external_id` |
| product.ProductVariant, ProductOption, ProductOptionValue | `offers` (product x license_type) | rename/align | license type = option "License"; offer = variant. Keep our names, map for import/export |
| product.ProductCategory, ProductTag, ProductType, ProductCollection | `categories`, `tags` (Codex taxonomies) | same / later | `collections` (curated sets) later |
| product.ProductImage | `products.gallery` | same | |
| pricing.PriceSet, Price, PriceRule | `offers.price_minor` | add | `prices {offer, currency, amount_minor, min_qty, rules json, price_list}`; offer -> price set; enables several currencies/storefronts and customer-group prices |
| pricing.PriceList, PriceListRule | none | later | sales and overrides with dates (phase 2 promotions) |
| pricing.PricePreference (is_tax_inclusive per region/currency) | `storefronts.tax_display` | rename/align | keep per storefront; store the flag on every line as Medusa does (`is_tax_inclusive`) |
| cart.Cart, LineItem, LineItemAdjustment, LineItemTaxLine, CreditLine | none (buy-now creates the order) | later | phase 2 cart; adopt the same line shape as order lines |
| cart.Address, ShippingMethod* | none | not needed | digital goods; a billing country is the only address data we need |
| order.Order | `orders` | rename/align | add `version`, `display_id` (we have `number`), `locale`, `sales_channel` (= storefront), `is_draft_order` later |
| order.OrderItem (versioned quantities) + OrderLineItem (snapshot) | `order_items` | rename/align | split is useful only with order edits; for us keep one table but add `version` and the snapshot fields below |
| order.OrderLineItemTaxLine | `order_items.tax_*` (one per line) | add | `order_item_tax_lines {item, code, rate, provider_id, tax_rate_id, description}`; several per line (combinable taxes) |
| order.OrderLineItemAdjustment | `order_items.unit_discount_minor` | add (phase 2) | `order_item_adjustments {item, amount, code, promotion_id, is_tax_inclusive}` |
| order.OrderChange, OrderChangeAction | none | add | post-purchase changes (refund, revoke, license upgrade) as requested/confirmed changes with actions, `order.version` bump |
| order.OrderSummary (versioned totals json) | `orders` totals columns | add | `order_summaries {order, version, totals json}` for audit of totals per version |
| order.OrderTransaction | `ledger_entries` + `payments` | rename/align | order-level money movements (payment +, refund -) per version; our ledger is stricter (double entry); keep both: transactions for order balance, ledger for accounting |
| order.OrderCreditLine | none | later | store credit |
| order.Return, ReturnItem, ReturnReason, OrderClaim*, OrderExchange* | `refunds` | not needed / later | no physical return; `refund_reasons` worth adopting |
| order.OrderShipping* | none | not needed | |
| payment.PaymentCollection | none (payment per order) | add | `payment_collections {order, currency, amount, authorized_amount, captured_amount, refunded_amount, status}`; status enum below |
| payment.PaymentSession | `payments` (status created/pending) | rename/align | our payment row before success = session; adopt statuses `pending`, `requires_more` (3DS, SBP QR), `authorized`, `captured`, `canceled`, `error` |
| payment.Payment, Capture | `payments` (succeeded) | rename/align | immediate capture: one capture per payment; keep `captured_at`, `canceled_at` |
| payment.Refund, RefundReason | `refunds` | same + add | add `refund_reasons`, `note`, `created_by` |
| payment.PaymentProvider | `enabled_providers` config | same | |
| payment.AccountHolder (+ link customer_account_holder) | none | later | saved cards / provider customer ids |
| tax.TaxRegion, TaxRate, TaxRateRule, TaxProvider | region plugin `tax.rules` (ADR-0017) | rename/align | make plugin output and an optional admin override table follow TaxRegion (country, province, parent) + TaxRate (code, rate, is_default, is_combinable) + TaxRateRule (reference, reference_id) |
| promotion.Promotion, ApplicationMethod, PromotionRule(+Value), Campaign, CampaignBudget(+Usage) | none | later | phase 2 coupons: copy the shape; our discount allocation = `allocation: across` |
| customer.Customer, CustomerGroup, CustomerAddress | `users` | rename/align | keep PocketBase auth users as customers; add `customer_groups` later for B2B pricing |
| auth.* (auth_identity, provider_identity, mfa, reset tokens) | PocketBase auth | not needed | PocketBase covers it |
| user.User, Invite | PocketBase superusers + `seller_members` | same | |
| api-key.ApiKey (publishable / secret) | none | later | headless storefronts and partner APIs (purchase-code verification) |
| inventory.*, stock-location.*, fulfillment.* | none | not needed | digital delivery = `entitlements` + `downloads` |
| file.* | PocketBase files | same | |
| notification.notification, notificationProvider | `outbox` email effects | rename/align | add a `notifications` log (to, channel, template, status, external_id) later |
| settings.*, index.*, search.*, analytics, caching, locking, workflow-engine-* | n/a | not needed | locking: we use leases and SQLite single writer |
| audit.audit_log | `audit_log` | same | |
| none | `sellers`, `seller_members`, `payouts`, `ledger_entries` | ours | Medusa marketplace is a recipe; we keep native seller columns |
| none | `product_releases`, `license_types`, `entitlements`, `downloads`, `reviews` | ours | Medusa has no license model (digital products are a recipe) |
| none | `tax_documents`, `payment_events` (inbox), `outbox` | ours | fiscal documents and inbox are not in Medusa core |

## Contracts worth copying

1. **Payment session lifecycle.** `initiatePayment -> (updatePayment) ->
   authorizePayment -> capturePayment`, plus `cancelPayment`, `refundPayment`,
   `retrievePayment`, `getPaymentStatus`, `deletePayment`,
   `getWebhookActionAndData`. Webhook actions: `authorized`, `captured`,
   `failed`, `pending`, `requires_more`, `canceled`, `not_supported`, `pending_authorization`. Our
   `Gateway` maps as: CreatePayment = initiate (+ authorize and capture in one
   step), FetchPayment = getPaymentStatus/retrievePayment, CancelPayment,
   Refund, ParseWebhook = getWebhookActionAndData. Decision r2: the enum may
   be used inside adapters only; no `requires_more` status (deferred).
2. **Payment collection rollups.** `authorized_amount`, `captured_amount`,
   `refunded_amount` and collection status (`not_paid`, `awaiting`,
   `authorized`, `partially_authorized`, `partially_captured`, `completed`,
   `canceled`, `failed`) give one place to answer "is this order paid".
3. **Order versioning.** `order.version`; items, summaries and transactions
   carry the version they belong to; post-purchase changes are
   `OrderChange` (requested/confirmed/declined/canceled) with ordered
   `OrderChangeAction` rows (`action`, `details`, `amount`, `applied`). Fits
   refunds, disputes and license upgrades without mutating history.
4. **Price sets.** A sellable thing points to a price set; prices are rows
   with currency, amount, quantity range and rules (region, customer group);
   price lists add sales and overrides with time windows; a price
   preference says whether prices are tax-inclusive per region or currency.
5. **Tax lines.** Taxes are separate lines per item (`code`, `rate`,
   `provider_id`, `tax_rate_id`), several per item when combinable; the tax
   provider contract is batch: `getTaxLines(itemLines, shippingLines,
   context)`.
6. **Sales channels and regions.** Channel = where a product is sold
   (catalog visibility, scoped API keys); region = currency + countries +
   tax settings + payment providers. Our storefront merges both; keep it
   merged but add the product-storefront link.
7. **Promotions.** Rule-based promotions with application method (`fixed` or
   `percentage`, target `order` or `items`, allocation `each` / `across` /
   `once`), campaigns with budgets and usage, adjustments stored per line.
8. **Cart completion workflow.** `complete-cart`: acquire a lock on the
   cart, validate items, payments and tax country, authorize the payment
   session, create the order, add an order transaction, register promotion
   usage, release the lock; idempotent for the same cart; compensation
   cancels or refunds the payment if order creation fails
   (`compensatePaymentIfNeededStep`). Related: `refund-payment-workflow`,
   `capture-payment-workflow`, `process-payment-workflow` (webhooks),
   `cancel-order`, `create-order-change`.
9. **Line snapshot fields.** `product_title`, `product_handle`,
   `variant_sku`, `variant_title`, `variant_option_values`, `unit_price`,
   `compare_at_unit_price`, `is_tax_inclusive`, `is_discountable`,
   `is_custom_price`.

## Gaps in our ADR-0004 model: proposals and decisions (review r2)

The 11 proposals below were reviewed by BalunMY (review r2, 2026-11-24).
Decisions are applied in ADR-0004/0006/0007/0015 and `../architecture.md`;
nothing here is carried into the model "as Medusa has it". Rule: one SQLite
writer and `ledger_entries` + `Transition` as the single money source of
truth; no table without a phase 1 consumer.

| # | Proposal | Decision | Applied as / reason |
| --- | --- | --- | --- |
| 1 | `payment_collections` (authorized/captured/refunded rollups, status) above `payments` | reject | Third status machine and a second money SoT (P0-B). "Is it paid" = `Transition` + at most one `succeeded` payment; totals are queries over `payments`/`refunds`/ledger. `payments` rows are the provider attempts |
| 2 | Medusa webhook action enum and `requires_more` status | defer | Adapters may normalize events internally; payment status unchanged (3DS / SBP QR = `pending` + `confirmation_url`). A new status only when a different timeout or reconciliation rule needs it |
| 3 | `orders.version`, `order_changes`, `order_change_actions`, `order_summaries`, `order_transactions` | reject | Second money SoT next to the ledger. `status_version` covers conditional updates; refunds and disputes are `refunds` rows and status machines. Order versioning only when post-payment order edits (license upgrade) exist |
| 4 | `order_item_tax_lines` rows; batch `TaxPolicy` | trim | Accept batch `ResolveLines(items, ctx)` (ADR-0015); reject the child table: one tax per line, `tax_*` columns on `order_items` |
| 5 | Tax data shaped as TaxRegion/TaxRate/TaxRateRule + admin override | defer | Dated rate table in `internal/region/ru` (ADR-0008) is enough; an admin rate editor has no consumer |
| 6 | `prices` table instead of `offers.price_minor` | reject now, defer to a consumer | One currency per storefront, one price per offer; unique `(product, license_type, currency)` is the seam; price lists / quantity tiers / customer groups in phase 2+ when needed |
| 7 | Storefront as sales channel: `product_storefronts`, publishable keys, `countries` | trim | Accept `storefronts.countries`; reject the link table and publishable keys (one storefront per instance, no headless API in phase 1) |
| 8 | Generic `translations` collection | reject (defer) | Would diverge from Codex per-locale records and `ui_messages` (ADR-0018). Phase 1 catalog is entered in admin in the deployment language |
| 9 | Line snapshot alignment + `refund_reasons` | trim | Add `product_slug`, `license_title` to `order_items`; skip `compare_at_unit_price`, `is_discountable`, `is_custom_price` (no discounts in phase 1); refund reason is a typed `reason_code` + `note` on `refunds`, not a collection; `requested_by` already exists |
| 10 | Checkout compensation + per-order lock | trim (lock rejected) | Reconciliation branch: provider `succeeded`, order not completable -> `refund_pending` -> automatic full refund through the operator refund path (ADR-0007). No per-order lock: single writer + `WHERE status_version = n` |
| 11 | Copy Promotion / Campaign / ApplicationMethod for phase 2 | defer | Coupons are phase 2; shape decided when real promotion rules exist; discount allocation already in ADR-0006 |

Not added in phase 1: `payment_collections`, `order_changes`,
`order_change_actions`, `order_summaries`, `order_transactions`, `prices`,
`product_storefronts`, `translations`, `order_item_tax_lines`,
`refund_reasons`, `api_keys`.

Not adopted: Medusa's separate auth/customer identities (PocketBase auth
covers it), inventory, fulfillment, shipping, returns, claims and exchanges
(digital goods), and its module-link tables where a denormalized column is
simpler in one SQLite database.
