# ADR-0007: Payment gateway port, checkout protocol, inbox and outbox

- Status: Proposed (revised 2026-11-24: review r1, provider-neutral core,
  review r2)

## Context

The engine is reused across regions, so the core must not know which payment
provider runs. Phase 1 has one consumer: one RU provider (YooKassa, T-Bank
Internet Acquiring, CloudPayments or Robokassa; SBP, SberPay, T-Pay, Mir Pay
through the acquirer). One SQLite writer.

## Decision

### Rule zero: no network inside a write transaction

Every write transaction is short: `BEGIN IMMEDIATE`, conditional updates and
inserts, commit. No network call (`CreatePayment`, `FetchPayment`,
`CancelPayment`, `Refund`, `Issue`), no goja call and no CPU-heavy work
inside it. Provider calls happen only between transactions. A use case is a
sequence: tx (record intent or claim) -> commit -> network call -> new tx
(apply result with a conditional update). `busy_timeout` is set explicitly
(ADR-0001) and lock waits are measured.

### Port (declared in `internal/payments`), one adapter package per provider

```go
type Gateway interface {
    Code() ProviderCode
    Methods() []Method
    CreatePayment(ctx context.Context, req CreatePaymentRequest) (CreatePaymentResult, error) // IdempotencyKey, Amount (minor + currency), optional Document
    FetchPayment(ctx context.Context, providerPaymentID string) (Status, error)                // source of truth
    CancelPayment(ctx context.Context, providerPaymentID, idempotencyKey string) (Status, error)
    Refund(ctx context.Context, req RefundRequest) (RefundResult, error)                      // IdempotencyKey, optional return Document
    ParseWebhook(ctx context.Context, r *http.Request) (Event, error)                         // verify only, no side effects
}

// Optional capability: adapters whose provider issues tax documents
// (RU cloud registers) also implement this; the RU DocumentIssuer uses it.
type InlineDocumentGateway interface {
    Gateway
    IssueDocument(ctx context.Context, req DocumentRequest) (DocumentResult, error)
    FetchDocument(ctx context.Context, externalID string) (DocumentStatus, error)
}
```

`CreatePaymentRequest.Document` is the region-neutral `taxdocs.Document`
built by the pure `taxdocs.Build`. Import direction: `ordering -> payments ->
taxdocs`, `ordering -> taxdocs`; `taxdocs` imports neither.

Adapters are enabled per storefront by config (`enabled_providers`: method ->
provider). Credentials come from env (`MARKET_PROVIDER_<CODE>_*`). Immediate
capture only; no `CapturePayment` until a provider needs it. Adapters may
normalize provider webhook types into an internal event enum; the payment
status machine does not change (a buyer still in 3DS or SBP QR is `pending`
with a `confirmation_url`).

### Checkout protocol (use case in `internal/ordering`)

1. `Idempotency-Key` is required on `POST /checkout`. Tx 1: order + items +
   payment `created`; payment key = `pay:{order_key}:{provider}`. Commit.
2. `CreatePayment` with the payment key (no tx open).
3. Tx 2: store `provider_payment_id`, `confirmation_url`; payment `pending`,
   order `awaiting_payment` (conditional update). Commit.
4. Retry with the same order key: order `awaiting_payment` -> repeat 2-3 (the
   provider returns the same payment for the same key); order `paid` -> show
   it; never a second order or payment. A crash between 2 and 3 is healed by
   the retry or by reconciliation.

No per-order lock or lease: the single writer plus conditional updates
(`WHERE status = ? AND status_version = ?`) close the races.

### Cancel and late success

- The timeout cron (30 min) only requests `CancelPayment` via the outbox. The
  order becomes `cancelled` only after the provider confirms.
- A provider `succeeded` for a `cancelled` or timed-out order moves it to
  `paid` with the normal effects (transition table and tests).

### Effects

- In the apply transaction (same tx as the status change): entitlement
  insert, ledger entries with deterministic `txn_id` (ADR-0006), order totals
  snapshot, tax document row `pending` (if the region requires one), inbox
  row `processed`.
- In the outbox (written in that tx): only external effects: purchase email,
  `DocumentIssuer.Issue`, `CancelPayment`, `Refund`, each with a deterministic
  `idempotency_key`.

### Inbox and outbox workers

Ownership: `payments` verifies webhooks and inserts inbox rows; apply,
return URL, reconciliation and refund command are use cases in
`internal/ordering` (`payments` never imports `ordering`).

- Webhook handler: verify -> one short insert into `payment_events` -> 2xx;
  unique conflict on `(provider, event_id)` = already accepted, 2xx.
- Verification per RU adapter: YooKassa (IP allowlist), T-Bank (`Token`
  SHA-256 with the terminal password), CloudPayments (`Content-HMAC`),
  Robokassa (`SignatureValue` with password 2). The status is always
  confirmed with `FetchPayment`, never taken from the body.
- Exactly one inbox worker and one outbox worker per process. Each item is
  three steps: (a) claim tx: `received|pending -> processing`,
  `locked_until`, commit; (b) network call outside any tx (`FetchPayment` or
  the outbox effect); (c) apply tx: conditional `Transition` and in-tx
  effects, mark `processed`/`done`, commit. Expired leases return to the
  queue; after N attempts -> `dead` + alert.
- Return URL `GET /checkout/return/{order}`: `FetchPayment` (no tx) -> apply
  tx; query parameters ignored.

### Reconciliation (cron every 5 min, same apply path, no second path)

Each selection reads in a short tx, calls the provider outside a tx, applies
in a new tx:

1. Payments `created`/`pending` older than 10 min -> `FetchPayment`.
2. Orders `awaiting_payment` or `cancelled` with a provider payment id ->
   `FetchPayment` (late success).
3. Orders `paid` without an entitlement or without the ledger `txn_id` ->
   replay the in-tx effects (idempotent by uniques).
4. Tax documents not `issued`/`not_required` -> `DocumentIssuer.Fetch`, then
   outbox `Issue`, up to the attempt cap, then page alert and operator task.
5. Paid but not completable: the provider reports `succeeded` but the order
   cannot be fulfilled (offer or product removed, entitlement invariant
   fails, amount mismatch) -> the order is marked `refund_pending` and the
   same full-refund command as the operator refund runs automatically; alert
   the operator. Covered by a test (ADR-0013).

### Refunds and disputes

- Refund (phase 1: full only): tx: `refunds` row `pending` (provider, key,
  reason code, note) -> commit -> `Gateway.Refund` -> tx on confirmation:
  payment and order `refunded`, ledger reversal, entitlement `refunded`,
  return tax document `pending` (region-dependent).
- Dispute: provider event -> `FetchPayment` -> payment `disputed`,
  entitlement `disputed` (downloads stop). Lost -> refund path; won -> back
  to `succeeded`/`active`.

### Shutdown

Workers stop claiming on `OnTerminate`, finish or release leases within the
context deadline.

## Phase 4 note: other regions' providers

Stripe and PayPal fit the same `Gateway` (`Stripe-Signature` with the
endpoint secret; PayPal verify-webhook-signature API; invoices from the
region `DocumentIssuer`). They are not part of phase 0 to 3 and no code or
config for them ships before an EU deployment is planned.

## Consequences

- Adding a provider is one package + config, no domain change.
- Plain `net/http` adapters, tested with recorded fixtures (ADR-0013).
- The single writer is never held across a network round trip.

## Review 2026-11-24 (BalunMY)

Applied P0-1, P0-2, P0-3, P0-4, P0-5, P1-1, P1-2, P1-4, P1-7, P2 (see the
previous revision for details).

## Review r2 2026-11-24 (BalunMY)

Applied P0-A (rule zero, three-step claim/call/apply, `BEGIN IMMEDIATE`),
P1-1 (Stripe/PayPal moved to a phase 4 note), P1-2 (`refunds.provider`),
P1-3 (auto-refund branch for paid-but-uncompletable), mapping item 10
trimmed (no per-order lock), mapping item 2 deferred (no `requires_more`
status; adapter-level event enum only).
