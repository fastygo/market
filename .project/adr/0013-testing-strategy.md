# ADR-0013: Testing strategy

- Status: Proposed (revised after review 2026-11-24)

## Decision

- TDD (test first) for pure domain logic: money, VAT extraction and rounding
  remainders, discount allocation (phase 2), commission split (10001 kopecks
  at 1500 bps and similar vectors), order and payment transition tables,
  ledger balancing and deterministic `txn_id`, entitlement rules, signature
  verification, download tokens, order number generator. Table tests, no IO.
- Integration tests with a real PocketBase app on a temp data dir:
  migrations, uniques, API rules, cross-seller access denial, buyer and
  seller writes to money collections denied, admin edits of ledger and
  published releases rejected by hooks.
- Crash and race tests for the money path (integration, deterministic fault
  injection through a fake `Gateway` and a crash hook between steps):
  1. checkout retried with the same key -> one order, one payment;
  2. crash after `CreatePayment`, before storing the provider id -> retry and
     reconciliation heal it, no second payment;
  3. webhook, return URL and reconciliation processing the same payment
     concurrently -> one transition, one entitlement, one ledger txn;
  4. order cancelled by timeout request, provider reports `succeeded` ->
     order `paid`, entitlement granted;
  5. payment succeeded, receipt registration fails -> outbox retry ->
     `registered`; after the cap -> `failed` + alert;
  6. refund -> entitlement `refunded`, `/dl/{token}` issued before refund is
     rejected;
  7. outbox and inbox workers re-run after a crash (expired lease) -> no
     double ledger entries or emails;
  8. dispute -> downloads stop; dispute won -> restored;
  9. duplicate webhook -> 2xx, processed once;
  10. paid order with missing effects -> reconciliation replays them.
- Gateway contract tests: `httptest` servers replaying recorded provider
  responses and webhooks (sandbox captures, secrets removed); one test per
  status mapping, including cancel, refund and receipt calls.
- SSR: golden HTML tests for blocks and screens; UI8Kit checks (ui8px 8+4 on
  `ui/blocks`, `ui/layouts`; literal-text, raw-native-tag, block-root) in
  `scripts/verify.sh`; html-validate and axe when the tooling is available.
- E2E smoke (phase 2): Playwright on staging with provider sandbox.
- Gate: `scripts/verify.sh` green before every push (gofmt, vet, `go test
  -race`, templ diff, UI8Kit checks, goja money-collection grep); coverage
  target 80% for domain packages, none for adapters.

## Consequences

The riskiest code (money) has the cheapest tests plus explicit failure
scenarios.

## Review 2026-11-24 (BalunMY)

Applied P1-11 (crash and race scenarios), P0-7 test extension (buyer writes
denied).
