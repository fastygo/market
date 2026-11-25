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

1. ANSWERED 2026-11-24 (owner t237u, see E13): GoDudY is a self-employed
   third-party seller under a pseudonym, so it is not the operator seller;
   the launch needs the minimal marketplace part. Sale scheme (agent or
   sublicense) needs counsel (E7).
2. Payment provider of the RU deployment (YooKassa, T-Bank, CloudPayments or
   Robokassa); later Stripe/PayPal for an EU deployment.
3. License types: one common non-exclusive license for now (owner t237u,
   2026-11-24); its code, terms text and prices are still open.
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
8. Refund policy (ADR-0024), for a practicing lawyer before the offer is
   published: does Art. 48 of the Union State Unified Rules (Decree No. 6
   of 2024-12-06) apply directly to file downloads (default: yes, 7 days
   not counting the purchase day, no "after download" exception); one
   refund term in the offer of at most 7 days; fix period offered on a
   defect; separate B2B contract and how a business buyer is identified;
   whether 289-FZ/290-FZ apply while only the operator sells.
9. ANSWERED 2026-11-24 (owner t237u): yes, accounts that abuse refunds are
   blocked from new purchases by the operator after review, per the terms
   of service, with an appeal path (ADR-0024). Still needs counsel: terms
   wording, Civil Code Art. 426, duration.
10. Goodwill refunds after the window: offer them at all, and the limit per
    account.
11. Retention of access evidence logs (default at least 5 years) and the
    exact list of fields (raw ip and user agent are personal data).
12. Watermark technique and cost: license file with an order id inside the
    archive, file metadata, or none for some product types; per-order
    copies need background processing and storage per order.
13. Time zone for "not counting the day of purchase" (default: storefront
    time zone, Europe/Moscow for RU).

## D. Before an EU go-live (values for the EU modules)

OSS registration, invoice numbering scheme, VAT id validation and reverse
charge, GDPR records and DPO need, Stripe/PayPal account setup.

## E. Marketplace (StorEEE vendor model, ADR-0020 to ADR-0022)

Defaults in the ADRs are placeholders for tests; each needs an owner answer
before phase 3.

1. ANSWERED 2026-11-24 (owner t237u): one flat commission of 50% for now
   (configurable default; tiers stay a seam).
2. ANSWERED 2026-11-24 (owner t237u): no exclusivity; one common
   non-exclusive license, sellers may sell anywhere (as on Envato after its
   recent update).
3. ANSWERED 2026-11-24 (owner t238u, as on Envato Market): no separate
   hold period or reserve by default; earnings stay on the seller balance
   until paid out, a month's earnings become payable on a fixed close day
   (default 8th), payout once the balance reaches the minimum; refunds and
   chargebacks after a payout make the balance negative and are netted
   against future earnings (ADR-0021). `hold_days` stays a seam at 0; the
   rolling reserve stays optional and off.
4. PARTLY ANSWERED 2026-11-24 (owner t237u): payouts are monthly. Still
   open: the final minimum amount per currency (placeholder 5000 RUB,
   owner-tunable; Envato uses USD 50), payout methods (bank transfer,
   card, wallet), and whether two operators must approve.
5. ANSWERED 2026-11-24 (owner t238u, as on Envato Market): the seller
   bears refunds, chargebacks, payment agent fees on them and unauthorised
   transactions on its items (deducted from the balance); payout fees
   default to the seller (config); the ordinary acquiring fee of a sale is
   covered by the platform's 50% author fee. Buyer fee and handling fee
   exist as config, default 0 (see E14).
6. ANSWERED 2026-11-24 (owner t238u, as on Envato Market): a refund by the
   seller or under the refund rules reverses the whole sale, commission
   included (buyer fee and handling fee reversed with a correction
   document); a goodwill refund at the platform's discretion is a platform
   expense and does not touch the seller balance.
7. PARTLY ANSWERED 2026-11-24 (owner t237u): all seller forms are accepted
   (LLC, sole proprietor, self-employed). Still open, needs counsel: agency
   (operator sells as agent, 54-FZ agent receipts, monthly agent reports)
   or sublicense (operator resells, royalties to authors); for
   self-employed sellers: who issues the receipt (seller in the tax service
   app or the operator as a tax service partner), the status check before
   approval and payouts, the annual income cap, and whether the agent
   scheme fits a self-employed principal.
8. KYC-lite: which seller data and documents are required, and how long
   they are kept.
9. Pricing: authors set prices freely within bounds, or the platform sets
   them? Price bounds per category? (One common license for now, so no
   extended/regular ratio.)
10. Moderation: review SLA targets, soft vs hard rejection policy, who
    reviews (operator staff only at start?).
11. Public author URL: `/authors/{slug}` (default) or another prefix.
12. Author levels and badges: thresholds and names; do levels change the
    commission?
13. ANSWERED 2026-11-24 (owner t237u): GoDudY is a self-employed seller
    under a pseudonym; the public profile shows only the pseudonym; passport
    data and tax id are disclosed only in the contract with the operator
    and stored only as private runtime data. Consequence: GoDudY is a
    third-party seller, so the launch needs the minimal marketplace part
    (ADR-0005 invariant, plan phase 1 step 11). Needs counsel: whether an
    agent receipt must show the supplier's real name and tax id, and what
    that means for the pseudonym.
14. Buyer fee (owner, after t238u): Envato adds a fixed buyer fee per
    category on top of the item price (list price = item price + buyer
    fee) and sometimes a handling fee per order. Both are modelled as
    config, default 0. Do we charge a buyer fee (and how much per
    category), and a handling fee per order? If yes, counsel confirms how
    it appears on the receipt and in the offer.

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
2. ANSWERED 2026-11-24 (owner t237u): StorEEE is the platform under its
   own brand; the platform default skin becomes `storeee` (initially a copy
   of the `godudy` tokens until the owner provides its own); `godudy` stays
   as a reference skin. Still open: StorEEE's own tokens and logo.
3. Production base URL (example `https://market.storeee.ru`) and whether
   the main domain gets a landing page that links to the market.
4. Status 2026-11-25 (vendor slice 1): name "StorEEE Market", short name
   "StorEEE" and the tagline are in the storefront brand (migration
   `1764000006`); the market runs on `https://market.storeee.ru` and the
   main domain has a landing page. Still open: alternate spellings for
   structured data and search synonyms, StorEEE's own tokens and logo,
   GoDudY's real profile text and avatar (placeholders now).

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
