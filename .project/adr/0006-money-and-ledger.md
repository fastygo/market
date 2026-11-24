# ADR-0006: Minor units with ISO 4217 currency, tax as data, append-only ledger

- Status: Proposed (revised 2026-11-24: review, then region-neutral core;
  no longer blocked)

## Decision

### Money

- `int64` minor units plus an ISO 4217 `currency`; the exponent comes from a
  currency table in `platform/money` (2 for RUB, EUR, USD; 0 for JPY). Same
  rule as Codex `RuleMoney`; max 2^53-1 (PocketBase number limit). No floats.
- One currency per storefront and per order (config). Adding a currency
  needs no code: offers in that currency plus a storefront that sells it.
  Operations across currencies are an error (`money.Add` checks).
- No currency conversion in the core.

### Tax as data

- Offers carry a `tax_category`, not a rate. At checkout the region
  `TaxPolicy` (ADR-0015) returns per line `rate_bp`, `mode`
  (`inclusive`,`exclusive`,`exempt`,`reverse_charge`,`none`) and a document
  `code`; the line snapshots them.
- Per line: `line = (unit_price - unit_discount) * qty`;
  inclusive `tax = round_half_up(line * rate_bp / (10000 + rate_bp))`;
  exclusive `tax = round_half_up(line * rate_bp / 10000)`.
- Order: `subtotal = sum(unit_price * qty)`, `discount = sum(unit_discount *
  qty)`, `tax = sum(line tax)`; inclusive `total = sum(line)`, exclusive
  `total = sum(line + tax)`; `payment.amount = total`.
- Phase 1: discount is always 0; a non-zero discount is rejected. Phase 2
  coupons allocate the order discount to lines pro rata, half up, remainder
  to the last line, so the tax document total equals the payment.
- Remainders never disappear: last line (tax, discount) or
  `platform_revenue` (commission split).

### Ledger

- Append-only `ledger_entries` with `currency`, unique `(txn_id, account)`,
  each `txn_id` balanced per currency (sum 0), no update/delete (Go hook).
- `txn_id` is deterministic: `pay:{payment_id}:succeeded`,
  `pay:{payment_id}:refund:{refund_id}`, `pay:{payment_id}:dispute:{n}`,
  `payout:{payout_id}`. A replay hits the unique index and is a no-op.
- Entries are written in the same DB transaction as the status transition.
- Balances are sums per account and currency; no balance column.

### Posting rules per line, both sale models from day one

`principal` (the operator sells as its own goods):

| Account | Payment succeeded | Refund |
| --- | --- | --- |
| `provider_clearing` | +line total | reverse |
| `platform_revenue` | -(line total - tax) | reverse |
| `tax_payable` | -tax (only if the line has tax) | reverse |

`agent` (the operator sells on behalf of the seller for a commission):

| Account | Payment succeeded | Refund |
| --- | --- | --- |
| `provider_clearing` | +line total | reverse |
| `platform_revenue` | -(commission - commission tax) | reverse |
| `tax_payable` | -commission tax (if the operator is tax registered; rate from `TaxPolicy`) | reverse |
| `seller_payable` (with `seller`) | -(line total - commission) | reverse |

`commission = round_half_up(line_after_discount * commission_bps / 10000)`;
the remainder stays with the platform. Test vectors include odd amounts such
as 10001 minor units at 1500 bps, and a zero-exponent currency.

Which model a seller uses is seller data (ADR-0015); GoDudY's value is set in
admin before go-live. Tax registration of the operator is operator data.

## Consequences

- One table answers payouts, reconciliation and accounting exports, per
  currency.
- No region constants in the ledger or in money math.

## Review 2026-11-24 (BalunMY)

Applied P0-3 (deterministic `txn_id`, unique, in-tx), P0-6 (invariants,
remainders), P0-8 (both sale models), P1-9 (`tax_payable`, no `refunds`
account), P1-13 (commission rounding).

## Revision 2026-11-24 (owner: region-neutral core)

RUB-only and VAT 22% removed from core; currency per order; rates from the
region `TaxPolicy`; `vat_payable` renamed `tax_payable`; both sale models are
implemented, so the posting scheme is no longer blocked.

## Revision 2026-11-24 (owner: commerce mode, ADR-0017)

In `single_store` mode every line is `principal`, so only the principal
posting rule runs; `agent` postings, commission and `seller_payable` are
active only with the `marketplace` module.
