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
3. OWNER DECISION. Region policy as goja plugins (ADR-0017). New default
   after review r2 (P0-C): phase 0-1 ships region policy (tax table, receipt
   field mapping, consent and legal documents, legal id rules, checkout
   fields) as the Go module `internal/region/ru` behind the same
   `TaxPolicy`/`DocumentIssuer`/`ConsentPolicy`/`LegalProfile` interfaces;
   the region stays a module selected by config. The goja policy plugin
   runtime is built when it is triggered: a second live region, a need to
   change policy without a release, or your explicit decision; the RU Go
   tests become its contract vectors. Alternative: build the plugin runtime
   in phase 0 as before (about +1 to 2 weeks, and the reviewer's risk: a
   plugin error or timeout on the checkout path means a wrong receipt or a
   failed payment). Confirm the new default or ask for the plugin runtime
   now.

## B. Configuration or content (per deployment, later, not code)

1. GoDudY `sale_model` (`principal` if a brand of the operator, `agent` if a
   separate legal entity): seller data set in admin before go-live. `agent`
   is incompatible with a `single_store` launch (admin invariant); if the
   answer is `agent`, the minimal marketplace part ships in phase 1 first.
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

## Review r2 2026-11-24 (BalunMY)

A3 default changed: Go region policy in phase 0-1, goja policy plugins at
their trigger; flagged as an owner decision. B1 notes that `agent` cannot
launch in `single_store`.
