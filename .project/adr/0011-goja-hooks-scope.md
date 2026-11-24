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
`tax_documents`, `ledger_entries`, `entitlements`, `downloads`, `outbox`,
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

## Revision 2026-11-24 (owner: module system, ADR-0017)

There are two goja runtimes. PocketBase `pb_hooks` stay glue-only as above.
The engine plugin sandbox (`internal/modules`) runs region and locale policy
plugins: separate VM pool, no `$app`, DAO, network, filesystem or timers,
per-call timeout (default 50 ms), input/output size caps, pure functions with
`now` passed in, output validated by Go against the capability schema,
contract tests in `go test` and at start-up. Neither runtime writes money
collections, calls providers or reads secrets.

## Review r2 2026-11-24 (BalunMY)

Phase 0-1 has only the PocketBase `pb_hooks` runtime (glue). The engine
policy sandbox is deferred until its trigger (ADR-0017); when built, it runs
outside write transactions and never on the money-apply path.
