# ADR-0011: goja hooks only for non-money glue

- Status: Proposed

## Context

PocketBase JSVM runs `pb_hooks/*.pb.js` in goja: no npm, partial ES6, pooled
VMs, hot reload, hard to unit test and type check.

## Decision

Go owns: checkout, payments, webhooks, ledger, receipts, entitlements,
downloads, authz, state machines, migrations, SSR.

goja (`pb_hooks`) may own, with no access to money tables:

- email template tweaks and notification text selection;
- small admin conveniences (slug fill, default values, denormalized counters
  that are also recomputed by a Go cron);
- operator one-off scripts and experiments before they move to Go.

Guardrails: a hook that touches `orders`, `payments`, `ledger_entries`,
`entitlements`, `fiscal_receipts` fails review. Each hook file has a header
comment with its owner and purpose; hooks are covered by an integration test
that boots the app with `pb_hooks`.

## Consequences

- Money paths are typed, tested Go. goja stays a convenience, not a second
  backend.
