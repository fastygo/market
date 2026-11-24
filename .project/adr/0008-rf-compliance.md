# ADR-0008: RF compliance: 54-FZ, 152-FZ, VAT modes, 289-FZ later

- Status: Proposed (needs legal confirmation, see `../open-questions.md`)

## Decision

- 54-FZ receipts: use the provider's cloud cash register service (receipt data
  sent with the payment); no own cash register in phase 1. Item attributes:
  payment subject "intellectual activity result", payment method "full
  prepayment"/"full payment", VAT code from `vat_mode`, buyer email.
  Refunds produce an `income_return` receipt. Status tracked in
  `fiscal_receipts`.
- VAT: `vat_mode` per offer: `vat22` (standard rate from 2026-01-01),
  `exempt_149_26` (software in the Russian registry), `none` (seller not a
  VAT payer). Final values set by the accountant.
- Agent sales (phase 3): receipts carry agent sign and supplier name and INN;
  commission is IT Garage income; seller amount is payable, not revenue.
- 152-FZ: production database, backups and logs hosted in Russia; explicit
  consent records (`consents`) with document versions; minimal data (email,
  name); IP and UA stored hashed; privacy policy page; data deletion on request
  except records the law requires to keep (orders, receipts).
- 289-FZ (platform economy, in force 2026-10-01): a store selling only its own
  goods is not an intermediary platform. Before the first third-party seller:
  legal review; seller verification (EGRUL/EGRIP/ESIA), contract with the
  required terms, 45-day notice of term changes, seller cabinet and appeals.
- Consumer law: public offer, seller requisites in the footer, refund policy.

## Consequences

- Phase 1 needs no fiscal hardware and no agent logic.
- Choosing a provider without cloud receipts adds a separate OFD/cash service.
