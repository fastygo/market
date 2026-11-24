# ADR-0006: Integer kopecks, RUB only, append-only ledger

- Status: Proposed

## Decision

- Money is `int64` minor units (kopecks) plus `currency = "RUB"`; same rule as
  Codex `RuleMoney`; max 2^53-1 (PocketBase number limit). No floats anywhere.
- VAT is computed per item from `vat_mode` with integer math, rounding half up
  to the kopeck, and stored on the item (`vat_minor`).
- Ledger: append-only `ledger_entries`, each `txn_id` balanced (sum 0).
  Phase 1 writes on payment success: `provider_clearing` debit,
  `platform_revenue` credit (principal model). Refund writes the reverse.
  Phase 3 adds `seller_payable:{id}` and commission split.
- Balances are sums over entries; no mutable balance column.

## Consequences

- Payout and reconciliation reports come from one table.
- Slight overhead now; avoids a money migration when sellers join.
