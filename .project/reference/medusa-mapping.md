# Medusa v2 -> our PocketBase model

Reference: Medusa `v2.23.0` (`6369161`), summary in `medusa/` (MIT, see
`medusa/README.md`). Our model: `../architecture.md`, ADR-0004. Codex
(WordPress-shaped) stays the content contract (pages, posts, menus,
settings, taxonomies); Medusa is the reference for commerce structures and
contracts. This page proposes changes; the ADRs are not rewritten yet.

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
   Refund, ParseWebhook = getWebhookActionAndData. Adopt the action enum as our
   normalized webhook event type, and `requires_more` as a status.
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

## Gaps in our ADR-0004 model and proposed changes

Proposals only; to be applied in an ADR revision after review.

1. **Payment collection per order** (`payment_collections` with
   authorized/captured/refunded rollups and status) above our `payments`
   rows; `payments` keep the provider attempt (= session). Clarifies
   partial states, second provider and disputes.
2. **Normalized webhook action enum and `requires_more` status** from
   Medusa `PaymentActions`/`PaymentSessionStatus` instead of our ad hoc
   event types; SBP QR and 3DS map to `requires_more`.
3. **Order versioning and order changes**: `orders.version`,
   `order_changes` + `order_change_actions` for refunds, disputes, revokes
   and license upgrades; `order_summaries` (totals per version);
   `order_transactions` (order balance) next to the double-entry ledger.
4. **Tax lines as rows**: `order_item_tax_lines` (code, rate_bp,
   provider_id, tax_rate_id) instead of one tax column set per line; make
   `TaxPolicy` batch (`ResolveLines(items, context)`) like `getTaxLines`.
5. **Tax data shape**: region plugin output and an optional admin override
   table follow TaxRegion/TaxRate/TaxRateRule (country, province, code,
   rate, default, combinable, rules by product or category).
6. **Price sets**: `prices` rows per offer (currency, amount, quantity
   range, rules) instead of `offers.price_minor`; keeps multi-currency and
   multi-storefront pricing without new offers; price lists in phase 2.
7. **Storefront as sales channel**: `product_storefronts` link and
   publishable API keys per storefront for headless clients; storefront
   gets `countries` (region) explicitly.
8. **Entity translations**: `translations` collection
   (reference, reference_id, locale_code, translations json) for product and
   license texts; answers where Russian catalog text lives without Cyrillic
   in the repo.
9. **Line snapshot alignment**: add `product_handle`, `variant_title`
   (license title), `compare_at_unit_price_minor`, `is_tax_inclusive`,
   `is_discountable` to `order_items`; add `refund_reasons`, refund `note`
   and `created_by`.
10. **Checkout compensation and lock**: state in ADR-0007 that a failure
    after provider success but before the order is `paid` triggers the same
    recovery (reconciliation) and, if the order cannot be fulfilled, an
    automatic cancel or refund, as Medusa's `compensatePaymentIfNeededStep`;
    a per-order lock (lease) around completion.
11. **Promotions shape for phase 2**: copy Promotion / ApplicationMethod /
    PromotionRule / Campaign and per-line adjustments instead of a custom
    coupon design.

Not adopted: Medusa's separate auth/customer identities (PocketBase auth
covers it), inventory, fulfillment, shipping, returns, claims and exchanges
(digital goods), and its module-link tables where a denormalized column is
simpler in one SQLite database.
