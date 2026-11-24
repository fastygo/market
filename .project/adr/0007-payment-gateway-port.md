# ADR-0007: Payment gateway port, checkout protocol, inbox and outbox

- Status: Proposed (revised 2026-11-24: review, then provider-neutral core)

## Context

The marketplace is reused across regions. Providers differ by deployment:
RU (YooKassa, T-Bank Internet Acquiring, CloudPayments, Robokassa; SBP,
SberPay, T-Pay, Mir Pay reached through the acquirer), EU (Stripe, PayPal).
The core must not know which one runs. One SQLite writer.

## Decision

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
(lines with amount, tax rate, tax code, subject code, agent data). Adapters
that issue documents inline (RU) send it; others (Stripe, PayPal) ignore it
and the region `DocumentIssuer` issues invoices itself. Import direction:
`ordering -> payments -> taxdocs`, `ordering -> taxdocs`; `taxdocs` imports
neither.

Adapters are enabled per storefront by config (`enabled_providers`: method ->
provider). Credentials come from env (`MARKET_PROVIDER_<CODE>_*`), never from
the repo or admin-visible fields. Phase 1 creates payments with immediate
capture; no `CapturePayment` until a provider needs it (Stripe manual capture
or PayPal authorize flows are not used). Phase 2: fallback order per method.

### Checkout protocol (use case in `internal/ordering`)

1. `Idempotency-Key` is required on `POST /checkout` (header or form field
   generated when the checkout page renders). One DB transaction: order +
   items + payment `created`; payment key = `pay:{order_key}:{provider}`
   (deterministic). No network call inside the transaction.
2. `CreatePayment` with the payment key.
3. Store `provider_payment_id`, `confirmation_url`; payment `pending`, order
   `awaiting_payment`.
4. Retry with the same order key: order `awaiting_payment` -> repeat 2-3 (the
   provider returns the same payment for the same key); order `paid` -> show
   it; never create a second order or payment. A crash between 2 and 3 is
   healed by the retry or by reconciliation.

### Cancel and late success

- The timeout cron (30 min) only requests `CancelPayment` via the outbox.
  The order becomes `cancelled` only after the provider confirms (response or
  `FetchPayment`).
- If the provider reports `succeeded` for a `cancelled` or timed-out order,
  `Transition` moves it to `paid` with the normal effects. This edge is in the
  transition table and in the tests.

### Effects

- In the same DB transaction as the status change: entitlement insert,
  ledger entries with deterministic `txn_id` (ADR-0006), tax document row
  `pending` (if the region requires one), inbox row `processed`.
- In the outbox (written in that transaction): only external effects:
  purchase email, `DocumentIssuer.Issue` retry, `CancelPayment`, `Refund` calls.
  Each with a deterministic `idempotency_key`.

### Inbox and outbox workers

Ownership: `payments` verifies webhooks and inserts inbox rows; the inbox
apply step, return URL, reconciliation and refund command are use cases in
`internal/ordering`, because they change the order, entitlements and ledger
in one transaction (`payments` never imports `ordering`).

- Webhook handler: verify -> one insert into `payment_events` -> 2xx. Unique
  conflict on `(provider, event_id)` = already accepted, 2xx.
- Verification: YooKassa (IP allowlist), T-Bank (`Token` SHA-256 with the
  terminal password), CloudPayments (`Content-HMAC`), Robokassa
  (`SignatureValue` with password 2); Stripe (`Stripe-Signature` with the
  endpoint secret), PayPal (verify-webhook-signature API). Each adapter owns
  its check. The status is always confirmed with
  `FetchPayment`, never taken from the body.
- Exactly one inbox worker and one outbox worker per process. Claim: atomic
  update `received|pending -> processing` with `locked_until`; expired leases
  return to the queue; after N attempts -> `dead` + alert, the queue moves on.
- Status updates are conditional (`WHERE status = ? AND status_version = ?`),
  so webhook, return URL and reconciliation can race safely.
- Return URL `GET /checkout/return/{order}` calls `FetchPayment` ->
  `Transition`, ignoring query parameters.

### Reconciliation (cron every 5 min, same `Transition`, no second path)

1. Payments `created`/`pending` older than 10 min -> `FetchPayment`.
2. Orders `awaiting_payment` or `cancelled` with a provider payment id ->
   `FetchPayment` (late success).
3. Orders `paid` without an entitlement or without the ledger `txn_id` ->
   replay the in-transaction effects (idempotent by uniques).
4. Tax documents not `issued`/`not_required` -> `DocumentIssuer.Fetch`,
   then outbox `Issue`, up to the attempt cap, then page alert and operator
   task.

### Refunds and disputes

- Refund (phase 1: full only): `refunds` row `pending` with its key ->
  `Gateway.Refund` -> on confirmation one tx: payment and order `refunded`,
  ledger reversal, entitlement `refunded`, return tax document `pending` (region-dependent).
- Dispute / chargeback: provider event -> `FetchPayment` -> payment
  `disputed`, entitlement `disputed` (downloads stop). Lost -> `refunded`
  path (reversal, entitlement `refunded`, return tax document if the region requires
  it for that flow); won -> back to `succeeded`/`active`.

### Shutdown

Workers stop claiming on `OnTerminate`, finish or release leases within the
context deadline.

## Consequences

- Adding a provider is one package + config, no domain change.
- Plain `net/http` adapters, tested with recorded fixtures (ADR-0013).

## Review 2026-11-24 (BalunMY)

Applied P0-1 (checkout protocol), P0-2 (cancel only after provider confirms,
late success, `CancelPayment`), P0-3 (in-tx effects vs outbox, leases, single
workers), P0-4 (wider reconciliation), P0-5 (receipt methods), P1-1
(disputes), P1-2 (full refunds only), P1-4 (return URL), P1-7 (conditional
updates, duplicate webhook = 2xx), P2 (no `waiting_for_capture`, shutdown).

## Revision 2026-11-24 (owner: provider-neutral core)

Receipt methods moved out of `Gateway` into the optional
`InlineDocumentGateway`; tax documents are a region capability
(ADR-0015). Stripe and PayPal are adapters like the RU providers; all P0
guarantees (checkout protocol, `CancelPayment`, in-tx ledger, leases,
reconciliation) are provider-neutral.
