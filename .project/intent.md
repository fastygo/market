# Intent

## What

A commerce engine for digital goods (Medusa-like, white-label) plus a
reference storefront (first deployment: StorEEE Market, brand config). It runs as a single store or as a
multi-vendor marketplace (commerce mode is config plus the `marketplace`
module). Region-neutral core for digital goods: sites, templates,
designs, extensions, modules. Buyers pay, get a license (purchase code) and
download the files and future updates of the product.

- One codebase, many deployments. Each deployment configures its storefront,
  operator (legal entity), currency, region pack and payment providers as
  data and environment, never in code.
- First deployment: Russia, operator IT Garage LLC (legal details entered in
  admin), storefront brand StorEEE Market, first seller GoDudY (a vendor
  with its own profile and portfolio), RU region pack, one RU payment
  provider.
- Later: more sellers (example: HinddY), Envato Market style vendor model
  (ADR-0020 to ADR-0022); a separate affiliate partner site (ADR-0023);
  supplier feed import as a future module; other regions
  (example: an EU deployment with Stripe or PayPal and the EU region pack).

## Who

- Buyer: browses, pays with the methods of the configured provider, downloads.
- Seller staff: publishes products and releases.
- Operator: the legal entity running the storefront; moderates, refunds,
  reports, payouts (later).

## Stack

PocketBase (Go, extend mode, SQLite) + goja hooks (PocketBase JSVM) + templ SSR,
UI by UI8Kit rules and contracts (`ui8kit/contract`).

## Non-goals (phase 1)

- No SPA. Server-rendered pages; small progressive enhancement only.
- No seller self-registration or payouts (phase 3). Both sale models
  (`principal`, `agent`) exist in the model from day one.
- No region-specific rule, legal entity data, personal data or secret in the
  code, seeds or docs (ADR-0016).
- One currency per storefront; no currency conversion.
- No separate BaaS service, no microservices, no Kubernetes.
- No product page builder; product fields are code-defined (migrations).

## Success of phase 1

On the RU deployment a buyer finds a product, pays, gets the tax document the
region requires (fiscal receipt in RU) by email, sees the purchase in the
account and downloads the file, on production, with the SLOs in `slo.md`
measured. The region interfaces are satisfied at compile time by a fake
region in tests (no shipped EU pack before an EU deployment is planned).
