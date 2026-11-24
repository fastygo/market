# ADR-0008: RF compliance: 54-FZ, 152-FZ, VAT modes, agent sales, 289-FZ

- Status: Proposed (revised after review 2026-11-24; needs owner and legal
  answers, see `../open-questions.md`)

## Decision

### 54-FZ receipts

- Phase 1 uses the provider's cloud cash register (receipt payload sent with
  the payment; `RegisterReceipt` for retries and refunds). No own register.
- `fiscal_receipts` is the single source of truth for receipt state:
  `pending`, `registered`, `failed`, `not_required`; same set for
  `income_return`. `payments.receipt_status` does not exist.
- Item attributes: payment subject "intellectual activity result", payment
  method "full payment", VAT code from `vat_mode`, line amounts after
  discount allocation (receipt total = payment amount), buyer email.
- Retry only via the outbox and reconciliation with an attempt cap; then a
  page alert and an operator task. A terminal receipt failure is a legal
  incident (page), not an error-budget event.

### Sale model (blocked on owner question #1)

- Branch A, `principal`: IT Garage sells GoDudY goods as its own; plain
  receipts; revenue posting (ADR-0006 branch A).
- Branch B, `agent`: GoDudY is a separate legal entity; already in phase 1
  receipts carry the agent sign and supplier name, INN and phone; IT Garage
  income is the commission; seller amount is `seller_payable`
  (ADR-0006 branch B). Agency contract needed before launch.
- No code for the payment path is written before the answer.

### VAT

`vat_mode` per offer: `vat22` (standard rate from 2026-01-01), `exempt_149_26`
(software in the Russian registry), `none` (seller not a VAT payer).
Invariant checked on save: a seller or merchant that is not a VAT payer
(`usn` without VAT, `npd`) cannot have `vat22` offers; `exempt_149_26` needs a
registry entry reference. Final values set by the accountant.

### Self-employed sellers (`npd`)

Self-employed sellers issue their own receipts through the tax service app;
how this combines with an agent receipt is a legal question. No `npd` seller
is onboarded before it is answered.

### 152-FZ

Production database, backups and logs hosted in Russia; consent records with
document versions; minimal data (email, name); IP and UA hashed; privacy
policy page; deletion on request except records the law requires to keep
(orders, receipts).

### 289-FZ (platform economy, in force 2026-10-01)

A store selling only its own goods is not an intermediary platform (branch A).
Branch B and any third-party seller need legal review first: seller
verification (EGRUL/EGRIP/ESIA), a contract with the required terms, 45-day
notice of term changes, a seller cabinet and an appeal process.

### Consumer law

Public offer, seller requisites in the footer, refund policy.

## Consequences

- Branch A launches without agent logic; branch B moves agent receipts and
  `seller_payable` into phase 1.

## Review 2026-11-24 (BalunMY)

Applied P0-5 (single receipt SoT, statuses, retries, page alert), P0-8 (two
branches), P1-14 (tax regime vs VAT invariant, `npd` question).
