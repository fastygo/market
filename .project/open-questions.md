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
3. DECIDED (owner t223u, 2026-11-24). Region policy as goja plugins
   (ADR-0017). Decision, as proposed after review r2 (P0-C): phase 0-1 ships region policy (tax table, receipt
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

1. GoDudY `sale_model` (see also E13; GoDudY is now a seller of the
   StorEEE Market storefront) (`principal` if a brand of the operator, `agent` if a
   separate legal entity): seller data set in admin before go-live. `agent`
   is incompatible with a `single_store` launch (admin invariant); if the
   answer is `agent`, the minimal marketplace part ships in phase 1 first.
2. Payment provider of the RU deployment (YooKassa, T-Bank, CloudPayments or
   Robokassa); later Stripe/PayPal for an EU deployment.
3. License types (codes, terms, prices).
4. Refund policy text and window (phase 1 code supports full refunds).
5. Buyer sign-in mode: email OTP, password, or both (auth config).
6. Domain, hosting, brand skin (logo, tokens), storefront name (StorEEE
   Market, see G).
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
5. Return receipt on a lost card dispute (current default: issued, same as a
   refund; a policy flag can turn it off).
6. Roskomnadzor operator notice; retention periods for orders, receipts,
   logs.
7. 54-FZ payment subject and payment method codes for digital goods (code
   placeholders: subject `service`, method `full_payment`).

## D. Before an EU go-live (values for the EU modules)

OSS registration, invoice numbering scheme, VAT id validation and reverse
charge, GDPR records and DPO need, Stripe/PayPal account setup.

## E. Marketplace (StorEEE vendor model, ADR-0020 to ADR-0022)

Defaults in the ADRs are placeholders for tests; each needs an owner answer
before phase 3.

1. Commission: default rate, and whether it depends on author level,
   category and exclusivity (Envato style tiers) or is one flat rate.
2. Exclusivity: offer exclusive and non-exclusive authors with different
   rates? Per author or per item?
3. Hold period in days before earnings become payable (trade-off: refund
   window and card chargeback risk vs. author cash flow); optional rolling
   reserve for risky sellers.
4. Payout schedule (monthly or on request), minimum amount, payout methods
   (bank transfer, card, wallet), and whether two operators must approve.
5. Who pays the acquiring fee and the chargeback fee: platform out of its
   commission, or the seller.
6. On a refund, does the platform return its commission together with the
   sale (default) or keep it as a non-refundable fee?
7. Legal scheme for third-party sellers: agency (operator sells as agent,
   54-FZ agent receipts, monthly agent reports) or sublicense (operator
   resells, royalties to authors). Which seller forms are accepted at start
   (LLC, sole proprietor, self-employed, private individual) and, for
   self-employed, who issues the receipt.
8. KYC-lite: which seller data and documents are required, and how long
   they are kept.
9. Pricing: authors set prices freely within bounds, or the platform sets
   them? Price bounds per category; extended license price at least N times
   regular?
10. Moderation: review SLA targets, soft vs hard rejection policy, who
    reviews (operator staff only at start?).
11. Public author URL: `/authors/{slug}` (default) or another prefix.
12. Author levels and badges: thresholds and names; do levels change the
    commission?
13. GoDudY: a brand of the operator (operator seller, `principal`) or a
    separate entity selling as `agent`? (replaces B1 for the new brand
    setup).

## F. Affiliate program (ADR-0023, seam only)

1. Attribution policy: first click or last click, and the cookie window in
   days.
2. Level rates for levels 1, 2 and 3.
3. Who funds affiliate commissions: platform share only (default), or may a
   seller add a contribution?
4. Hold period for affiliate earnings (default: same as sellers).
5. Legal review of a three-level program in Russia (multi-level and
   pyramid-like structure risks, advertising rules, taxes and reporting for
   payouts to private individuals). No conclusion is drawn in the docs.

## G. Brand (StorEEE Market)

1. Exact brand strings for config: name ("StorEEE Market"), short name,
   tagline ("Easy to find, Easy to learn, Easy to Sell"), alternate
   spellings to list in structured data and search synonyms.
2. Skin: is the current `godudy` skin the StorEEE Market skin (rename to
   `storeee`), or should GoDudY keep it for its seller pages only and
   StorEEE get its own tokens?
3. Production base URL (example `https://market.storeee.ru`) and whether
   the main domain gets a landing page that links to the market.

## Review r2 2026-11-24 (BalunMY)

A3 default changed: Go region policy in phase 0-1, goja policy plugins at
their trigger; flagged as an owner decision. B1 notes that `agent` cannot
launch in `single_store`.

## Decisions 2026-11-24 (owner t223u)

- A1 stays at the default: Russian dictionary in the database
  (`ui_messages`), imported from a bundle outside this repo.
- A3 decided: phase 1 RU rules in Go behind the module interfaces; goja
  policy plugins later, at the triggers written in ADR-0017.
- Product shape is open; product types are modules with a plain default
  page (ADR-0019), so this is not a blocker.
