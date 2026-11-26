# ADR-0033: Payment providers as in-process adapters

- Status: Accepted (owner t391u, 2026-11-26). Builds on ADR-0007 (gateway
  port, checkout protocol, inbox and outbox) and ADR-0025 (YooKassa
  adapter). Does not replace those ADRs: the protocol, rule zero (no
  network inside a write transaction) and the YooKassa mapping stay
  there. This ADR records the architectural choice the owner confirmed
  when asking to add further acquirers, and the small additions needed
  to do that without a behaviour change for existing YooKassa payments.

## Context

Checkout already talks to a `payments.Gateway` port. YooKassa is the
first real adapter; a fake gateway covers tests and development.
Receipts (54-FZ) go through `taxdocs.Issuer`, with `yookassa.Receipts`
as the first implementation. Each payment is a row in the `payments`
collection (one attempt, unique per `(provider, provider_payment_id)`),
and provider notifications land in `payment_events` before apply.

The question was whether the next acquirers (CloudPayments, T-Bank,
later Stripe once a foreign legal entity exists) should live in a
separate payment service. They should not. The monolith already has
one SQLite writer, one outbox and one apply path. A second service
would add a deploy, an auth path and a network failure mode, and
would still call the same provider APIs.

A microservice would win only if we had to store card data and meet
PCI DSS as a card holder. We do not: the buyer pays on the provider
page or widget, and PAN never reaches us.

## Decision

### 1. Adapters stay in the monolith

Every provider is an in-process adapter behind `payments.Gateway` in
`internal/payments` (`CreatePayment`, `FetchPayment` as the status
source of truth, `CancelPayment`, `RefundPayment`, `ParseWebhook` with
signature or source-IP verification). `payments.HandleWebhook` is the
HTTP helper: verify, then persist the event; apply is a later inbox
step. Adding a provider is one package, configuration and tests. There
is no payment microservice.

SBP, cards and wallet methods are payment methods inside a provider
(`Gateway.Methods`), not providers of their own.

### 2. Registry, enablement and selection

`payments.Registry` holds the enabled adapters. An adapter is enabled
when its own credentials are present (YooKassa: `MARKET_YOOKASSA_SHOP_ID`),
then optionally filtered by the allow-list `MARKET_PAYMENT_PROVIDERS`
(comma-separated codes; unset keeps today's auto-enable behaviour).

Provider choice uses config rules (`MARKET_PAYMENT_RULES`) matched
against currency, buyer country, legal entity and method. The first
matching rule whose provider is enabled wins. When no rule matches,
checkout keeps using the storefront map `enabled_providers` (method to
provider). The legal entity of the RF shop is the code `it_garage`
(`MARKET_PAYMENT_ENTITY`); it is a configuration value, not a reason
to split the process.

### 3. Orders and payment attempts stay separate

The order state machine stays on the order (`pending`,
`awaiting_payment`, `paid`, `cancelled`, `refund_pending`, `refunded`,
`disputed`, plus `fulfilled`). Each provider call is a row in
`payments` (the payment-attempt collection; the name is kept to avoid
a breaking rename). An attempt stores provider, external id, amount,
currency, status, idempotency key and optional legal entity. The raw
event log remains `payment_events` (unique on `(provider, event_id)`):
the event is written before apply, and a unique conflict is accepted
as a duplicate (HTTP 2xx).

A failed or canceled attempt on an unpaid order can be followed by a
new attempt on the same order, with a new idempotency key
(`pay:{order_key}:{provider}:{n}` from the second attempt). The first
attempt keeps the original key, so existing in-flight payments do not
change. A cancelled order is reopened to `pending` for that retry.
Checkout with the same client key while an attempt is still `created`
or `pending` still returns that attempt, never a second provider
payment.

### 4. Webhook paths

The canonical notification path is `POST /webhooks/{provider}`. The
existing `POST /hooks/pay/{provider}` (including
`/hooks/pay/yookassa`) stays as an alias with the same handler, so
shop settings that already point at the old URL keep working.

### 5. Receipts stay a separate port

54-FZ receipts and other tax documents stay on `taxdocs.Issuer`, not
on `Gateway`. YooKassa receipts remain the first implementation.
Enabling or disabling them is still `MARKET_YOOKASSA_RECEIPTS`.

## Consequences

- YooKassa behaviour is unchanged: same API mapping, same inbox
  apply path, same environment variables, same alias URL.
- The next acquirer is a new adapter package plus rules; it does not
  need a new service or a new order machine.
- A late success on an earlier attempt after a retry has started still
  follows ADR-0007 (order becomes `paid`). The newer pending attempt is
  left for timeout cancel and reconciliation; a double capture is an
  operator refund, not a second apply path.
- Going live with a second provider is configuration (credentials,
  allow-list, rules) after the adapter lands. Deploy of this slice is
  a separate owner approval.
