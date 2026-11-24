# ADR-0008: RU region pack (54-FZ, VAT, 152-FZ, agent sales, 289-FZ)

- Status: Proposed (revised 2026-11-24: was "RF compliance"; now one region
  pack behind core interfaces, ADR-0015). Values need the RU accountant and
  lawyer before RU go-live, not before code.

## Context

The first deployment sells in Russia. Everything Russia-specific lives in
the `modules/region-ru` plugin module and RU payment Go modules; the core does not know it.

## Decision

### TaxPolicy (ru)

Maps `tax_category` + operator/seller `tax_profile` to a line tax:

| tax_category | Operator or seller is a VAT payer | Not a VAT payer (`usn` without VAT, `npd`) |
| --- | --- | --- |
| `standard` | VAT 22% inclusive (rate from 2026-01-01; table-driven by date) | `none` |
| `software_exempt` (registry software, Tax Code art. 149 p. 2 sub. 26) | `exempt` | `none` |

Rates are a dated table in the pack, so a rate change is a data change.
Invariant: a seller whose profile is not a VAT payer cannot offer
`standard` with VAT; `software_exempt` needs a registry reference in the
offer data.

### DocumentIssuer (ru): 54-FZ receipts

- Uses the provider's cloud cash register through `InlineDocumentGateway`
  (receipt sent with the payment; `IssueDocument` for retries and refunds).
- `tax_documents` kinds `receipt` and `receipt_return`; statuses `pending`,
  `issued`, `failed`, `not_required`.
- Line attributes: payment subject "intellectual activity result", payment
  method "full payment", VAT code from the line, buyer email.
- `agent` lines carry the agent sign and the supplier name, tax id and phone
  from the seller's legal snapshot (`order_items.agent_info`).
- A terminal failure is a legal incident (page alert, operator task), not an
  error-budget event.

### ConsentPolicy and data (152-FZ)

Consent documents with versions (terms, privacy, marketing, seller
contract); minimal data; IP and UA hashed; deletion on request except
records the law requires to keep. Deployment rule (ops checklist, not code):
database, backups and logs hosted in Russia.

### LegalProfile (ru)

Operator and seller `legal_ids` validated for INN (10 or 12 digits,
checksum), OGRN/OGRNIP, KPP; footer requisites rendered from operator data
entered in admin. No real values in the repo (ADR-0016).

### Sale models and 289-FZ

Both `principal` and `agent` work in code (ADR-0006). Legal points for RU
go-live: which model GoDudY uses; the agency contract; self-employed
(`npd`) sellers and their own receipts; 289-FZ duties (in force 2026-10-01)
once third-party sellers join: seller verification (EGRUL/EGRIP/ESIA),
contract terms, 45-day notice of term changes, seller cabinet, appeals.

### Payment adapters (ru)

YooKassa, T-Bank Internet Acquiring, CloudPayments, Robokassa; SBP and wallets
through the acquirer. Phase 1 wires one, chosen by config.

## Consequences

- The RU deployment is a configuration of the generic core plus this pack.
- Removing or replacing this pack touches no core package.

## Review 2026-11-24 (BalunMY)

Applied P0-5 (single document SoT, statuses, retries, page alert), P0-8 (both
sale models), P1-14 (tax profile vs VAT invariant, `npd` question).

## Revision 2026-11-24 (owner: engine, ADR-0017)

Packaging: the RU rules above (tax table, receipt field mapping, consent
documents, INN/OGRN/KPP rules, checkout fields) ship as the plugin module
`modules/region-ru`; 54-FZ issuing calls and the provider adapter are Go
modules. Russian texts are the `locale-ru` module (ADR-0018). 289-FZ duties
apply only in `marketplace` mode with third-party sellers.

## Review r2 2026-11-24 (BalunMY)

Phase 1 packaging: the RU rules are the Go module `internal/region/ru`
(dated VAT table, receipt field mapping, consent docs, INN/OGRN/KPP rules,
checkout fields) with table tests; the `modules/region-ru` plugin form comes
with the ADR-0017 later phase and must pass the same test vectors.
