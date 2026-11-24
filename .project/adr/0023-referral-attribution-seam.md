# ADR-0023: Referral attribution and affiliate commissions (seam only)

- Status: Proposed (owner direction t230u, 2026-11-24). Seam only: the
  partner site and the `affiliates` module are not designed or built here.

## Context

A separate partner site with a three-level affiliate program is planned.
The engine must not change its money core later to support it, so the
seams are fixed now: attribution at checkout, a partner tree of at most
three levels, affiliate commissions as ledger entries reversed on refund
and dispute, and the same hold period as seller earnings. Multi-level
programs carry legal risk in Russia (see `open-questions.md` F); this ADR
records structure only and draws no legal conclusion.

## Decision

- Separate Go module `affiliates` (ADR-0017), off by default; the partner
  site is a separate application that consumes the engine API
  (`/api/v1/partners/...`, partner tokens, read-only stats plus link
  management). The storefront itself only captures attribution.
- Attribution capture: a referral link (`?ref={code}`) sets a first-party
  cookie with the code and timestamp; at checkout the order snapshots
  `referral` (json: code, partner id, captured_at, policy) in the same
  transaction that creates the order. The policy (`first_click` or
  `last_click`) and the window in days are storefront config (open
  question). Self-referral (partner = buyer) is rejected at checkout.
- Partner tree: `partners` (user, code, parent, level_path, status); the
  depth is capped at 3 by a Go invariant (`level_path` holds at most two
  ancestors); a parent can never be a descendant (cycle check).
- Commissions: computed in the `paid` apply transaction from the order
  item snapshot and `affiliate_plans` (level 1..3 bps, valid dates), written
  as ledger entries `aff:{order_item}:{level}` to `affiliate_pending`
  (with a `partner` column on `ledger_entries`, additive nullable field)
  and released to `affiliate_available` by the same hold rule as sellers
  (ADR-0021, `hold_until`). Refund or lost dispute reverses them like the
  seller share. Payouts reuse `payouts` with `party_type = partner`.
- Funding: by default the affiliate amount is taken from the platform's
  commission only (the seller share is never reduced without a seller
  contract term); the posting debits `platform_revenue`. Whether sellers
  may opt in to pay part is an open question (F).
- No affiliate entries in `single_store` without the module; nothing in
  checkout branches on the module except storing the `referral` snapshot,
  which is a no-op when the module is off.

## Collections (future, created by the module)

| Collection | Fields | Unique / invariants |
| --- | --- | --- |
| `partners` | user, code, parent, level_path (json), status (`active`,`suspended`,`closed`), payout_method | code; depth <= 3; no cycles |
| `affiliate_plans` | level (1..3), bps, valid_from, valid_to, scope (`default`,`category`) | no overlapping validity per level and scope |
| `orders` (changed) | + referral (json snapshot) | written once at order creation |
| `ledger_entries` (changed) | + partner (nullable) | as ADR-0006 |

## Consequences

- The partner site can be built later without touching checkout, the
  ledger rules of sellers or the payout states.
- Resource fit: one extra cookie read at checkout and up to three extra
  ledger rows per paid line.
