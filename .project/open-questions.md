# Open questions

Reclassified 2026-11-24 after the owner direction "commerce engine,
region-neutral core, white label". Most former questions are now
configuration or content of a deployment.

## A. Affects the architecture now

Defaults are written into the ADRs; the owner only needs to confirm or
change them.

1. Russian dictionary location (ADR-0018): default is a `locale-ru` module
   whose strings live in the database (`ui_messages`), imported from a
   bundle outside this repo. Alternative: allow `modules/locale-ru/` with
   Cyrillic in this repo as the single exception to the no-Cyrillic rule, or
   a separate repo `fastygo/market-locale-ru`. Which one?
2. Deployment topology (ADR-0015): one region per deployment (data
   residency), several storefronts of one region per instance later. Confirm.
3. Region policy in goja plugins (ADR-0017): tax tables, consent and legal
   documents, receipt field mapping, checkout fields, locale formatting are
   goja plugins returning data validated by Go; money-critical code stays in
   Go modules. Confirm the split.

## B. Configuration or content (per deployment, later, not code)

1. GoDudY `sale_model` (`principal` if a brand of the operator, `agent` if a
   separate legal entity): seller data set in admin before go-live.
2. Payment provider of the RU deployment (YooKassa, T-Bank, CloudPayments or
   Robokassa); later Stripe/PayPal for an EU deployment.
3. License types (codes, terms, prices).
4. Refund policy text and window (phase 1 code supports full refunds).
5. Buyer sign-in mode: email OTP, password, or both (auth config).
6. Domain, hosting, brand skin (logo, tokens), storefront name.
7. Behaviour when every release of a product is yanked (default: "update
   pending" + seller alert; a config flag could trigger refunds).
8. Operator legal data, support contacts, legal pages: admin data.

## C. Legal and accounting before RU go-live (values for the RU modules)

1. Tax regime of the operator (VAT payer or not) and whether products are in
   the Russian software registry (exemption, Tax Code art. 149 p. 2 sub. 26).
2. Contract form for third-party sellers (agency or sublicense) and VAT on
   commission.
3. Self-employed (`npd`) sellers and their own receipts.
4. 289-FZ duties once third-party sellers join.
5. Return receipt on a lost card dispute.
6. Roskomnadzor operator notice; retention periods for orders, receipts,
   logs.

## D. Before an EU go-live (values for the EU modules)

OSS registration, invoice numbering scheme, VAT id validation and reverse
charge, GDPR records and DPO need, Stripe/PayPal account setup.
