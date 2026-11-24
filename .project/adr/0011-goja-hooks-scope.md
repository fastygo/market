# ADR-0011: goja hooks only for non-money glue

- Status: Accepted (direction confirmed by review 2026-11-24; kept strict)

## Context

PocketBase JSVM runs `pb_hooks/*.pb.js` in goja: no npm, partial ES6, pooled
VMs, hot reload, hard to unit test and type check.

## Decision

Go owns: checkout, payments, webhooks, inbox and outbox workers, ledger,
receipts, entitlements, downloads, authz, state machines, write-protection
hooks, migrations, SSR.

goja (`pb_hooks`) may own, with no read or write of money and fulfilment
collections (`orders`, `order_items`, `payments`, `payment_events`, `refunds`,
`fiscal_receipts`, `ledger_entries`, `entitlements`, `downloads`, `outbox`,
`payouts`):

- email template tweaks and notification text selection;
- small admin conveniences (slug fill, default values);
- denormalized display counters (`sales_count`, `rating_avg`), which are only
  a cache: a Go cron recomputes them as the source of truth, and nothing reads
  them for money or sales reports;
- operator one-off scripts and experiments before they move to Go.

Guardrails: a hook touching the collections above fails review (a grep check
in `scripts/verify.sh`). Each hook file has a header comment with its owner
and purpose; hooks are covered by an integration test that boots the app with
`pb_hooks`.

## Consequences

Money paths are typed, tested Go. goja stays a convenience, not a second
backend.

## Review 2026-11-24 (BalunMY)

Kept strict; counters declared cache only (P2); verify-script grep added.
