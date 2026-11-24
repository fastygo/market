# ADR-0021: Commission, seller earnings ledger, monthly earnings close and payouts

- Status: Proposed (owner direction t229u/t230u, 2026-11-24; docs only).
  Numbers (rates, days, thresholds) are owner decisions in
  `open-questions.md` E; the defaults below are placeholders for tests.
- Owner direction t238u (2026-11-24): money rules follow Envato Market.
  Sources: Envato Market Author Terms, last revised 2026-07-01
  (help.author.envato.com/hc/en-us/articles/41371538488473, clauses 13,
  14, 16, 17), "Introduction to Earnings" (.../articles/360000472943),
  "Market Earnings FAQs" (.../articles/360000534606) and the glossary
  (.../articles/360000569746). Our numbers stay config; Envato values are
  cited as reference only.

## Context

ADR-0006 already posts `agent` lines with `platform_revenue`, `tax_payable`
and `seller_payable`, with deterministic `txn_id`s and append-only entries.
A marketplace also needs: commission rules that depend on author level,
category; earnings held on the seller balance until a monthly payout; balances that can go negative after a chargeback;
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
- The platform sets commission. Owner decision t237u (2026-11-24): one flat
  rate, 50% (5000 bps), as the configurable `default` rule. The
  `commission_rules` table keeps the seam for tiers later (priority order:
  seller override, category, level, default) and gives
  `commission_bps` for a line, valid by date. The resolved bps and amount
  are snapshotted on `order_items` (`commission_bps`, `commission_minor`)
  at checkout, so later rule changes never touch past sales.
- Exclusivity: none (owner decision t237u, 2026-11-24, following
  Envato's recent move to one non-exclusive model): every seller may sell
  the same items anywhere, and commission does not depend on exclusivity.
  The `exclusivity` fields proposed earlier are removed, not reserved
  (YAGNI); a split would come back as a `commission_rules` scope column
  with a migration.
- Commission base is the item price: the line amount after discount,
  without buyer fee and handling fee (ADR-0006 formula, half up, remainder
  to the platform). Envato reference: the author fee is always 50% of the
  item price, and the earnings formula deducts nothing else from the
  author for a normal sale. So the ordinary acquiring fee of a successful
  payment is a platform cost paid out of its commission; only the
  deductions listed under "Refunds, disputes and deductions" reach the
  seller.

### Buyer fee and handling fee (optional, off by default)

Envato reference: list price = item price + buyer fee; the buyer fee is
Envato's fee to the customer, fixed per category where authors set
prices; some orders also carry a per-order handling fee. We model both as
optional storefront config, both default 0 (feature off), and the owner
decides whether to use them (`open-questions.md` E14):

- `buyer_fees` table: category, currency, amount_minor, valid_from,
  valid_to. Resolved at checkout and snapshotted on the order item as
  `buyer_fee_minor`; the shown price is `item price + buyer fee` (list
  price), with the fee itemised on the order and in documents.
- `handling_fee_minor` per order and currency (storefront config),
  snapshotted on the order; 0 means no fee line.
- Both fees belong to the platform in full: they are posted to
  `platform_revenue` (with the operator's tax per region pack), never to
  the seller, and never enter the commission base or the affiliate base.
- Tax and fiscal treatment of the fees (separate receipt line, VAT, agent
  vs. own service) comes from the region pack and needs counsel before
  the owner turns them on.

### Accounts and posting (extends ADR-0006)

Seller share is the same for both sale models; only tax documents and who
is the tax payer differ (region pack):

| Account | Sale (`pay:{id}:succeeded`) | Notes |
| --- | --- | --- |
| `provider_clearing` | +line total | as ADR-0006 |
| `platform_revenue` | -(commission - commission tax) | for the operator seller: -(line - tax) |
| `tax_payable` | -tax due by the operator | commission tax (agent) or line tax (principal) |
| `seller_pending` (seller) | -(line - commission) | earnings of the open month |
| `platform_revenue` (fees) | -(buyer fee + handling fee - their tax) | only when the fees are on (default 0) |
| `affiliate_pending` (partner) | see ADR-0023 | only with the affiliate module; paid from the platform share |

- No separate hold period and no reserve by default (owner t238u,
  following Envato clause 16a: earnings are held on the seller balance
  until they are paid out). `hold_days` stays as a config seam with
  default 0; `hold_until` = `paid_at + hold_days` is still snapshotted.
- Monthly earnings close: on a fixed day of the month
  (`earnings_close_day`, default 8, as Envato moves a month's Market
  earnings to the balance on the 8th) a cron posts
  `release:{order_item_id}` for every item of earlier months whose
  `hold_until` has passed and whose entitlement is not `disputed`, moving
  the seller share from `seller_pending` to `seller_available` (the
  payable balance). It selects due items by index and writes up to N
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

### Refunds, disputes and deductions

Owner decision t238u (2026-11-24): as on Envato (Author Terms 17a, 17b).
Every refund row has `bearer`: `seller` (default) or `platform`
(goodwill).

- Seller-borne refund (`bearer = seller`): issued by the seller, or by the
  operator under the refund rules (ADR-0024: defects, cooling-off where
  the law gives one, duplicates, and so on). The sale is reversed in full,
  line by line, as `refund:{refund_id}`: the seller share leaves
  `seller_pending` if the month is still open, otherwise
  `seller_available` (it may go negative), and the platform's author fee
  (commission) is reversed too, so the sale disappears from both sides.
  Buyer fee and handling fee, if charged, are refunded to the buyer and
  reversed out of `platform_revenue` with their tax; the region pack
  issues the correction document (credit note, or a refund receipt in
  RU). The seller's monthly report shows the refund in the month it was
  processed.
- Goodwill refund (`bearer = platform`): issued by the operator at its own
  discretion outside the refund rules. The seller balance is not touched:
  the seller keeps the earnings of that sale. The refunded amount is posted
  `goodwill:{refund_id}`: debit `goodwill_refunds` (platform expense),
  credit `provider_clearing`; the sale postings stay as they are. The
  entitlement follows ADR-0024 (revoked unless the operator keeps it).
  Tax correction for the buyer (refund receipt) still applies; how the
  operator books the expense is a region pack and counsel item.
- Dispute opened: the item's release is blocked (no release while
  `disputed`); a released share is not clawed back until the dispute is
  lost. Dispute lost or unauthorised transaction: reversal as a
  seller-borne refund. The payment agent's chargeback fee is posted as
  `dispute:{payment_id}:{n}:fee` and deducted from the seller balance
  (`dispute_fee_payer`, default `seller`). Fees the payment agent keeps on
  a refund (if the acquirer does not return its fee) are posted as
  `refund:{refund_id}:fee` against the seller (`refund_fee_payer`, default
  `seller`). Both payer settings accept `platform`.
- Negative balance: allowed (with no hold, a refund or chargeback after a
  payout makes the balance negative); it is netted against future earnings
  at the next close, never paid out; a configurable threshold raises an
  alert and may suspend payouts or the seller (operator decision,
  audited).
- Optional rolling reserve (`reserve_bps`, `reserve_days`, default 0, off)
  for sellers with high dispute rates, posted to `seller_reserve` with its
  own release txn.
- Affiliate commissions are funded from the platform share (ADR-0023)
  and are reversed with the sale on a seller-borne refund or a lost
  dispute; a goodwill refund does not reverse them unless the operator
  decides so.

### Payouts

- Schedule and threshold are storefront config. Owner decision t237u
  (2026-11-24): payouts are monthly. Owner decision t238u (2026-11-24):
  as on Envato (clause 14c, minimum USD 50; payouts on the 15th after the
  close on the 8th), a payout is made only when `available` reaches the
  minimum threshold `payout_min_minor` per currency; below it the balance
  accumulates. Placeholder for RUB: 5000 RUB (500000 minor units),
  owner-tunable, not final. Config `payout_day` (default 15) follows the
  close day. A payout batch is created by the operator or a
  cron: one `payouts` row per seller with `available >= minimum`, amount =
  available at batch time (from the ledger, in the same short tx that posts
  `payout:{id}`).
- States: `draft` -> `approved` -> `processing` -> `paid` | `failed`;
  `draft` -> `canceled`. Approval needs `payouts.approve`; optional
  two-person rule (creator != approver) as config.
- Payout expenses (bank or provider fee of the transfer): config
  `payout_fee_payer`, default `seller` (Envato clause 16e: the author may
  bear expenses of paying money out). When the seller pays, the fee is
  posted `payout:{payout_id}:fee` from `seller_available` to `payout_fees`
  and the net amount is transferred; with `platform`, the fee is a
  platform expense.
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
| `order_items` (changed) | + commission_bps, hold_until, earnings_minor (seller share snapshot), buyer_fee_minor (default 0) | `earnings_minor = line_minor - commission_minor` for non-operator sellers, 0 for the operator |
| `commission_rules` | scope (`default`,`level`,`category`,`seller`), level, category, seller, bps (0..10000), valid_from, valid_to, note | no overlapping validity for the same scope key (Go check) |
| `orders` (changed) | + handling_fee_minor (default 0) | snapshot at checkout |
| `buyer_fees` | category, currency, amount_minor, valid_from, valid_to | no overlapping validity per (category, currency); empty table = no buyer fee |
| `refunds` (changed, ADR-0007/0024) | + bearer (`seller`,`platform`), fee_minor (agent fee kept on refund) | `platform` only by an operator with `refunds.goodwill`, audited |
| `price_rules` | category, license_type, currency, min_minor, max_minor, extended_min_ratio_bp | (category, license_type, currency) |
| `payout_batches` | period_from, period_to, currency, status (`draft`,`approved`,`processing`,`done`), created_by, approved_by, totals (json, cache) | (period_from, period_to, currency) |
| `payouts` (changed, ADR-0005) | seller, batch, amount_minor, currency, method_snapshot (masked json), status (`draft`,`approved`,`processing`,`paid`,`failed`,`canceled`), idempotency_key, provider_ref, failure_reason, seller_document, approved_by, paid_at | (seller, batch); idempotency_key; amount from the ledger; Go-only writes |
| `payout_methods` | seller, kind, details (encrypted json), masked, status (`unverified`,`verified`,`disabled`), is_default | one default per seller and currency |
| `seller_reports` | seller, kind (region code), period_from, period_to, totals (json), file, status (`draft`,`issued`,`accepted`) | (seller, kind, period_from) |

New ledger accounts: `seller_pending`, `seller_available`, `seller_reserve`,
`payout_clearing`, `bank`, `dispute_fees`, `payout_fees`,
`goodwill_refunds`; deterministic txn ids: `release:{order_item}`,
`reserve:{order_item}`, `refund:{refund}`, `refund:{refund}:fee`,
`goodwill:{refund}`, `payout:{id}`, `payout:{id}:fee`, `payout:{id}:paid`,
`payout:{id}:failed`, `dispute:{payment}:{n}:fee`.

Storefront config keys (defaults): `hold_days` 0, `earnings_close_day` 8,
`payout_day` 15, `payout_min_minor` per currency (RUB placeholder
500000), `payout_fee_payer` `seller`, `dispute_fee_payer` `seller`,
`refund_fee_payer` `seller`, `reserve_bps` 0, `reserve_days` 0,
`handling_fee_minor` 0.

## Consequences

- Every number a seller sees (pending, available, paid) is a ledger query
  with a written rule; nothing is a stored balance.
- Single store: only the operator seller sells, `earnings_minor = 0`, no
  release, payout or report jobs are registered; ADR-0006 principal posting
  is unchanged.
- Resource fit: release and payout jobs touch only due rows through indexes
  `order_items(hold_until)` and `(seller, currency, created)`, in batches of
  a few hundred per transaction, monthly; no per-request ledger
  scans.

## Decisions 2026-11-24 (owner t237u)

- Commission: flat 50% for every seller and item, configurable default
  rule; tiers stay a seam.
- Payouts: monthly batches.
- No exclusivity split; fields removed.

## Decisions 2026-11-24 (owner t238u: "as on Envato Market")

- Hold: no separate hold period or reserve by default; earnings stay on
  the seller balance until paid out; a month's earnings become payable on
  the close day (default 8th); payout once the balance reaches the
  per-currency minimum (RUB placeholder 5000, owner-tunable).
  `hold_days` and the rolling reserve stay as seams, default 0 and off.
- Who pays: the seller bears refunds, chargebacks, payment agent fees on
  refunds and disputes, and unauthorised transactions on its items
  (deducted from the balance); payout fees default to the seller; the
  ordinary acquiring fee of a sale is covered by the platform's 50% author
  fee. Buyer fee per category and handling fee per order exist as config,
  default 0; whether to use them is open (E14).
- Refunds: a seller-borne refund reverses the whole sale (seller share and
  author fee; buyer fee and handling fee with a correction document); a
  goodwill refund is a platform expense and leaves the seller balance
  untouched.
- Affiliate commissions stay funded from the platform share.
