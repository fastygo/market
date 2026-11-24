# Open questions

Owner (business):

1. Is GoDudY a brand of IT Garage LLC (principal sale) or a separate legal
   entity (then it is already an agent sale and needs a contract)?
2. First payment provider: YooKassa, T-Bank Internet Acquiring,
   CloudPayments or Robokassa? Recommendation: one provider with built-in
   cloud receipts (54-FZ) and SBP in phase 1; second in phase 2.
3. License types: Envato-like `regular` and `extended`, or own set? Terms text
   and prices per type.
4. Refund policy for digital goods (window, conditions).
5. Russian UI text: the repo has no Cyrillic. Keep the `ru` dictionary and
   legal texts in the database (`settings`/`pages`, editable in admin), or
   allow `ui/dict/ru.json` as a content-file exception?
6. Buyer accounts: email OTP only, or password too? Guest checkout by email?
7. Domain name and hosting provider in Russia.

Legal and tax (needs an accountant or lawyer):

1. Tax regime of IT Garage LLC (OSN with VAT 22% or USN) and whether products
   are in the Russian software registry (VAT exemption, Tax Code art. 149
   p. 2 sub. 26). Templates and designs are likely not "programs" for the
   exemption; vat_mode is per offer for this reason.
2. Contract form for third-party sellers: agency (commission) or
   sublicense/resale; receipts and VAT follow from it.
3. Whether the platform falls under 289-FZ (platform economy, in force
   2026-10-01) once third-party sellers join, and which duties apply below
   the registry thresholds.
4. Personal data: operator notice to Roskomnadzor, cross-border transfer (none
   planned), retention periods for orders and logs.
