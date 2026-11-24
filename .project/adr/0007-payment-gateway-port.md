# ADR-0007: Payment gateway port with adapters, webhook inbox, outbox

- Status: Proposed

## Context

Sales only in Russia, several methods (card, SBP, SberPay, T-Pay, Mir Pay,
wallets) and several providers: YooKassa, T-Bank Internet Acquiring,
CloudPayments, Robokassa. SBP is reached through the acquiring provider.
Foreign providers are out.

## Decision

Port (declared in `internal/payments`), one adapter package per provider:

```go
type Gateway interface {
    Code() ProviderCode
    Methods() []Method
    CreatePayment(ctx context.Context, req CreatePaymentRequest) (CreatePaymentResult, error) // req carries IdempotencyKey and Receipt
    ParseWebhook(ctx context.Context, r *http.Request) (Event, error)                         // verify, no side effects
    FetchPayment(ctx context.Context, providerPaymentID string) (Status, error)                // source of truth
    Refund(ctx context.Context, req RefundRequest) (RefundResult, error)
}
```

- `Registry` maps method -> provider by config (phase 2: fallback order).
- Verification per provider: YooKassa (IP allowlist, then `FetchPayment`),
  T-Bank (`Token` SHA-256 with the terminal password), CloudPayments
  (`Content-HMAC`), Robokassa (`SignatureValue` with password 2). In all cases
  the final status is confirmed by `FetchPayment`, never trusted from the body.
- Inbox: verified events go to `payment_events` (unique provider + event_id),
  2xx returned at once. Outbox worker processes them, applies the payment
  state machine and writes effects in one transaction.
- Idempotency: order key on `POST /checkout`; payment key sent to the
  provider; effects are keyed so a replay is a no-op.
- Reconciliation cron re-fetches stale `pending` payments.
- Sandbox first; adapters tested with recorded fixtures (ADR-0013).

## Consequences

- Adding a provider is one package + config, no domain change (open/closed
  where it pays).
- Provider SDKs are not used unless official and small; plain `net/http`.
