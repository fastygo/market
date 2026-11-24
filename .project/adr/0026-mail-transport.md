# ADR-0026: Transactional mail transport without SMTP

- Status: Accepted (owner t235u, t236u, 2026-11-24). Code: `internal/mail`,
  `internal/mail/unisender`, `internal/mail/resend`, `internal/notify`.

## Context

Buyers must receive the order and a download (cabinet) link. Target hosts
often block outbound SMTP, and running a mail server is out of scope. The
engine already has a durable outbox (ADR-0007).

## Decision

- Port `mail.Sender` with `Send(ctx, Message)`; `Message` carries To,
  Subject, Text, HTML and an idempotency key. Header injection is rejected
  by `mail.Validate`. Errors are transient unless wrapped in
  `mail.ErrPermanent`.
- Sending happens only from the outbox worker (`email.purchase` events
  written in the same tx that marks the order paid); the outbox key is the
  provider idempotency key, so retries do not duplicate mail.
- Adapters, chosen by `MARKET_MAIL_PROVIDER`:
  - `file` (default, development): writes RFC 5322 `.eml` files to
    `MARKET_MAIL_DIR` (default `pb_data/mail`).
  - `unisender` (default for RU production): Unisender Go HTTP API,
    `X-API-KEY`, `idempotence_key`.
  - `resend`: Resend HTTP API for non-RU deployments.
  Common settings: `MARKET_MAIL_API_KEY`, `MARKET_MAIL_FROM`,
  `MARKET_MAIL_FROM_NAME`, `MARKET_MAIL_API_URL` (tests).
- Provider choice for RU: Unisender Go. Russian company and data centres
  (152-FZ friendly), payable from Russia, good deliverability to Mail.ru,
  Yandex and Gmail, free tier for low volume, HTTP API with idempotence.
  Resend, Postmark and Mailgun cannot be paid from Russia and carry a
  sanctions risk; Resend is kept as an adapter for other regions.
- Texts come from the dictionary (`mail.purchase.*`, `mail.signin.*`);
  HTML through `html/template`, plus a plain text part.

## Consequences

- A verified sender domain (SPF, DKIM) at the provider is required before
  production.
- Without a provider key the engine still works and writes `.eml` files.

## Amendment 2026-11-25 (owner t242u): PocketBase mailer as the default

Self-hosted deployments have outbound SMTP, so the default transport is
the PocketBase mailer (`MARKET_MAIL_PROVIDER=pocketbase`, package
`internal/mail/pbmail`):

- It sends through `app.NewMailClient()` with the SMTP settings from the
  admin UI (Settings, Mail settings). Settings are read on every send, so
  turning SMTP on takes effect without a restart. PocketBase's own mail
  (email verification, password reset, OTP, login alerts) uses the same
  SMTP account and sender; it is not changed by the engine.
- Sender: `MARKET_MAIL_FROM` and `MARKET_MAIL_FROM_NAME` when set,
  otherwise Settings, Application, sender name and address.
- While SMTP is disabled the engine logs one warning and writes `.eml`
  files (the file sender), so development and previews keep working.
  `MARKET_MAIL_SENDMAIL=1` uses PocketBase's local sendmail client
  instead, as PocketBase itself does without SMTP.
- Outbox semantics are unchanged: the send runs in the outbox worker
  (no transaction), failures are retried with backoff up to the attempt
  cap. SMTP has no idempotency key, so the Message-ID is derived from the
  outbox key; a duplicate after a lost acknowledgement is possible but
  recognizable. SMTP 550 to 554 answers are marked permanent; 4xx,
  authentication and connection errors are retryable (fixable in the
  admin UI).
- Unisender Go and Resend stay as HTTP adapters for hosts without SMTP
  egress.
