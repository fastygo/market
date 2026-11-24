# ADR-0006: Integer kopecks, RUB only, append-only ledger

- Status: Proposed (revised after review 2026-11-24; phase 1 posting scheme
  blocked on owner question #1 and the accountant's VAT answer)

## Decision

### Money

- `int64` minor units (kopecks) plus `currency = "RUB"`; same rule as Codex
  `RuleMoney`; max 2^53-1 (PocketBase number limit). No floats.
- Prices are VAT-inclusive. Per line:
  `line = (unit_price - unit_discount) * qty`;
  `vat = round_half_up(line * rate / (100 + rate))` with rate 22 for `vat22`,
  0 for `exempt_149_26` and `none`.
- Order: `subtotal = sum(unit_price * qty)`, `discount = sum(unit_discount *
  qty)`, `total = subtotal - discount = sum(line)`, `vat = sum(item.vat)`,
  `payment.amount = total`.
- Phase 1: discount is always 0; a non-zero discount is rejected. Phase 2
  coupons allocate the order discount to lines pro rata by line amount,
  rounding half up, the remainder kopeck to the last line, so
  `sum(line) = total` exactly and the receipt equals the payment.
- Rounding remainders never disappear: they go to the last line (VAT,
  discount) or to `platform_revenue` (commission split, phase 3).

### Ledger

- Append-only `ledger_entries`, unique `(txn_id, account)`, each `txn_id`
  balanced (sum 0), no update/delete (Go hook, ADR-0005).
- `txn_id` is deterministic: `pay:{payment_id}:succeeded`,
  `pay:{payment_id}:refund:{refund_id}`, `pay:{payment_id}:dispute:{n}`,
  `payout:{payout_id}`. A replay hits the unique index and is a no-op.
- Entries are written in the same DB transaction as the status transition
  (not via the outbox, ADR-0007).
- Balances are sums; no balance column.

### Phase 1 posting, two branches (decide after owner question #1)

Branch A, GoDudY is a brand of IT Garage (`principal`):

| Account | Payment succeeded | Refund |
| --- | --- | --- |
| `provider_clearing` | +total | -total |
| `platform_revenue` | -(total - vat) | reverse |
| `vat_payable` | -vat (only if OSN with VAT; omitted on USN/exemption) | reverse |

Branch B, GoDudY is a separate legal entity (`agent`), needed in phase 1:

| Account | Payment succeeded | Refund |
| --- | --- | --- |
| `provider_clearing` | +total | -total |
| `platform_revenue` (commission net of its VAT) | -(commission - vat_on_commission) | reverse |
| `vat_payable` (on commission only, if IT Garage pays VAT) | -vat_on_commission | reverse |
| `seller_payable` (seller = GoDudY) | -(total - commission) | reverse |

The separate `refunds` account is dropped: a refund is the reversal of the
same accounts.

### Commission (branch B and phase 3)

`commission = round_half_up(line * commission_bps / 10000)` per line after
discount; `seller_payable = line - commission`; the rounding remainder stays
with the platform. Test vectors include odd amounts such as 10001 kopecks at
1500 bps.

## Consequences

- One table answers payouts, reconciliation and accounting exports.
- The posting scheme is written only after both blocking answers.

## Review 2026-11-24 (BalunMY)

Applied P0-3 (deterministic `txn_id`, unique, in-tx), P0-6 (VAT-inclusive,
invariants, remainders), P0-8 (two branches, not decided), P1-9
(`vat_payable`, dropped `refunds` account), P1-13 (commission rounding).
