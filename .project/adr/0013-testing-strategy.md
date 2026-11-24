# ADR-0013: Testing strategy

- Status: Proposed

## Decision

- TDD (test first) for pure domain logic: money and VAT math, order and
  payment state machines, ledger balancing, entitlement rules, signature
  verification, download tokens, slug and number generators. Table tests,
  no IO.
- Integration tests with a real PocketBase app on a temp data dir
  (`tests.NewTestApp` style): migrations, API rules, cross-seller access
  denial, checkout -> webhook -> entitlement happy path and replays.
- Gateway contract tests: `httptest` servers replaying recorded provider
  responses and webhooks (sandbox captures, secrets removed); one test per
  status mapping.
- SSR: golden HTML tests for blocks and screens with fixture view models;
  UI8Kit checks (ui8px 8+4 on `ui/blocks`, `ui/layouts`; literal-text,
  raw-native-tag, block-root) in `scripts/verify.sh`; html-validate and axe
  on rendered pages when the tooling is available.
- E2E smoke (phase 2): Playwright on staging with provider sandbox.
- Gate: `scripts/verify.sh` must be green before every push; coverage target
  80% for domain packages, no target for adapters.

## Consequences

The riskiest code (money) has the cheapest and fastest tests.
