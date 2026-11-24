# Open questions

Owner (business):

1. BLOCKER for payments: is GoDudY a brand of IT Garage LLC (branch A,
   principal sale) or a separate legal entity (branch B, agent sale with
   agent receipts and `seller_payable` already in phase 1)? See ADR-0006 and
   ADR-0008.
2. First payment provider: YooKassa, T-Bank Internet Acquiring,
   CloudPayments or Robokassa? It must support cloud receipts (54-FZ),
   receipt retry, cancel, refund and SBP.
3. License types: Envato-like `regular` and `extended`, or own set? Terms text
   and prices per type.
4. Refund policy for digital goods (window, conditions). Phase 1 supports
   full refunds only.
5. Russian UI text: the repo has no Cyrillic. Keep the `ru` dictionary and
   legal texts in the database (`settings`/`pages`, editable in admin), or
   allow `ui/dict/ru.json` as a content-file exception?
6. Buyer sign-in: email OTP only, or password too? (Guest checkout is not
   planned for phase 1: purchases bind to a user.)
7. Domain name and hosting provider in Russia.
8. All releases of a product yanked: is "no download, update pending" plus a
   seller alert acceptable, or should it trigger refunds?

Legal and tax (accountant or lawyer):

1. BLOCKER for the ledger: tax regime of IT Garage LLC (OSN with VAT 22% or
   USN) and whether products are in the Russian software registry (VAT
   exemption, Tax Code art. 149 p. 2 sub. 26). Templates and designs are
   likely not "programs" for the exemption; `vat_mode` is per offer.
2. Contract form for third-party sellers: agency (commission) or
   sublicense/resale; VAT on commission.
3. Self-employed (`npd`) sellers: how their own receipts combine with an
   agent receipt.
4. Whether the platform falls under 289-FZ in branch B or once third-party
   sellers join, and which duties apply below the registry thresholds.
5. Chargebacks: is a return receipt (`income_return`) required when a card
   dispute is lost?
6. Personal data: operator notice to Roskomnadzor, retention periods for
   orders, receipts and logs.
