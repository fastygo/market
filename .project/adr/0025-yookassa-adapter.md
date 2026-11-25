# ADR-0025: YooKassa payment adapter

- Status: Accepted (owner t235u, t236u, 2026-11-24). Code:
  `internal/payments/yookassa`, wiring in `cmd/market` (module
  `payment-yookassa`). Live keys pending; the adapter is covered by
  `httptest` tests against an in-memory YooKassa (`yookassa/yktest`) and a
  full engine cycle (`internal/platform/pb/yookassa_flow_test.go`).

## Context

ADR-0007 defines the provider-neutral `payments.Gateway` port and the
claim tx, network call, apply tx rule (no network inside a write
transaction). ADR-0008 and the RU region pack need 54-FZ receipts. The
first real provider is YooKassa (API v3). Switching from the test store to
the approved shop must change only the shop id and the secret key.

## Decision

- Transport: `https://api.yookassa.ru/v3` (override `MARKET_YOOKASSA_API_URL`
  for tests), HTTP Basic auth `shopId:secretKey` from
  `MARKET_YOOKASSA_SHOP_ID` and `MARKET_YOOKASSA_SECRET_KEY` (env or
  secrets file, never argv or logs). Every call asserts `tx.AssertNoTx`.
- Idempotence-Key: hex sha256 of the engine idempotency key (64 chars, the
  provider limit; deterministic, so outbox retries replay the same request
  within the provider's 24 h window). HTTP 202 and 5xx are transient;
  400/401/403 map to `payments.ErrPermanent`, 404 to `ErrNotFound`.
- Payment: one stage (`capture: true`), confirmation `redirect` with
  `return_url = MARKET_PUBLIC_URL + /checkout/return/{order_id}`,
  `metadata.order_id`, description = order number (max 128),
  `payment_method_data.type` from the checkout method (card to
  `bank_card`, sbp, yoo_money).
- Receipt (54-FZ): built from the order's `taxdocs.Document` through the
  RU pack: `vat_code` from `ru.YooKassaVATCode` (1 none, 2 vat0, 3 vat10,
  4 vat20, 7 vat5, 8 vat7, 11 vat22), `payment_subject` and `payment_mode`
  from the pack, customer email. Agent lines are rejected until the
  marketplace agent scheme (ADR-0008) is decided. `MARKET_YOOKASSA_RECEIPTS=0`
  disables receipts (test store without the cash register mode). With
  receipts on, the tax document issuer becomes `yookassa.Receipts`, which
  reads `GET /v3/receipts?payment_id=` or `refund_id=` and maps
  succeeded/canceled to issued/failed.
- Status mapping: pending and waiting_for_capture to pending, succeeded to
  succeeded, canceled to canceled; `refunded_amount >= amount` to refunded.
- Notifications: `POST /hooks/pay/yookassa` stores the event in the inbox
  (ADR-0007). The body is never trusted: the adapter re-fetches the payment
  or refund by id (`GET /v3/payments/{id}`, `/v3/refunds/{id}`) and uses
  that state. Optional source IP allowlist (YooKassa published ranges,
  `MARKET_YOOKASSA_CHECK_IP`, default on; behind a proxy set
  `MARKET_TRUSTED_PROXIES`, the header is honoured only from those peers). Event id is
  `event:object_id`, so redelivery is deduplicated. Handled events:
  payment.succeeded, payment.canceled, refund.succeeded.
- Refunds: `POST /v3/refunds` with the return receipt when
  `MARKET_YOOKASSA_REFUND_RECEIPTS=1` (otherwise the default YooKassa
  scheme forms it automatically for a full refund). A 400 answer marks the
  refund failed.
- Cancel: allowed only in `waiting_for_capture` (GET first, then
  `POST /v3/payments/{id}/cancel`); otherwise the local order is cancelled
  and a late success is refunded by the engine.

## Consequences

- Going live is configuration only: shop id, secret key, public URL,
  webhook URL `https://<public>/hooks/pay/yookassa` in the shop settings.
- Agent (marketplace) receipts are an open item; seller payouts stay
  outside YooKassa (ADR-0021).
- The fake gateway stays for development and emulates the same redirect,
  return and webhook sequence.
