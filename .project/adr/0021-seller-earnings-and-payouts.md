# ADR-0021: Commission, seller earnings ledger, hold period and payouts

- Status: Proposed (owner direction t229u/t230u, 2026-11-24; docs only).
  Numbers (rates, days, thresholds) are owner decisions in
  `open-questions.md` E; the defaults below are placeholders for tests.

## Context

ADR-0006 already posts `agent` lines with `platform_revenue`, `tax_payable`
and `seller_payable`, with deterministic `txn_id`s and append-only entries.
A marketplace also needs: commission rules that depend on author level,
exclusivity and category; earnings that are not payable until refund and
dispute risk has passed; balances that can go negative after a chargeback;
payout batches; and attribution of refunds and disputes to the seller. The
host is 1 vCPU / 2 GB with SQLite (one writer), so every job must be
batched in short transactions (ADR-0007 rule zero).

## Decision

### Price and commission ownership

- The seller sets the price per offer (product x license type x currency)
  within bounds from `price_rules` (category, license_type, currency,
  min_minor, max_minor; optional extended/regular ratio). Changes apply to
  new orders only (order items are snapshots) and are rate limited
  (config, default once per 7 days per offer) to block price games around
  featuring.
- The platform sets commission. `commission_rules` (priority order: seller
  override, category + exclusivity, level + exclusivity, default) gives
  `commission_bps` for a line, valid by date. The resolved bps and amount
  are snapshotted on `order_items` (`commission_bps`, `commission_minor`)
  at checkout, so later rule changes never touch past sales.
- Exclusivity: `sellers.exclusivity` (`exclusive` sells only here, lower
  commission; `non_exclusive` may sell elsewhere, higher commission), with
  an optional per-item override (`products.exclusive`) if the owner wants
  Envato's per-item mode.
- Commission base is the line amount the buyer pays after discount
  (ADR-0006 formula, half up, remainder to the platform). Buyer fees are
  not used (an open question if the owner wants them).

### Accounts and posting (extends ADR-0006)

Seller share is the same for both sale models; only tax documents and who
is the tax payer differ (region pack):

| Account | Sale (`pay:{id}:succeeded`) | Notes |
| --- | --- | --- |
| `provider_clearing` | +line total | as ADR-0006 |
| `platform_revenue` | -(commission - commission tax) | for the operator seller: -(line - tax) |
| `tax_payable` | -tax due by the operator | commission tax (agent) or line tax (principal) |
| `seller_pending` (seller) | -(line - commission) | held earnings |
| `affiliate_pending` (partner) | see ADR-0023 | only with the affiliate module |

- Hold release: `release:{order_item_id}` moves the seller share from
  `seller_pending` to `seller_available` when `hold_until` (snapshot on the
  order item: `paid_at + hold_days`) has passed and the entitlement is not
  `disputed`. A nightly cron selects due items by index and writes up to N
  release transactions per short write transaction.
- Payout: `payout:{payout_id}` moves `seller_available` -> `payout_clearing`
  when the payout is approved; `payout:{payout_id}:paid` moves
  `payout_clearing` -> `bank` (or provider account) on confirmation;
  `payout:{payout_id}:failed` reverses the first txn.
- `seller_payable` of ADR-0006 is renamed to `seller_pending` /
  `seller_available` when the marketplace module is built (code change,
  phase 3a; the posting tests change with it).
- Balances stay derived: `available = -sum(seller_available)`,
  `pending = -sum(seller_pending)` per seller and currency, computed with
  the `(seller, currency, created)` index; the seller portal caches the
  result per seller for 60 s in memory. A `ledger_balances` projection is
  added only if measurements need it (ADR-0006 r2).

### Refunds and disputes attributed to the seller

- Refund (full, ADR-0007): the sale txn is reversed line by line. If the
  share is still in `seller_pending`, it is reversed there; if it was
  released, the reversal hits `seller_available` (it may go negative). The
  commission is reversed too (the platform returns its fee) unless the
  owner chooses a non-refundable fee (open question).
- Dispute opened: the order item's release is blocked (no release while
  `disputed`); a released share is not clawed back until the dispute is
  lost. Dispute lost: reversal as a refund, plus the provider's chargeback
  fee as `dispute:{payment_id}:{n}:fee` charged to the platform or to the
  seller per config (`dispute_fee_payer`, open question).
- Negative balance: allowed; it is netted against future earnings at the
  next release, never paid out; a configurable threshold raises an alert
  and may suspend payouts or the seller (operator decision, audited).
- Optional rolling reserve (`reserve_bps`, `reserve_days`, default 0) for
  sellers with high dispute rates, posted to `seller_reserve` with its own
  release txn.

### Payouts

- Schedule and threshold are storefront config (default monthly, minimum
  amount per currency). A payout batch is created by the operator or a
  cron: one `payouts` row per seller with `available >= minimum`, amount =
  available at batch time (from the ledger, in the same short tx that posts
  `payout:{id}`).
- States: `draft` -> `approved` -> `processing` -> `paid` | `failed`;
  `draft` -> `canceled`. Approval needs `payouts.approve`; optional
  two-person rule (creator != approver) as config.
- Execution is a port `PayoutGateway` (`CreatePayout`, `FetchPayout`),
  called outside transactions with the payout id as idempotency key. Phase
  3 ships a manual adapter (export file for the bank, then the operator
  confirms each payout with a bank reference); bank or provider APIs are
  later Go modules.
- Payout details (bank account, card token, wallet id) are seller runtime
  data in `payout_methods`, encrypted at rest with a key from env, masked in
  every view and log, never in the repo (ADR-0016).
- Region documents: the RU pack produces the monthly agent report per
  seller (`seller_reports`, kind from the region, period, totals from the
  ledger, file) and declares whether a payout needs a seller document (for
  example a receipt of a self-employed seller) before `paid`. Tax
  withholding for individuals is a region pack question, not core.

## Collections (new or changed)

| Collection | Fields | Unique / invariants |
| --- | --- | --- |
| `order_items` (changed) | + commission_bps, hold_until, earnings_minor (seller share snapshot) | `earnings_minor = line_minor - commission_minor` for non-operator sellers, 0 for the operator |
| `commission_rules` | scope (`default`,`level`,`category`,`seller`), level, category, seller, exclusivity, bps (0..10000), valid_from, valid_to, note | no overlapping validity for the same scope key (Go check) |
| `price_rules` | category, license_type, currency, min_minor, max_minor, extended_min_ratio_bp | (category, license_type, currency) |
| `payout_batches` | period_from, period_to, currency, status (`draft`,`approved`,`processing`,`done`), created_by, approved_by, totals (json, cache) | (period_from, period_to, currency) |
| `payouts` (changed, ADR-0005) | seller, batch, amount_minor, currency, method_snapshot (masked json), status (`draft`,`approved`,`processing`,`paid`,`failed`,`canceled`), idempotency_key, provider_ref, failure_reason, seller_document, approved_by, paid_at | (seller, batch); idempotency_key; amount from the ledger; Go-only writes |
| `payout_methods` | seller, kind, details (encrypted json), masked, status (`unverified`,`verified`,`disabled`), is_default | one default per seller and currency |
| `seller_reports` | seller, kind (region code), period_from, period_to, totals (json), file, status (`draft`,`issued`,`accepted`) | (seller, kind, period_from) |

New ledger accounts: `seller_pending`, `seller_available`, `seller_reserve`,
`payout_clearing`, `bank`, `dispute_fees`; deterministic txn ids:
`release:{order_item}`, `reserve:{order_item}`, `payout:{id}`,
`payout:{id}:paid`, `payout:{id}:failed`, `dispute:{payment}:{n}:fee`.

## Consequences

- Every number a seller sees (pending, available, paid) is a ledger query
  with a written rule; nothing is a stored balance.
- Single store: only the operator seller sells, `earnings_minor = 0`, no
  release, payout or report jobs are registered; ADR-0006 principal posting
  is unchanged.
- Resource fit: release and payout jobs touch only due rows through indexes
  `order_items(hold_until)` and `(seller, currency, created)`, in batches of
  a few hundred per transaction, nightly or monthly; no per-request ledger
  scans.
