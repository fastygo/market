# ADR-0015: Region packs and multi-currency, region-neutral core

- Status: Proposed (owner direction 2026-11-24)

## Context

The marketplace must be reusable: the first deployment sells in Russia, a
later one may sell in Europe with Stripe or PayPal. Tax, legal documents,
consent rules, legal identity formats and payment providers differ by
region. None of that may be hardcoded in the core.

## Decision

1. Core packages are region-neutral: `storefront`, `sellers`, `catalog`,
   `ordering`, `payments` (port only), `taxdocs` (DTO and state only),
   `licensing`, `ledger`, `trust`. They contain no rate, currency, country,
   legal form or provider constant.
2. Region rules come from a region pack, a module bundle (ADR-0017): the plugin `modules/region-<code>` plus
   the Go modules it needs, behind Go adapters that implement interfaces declared by the
   consuming core package (consumer-side, small):

   ```go
   // ordering
   type TaxPolicy interface {
       ResolveLine(ctx context.Context, in TaxInput) (TaxLine, error) // category, seller and operator tax profile, buyer country, date
   }
   // taxdocs
   type DocumentIssuer interface {
       Required(kind Kind, line Snapshot) bool
       Issue(ctx context.Context, doc Document) (Result, error)
       Fetch(ctx context.Context, externalID string) (Status, error)
   }
   // web, sellers
   type ConsentPolicy interface {
       Required(ctx context.Context, flow Flow) []DocumentRef // checkout, signup, seller onboarding
   }
   // storefront, sellers
   type LegalProfile interface {
       ValidateParty(p Party) []validation.Issue // legal_ids, address, tax profile
       Footer(op Operator) FooterData
   }
   ```

3. Packs:
   - `region-ru` (phase 1, ADR-0008): VAT 22% / registry exemption / none;
     54-FZ receipts via `InlineDocumentGateway`; 152-FZ consents; INN, OGRN,
     KPP validation.
   - `region-eu` (stub, phase 4+): VAT by buyer country through OSS with two
     pieces of country evidence (billing country, IP or card country),
     reverse charge for validated VAT ids (later, VIES); invoices and credit
     notes with sequential numbering per operator instead of fiscal receipts;
     GDPR consents, data export and erasure, cookie consent; VAT id and
     company number validation. Stripe and PayPal adapters.
   - A pack that does not need documents returns `Required = false`, so
     `tax_documents` rows are `not_required`; fiscal receipts are a region
     capability, not core.
4. Selection is configuration: `storefronts.region` and env
   `MARKET_REGION`; `cmd/market` wires the pack and the enabled payment
   adapters. One region per deployment (data residency differs); several
   storefronts of the same region may share a deployment.
5. Multi-currency:
   - money is `amount_minor int64` + ISO 4217 `currency`, exponent from a
     table; one currency per storefront and per order;
   - offers are unique per `(product, license_type, currency)`, so one
     catalog can be priced for several storefronts;
   - ledger accounts are balanced and summed per currency; no conversion,
     no exchange rates in core (a payout in another currency is a phase 3+
     question).
6. Operator and sellers are data: `operators` (legal entity of the
   storefront) and `sellers` with `legal_ids`, `tax_profile` json and
   `sale_model` (`principal` | `agent`). Both sale models are implemented
   from day one; the value for a seller is admin data.
7. Locale texts other than English (dictionary, legal pages) are content in
   the database or in a deployment content bundle, not code (ADR-0010).

## Consequences

- RU-specific decisions no longer block the core: GoDudY's sale model, the
  provider, tax registration and texts are seed/admin/env choices.
- An EU deployment = `region-eu` + Stripe/PayPal adapters + config; no core
  change. The EU pack is a stub until a deployment is planned.
- Conformance tests run the checkout/refund/reconciliation suite against a
  fake pack and fake gateway, and against each real pack's tables.

## Revision 2026-11-24 (owner: engine, ADR-0017)

Implementation of packs follows the module system: the interfaces above are
implemented by Go adapters in core that call a region plugin module
(`modules/region-ru`, `modules/region-eu`) for rules and mappings as data,
plus Go modules for providers and document issuing. `internal/region/*`
becomes `modules/region-*` (plugins) and provider adapters under
`internal/payments/*`. Commerce mode (single store or marketplace) is
orthogonal to the region (ADR-0017).
