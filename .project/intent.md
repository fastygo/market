# Intent

## What

A storefront for digital goods: sites, templates, designs, extensions, modules.
Buyers pay in RUB, get a license (purchase code) and download the files and
future updates of the product.

- Merchant of record: IT Garage LLC (Russian legal entity). Sales only in Russia.
- First and only seller at launch: GoDudY.
- Later: more sellers register and sell (example: HinddY), Envato Market style.

## Who

- Buyer: browses, pays (card, SBP, wallets via a Russian provider), downloads.
- Seller staff (GoDudY at launch): publishes products and releases.
- Operator (IT Garage): moderates, refunds, reports, payouts (later).

## Stack

PocketBase (Go, extend mode, SQLite) + goja hooks (PocketBase JSVM) + templ SSR,
UI by UI8Kit rules and contracts (`ui8kit/contract`).

## Non-goals (phase 1)

- No SPA. Server-rendered pages; small progressive enhancement only.
- No seller self-registration, payouts or agent receipts (phase 3).
- No foreign payment providers or currencies other than RUB.
- No separate BaaS service, no microservices, no Kubernetes.
- No product page builder; product fields are code-defined (migrations).

## Success of phase 1

A buyer finds a GoDudY product, pays by card or SBP, gets a fiscal receipt by
email, sees the purchase in the account and downloads the file, on production,
with the SLOs in `slo.md` measured.
