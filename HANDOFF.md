# Handoff for a backend engineer

State on 2026-11-25. Read this first, then `README.md` and
`.project/README.md` (ADR index, decisions log).

## What it is

A white-label commerce engine for digital goods: PocketBase v0.40 in Go
extend mode (one binary, embedded SQLite, admin UI at `/_/`) plus templ SSR
pages built only from UI8Kit primitives. Storefront brand is data
(StorEEE Market); the core is region-neutral, the RU region pack holds
taxes and receipts.

## Run

```sh
make build                                    # Go only; generated code is committed
./bin/market superuser upsert admin@example.com 'a-long-password'
MARKET_FAKE_GATEWAY=1 ./bin/market serve --http 127.0.0.1:8090
# storefront /, /catalog, /products/aurora-theme; cabinet /account; admin /_/
```

Migrations and a placeholder catalog apply on start (`internal/migrations`).
Full browser flow with the fake gateway: README "Try the money path".
Production layout and every variable with comments:
`deploy/market.env.example` (mode 0600, never commit values).

## Environment variables (names only)

- Core: `MARKET_STOREFRONT`, `MARKET_PUBLIC_URL`, `MARKET_CLIENT_IP_HEADER`,
  `MARKET_PAYMENT_TIMEOUT`, `MARKET_TERMS_VERSION`, `MARKET_CHECKOUT_METHOD`,
  `GOMEMLIMIT`.
- Secrets (random per process when unset, which breaks links on restart):
  `MARKET_SESSION_SECRET`, `MARKET_DOWNLOAD_SECRET`.
- Files: `MARKET_S3_ENDPOINT`, `MARKET_S3_REGION`, `MARKET_S3_BUCKET`,
  `MARKET_S3_ACCESS_KEY`, `MARKET_S3_SECRET_KEY`, `MARKET_S3_URL_TTL`,
  `MARKET_S3_PATH_STYLE`, `MARKET_S3_LOCAL_BASE`, `MARKET_FILES_DIR`.
- Payments: `MARKET_YOOKASSA_SHOP_ID`, `MARKET_YOOKASSA_SECRET_KEY`,
  `MARKET_YOOKASSA_API_URL`, `MARKET_YOOKASSA_RECEIPTS`,
  `MARKET_YOOKASSA_REFUND_RECEIPTS`, `MARKET_YOOKASSA_CHECK_IP`;
  development only: `MARKET_FAKE_GATEWAY`, `MARKET_FAKE_WEBHOOK_SECRET`.
- Mail: `MARKET_MAIL_PROVIDER`, `MARKET_MAIL_FROM`, `MARKET_MAIL_FROM_NAME`,
  `MARKET_MAIL_SENDMAIL`, `MARKET_MAIL_DIR`, `MARKET_MAIL_API_KEY`,
  `MARKET_MAIL_API_URL`.

## Real vs fake

| Area | State |
| --- | --- |
| Catalog, product pages, dark theme | Real (placeholder seed data) |
| Checkout, orders, inbox/outbox workers, refunds, ledger | Real (ADR-0007) |
| Payments: YooKassa | Adapter done and covered by `httptest` tests; never run against the live API, shop keys pending (ADR-0025). Agent (marketplace) receipt lines are rejected until the agent scheme is decided |
| Payments: fake gateway | `MARKET_FAKE_GATEWAY=1` emulates redirect, return and webhook; must be 0 in production |
| Mail | PocketBase mailer with the admin SMTP settings (default); `.eml` files while SMTP is off (ADR-0026) |
| Buyer cabinet | Real: magic link or password sign-in, cookie session, CSRF, orders, downloads (ADR-0027). No password set/reset screen yet; PocketBase's built-in reset API exists |
| Downloads | Signed `/dl` links to S3 pre-signed URLs; `MARKET_FILES_DIR` serves local files for dev and small installs |
| 54-FZ receipts | Through YooKassa receipts when enabled; fake issuer only with the fake gateway; no issuer with `MARKET_YOOKASSA_RECEIPTS=0` |
| Russian texts | Not in the repo (no Cyrillic rule); only `ui/dict/en.json`. Delivery path is open question A1 |
| Vendor marketplace (phase 3) | Docs only (ADR-0020 to ADR-0024) |

## Where things are

- Entry and wiring: `cmd/market/main.go` (modules, env, routes, workers).
- Domain: `internal/ordering`, `internal/licensing`, `internal/account`,
  `internal/notify`, `internal/taxdocs`; providers in `internal/payments/*`,
  `internal/mail/*`; persistence in `internal/platform/pb`; SSR in
  `internal/web` and `internal/web/screens`.
- Decisions: `.project/adr`; open questions for the owner:
  `.project/open-questions.md`; roadmap and status: `.project/plan.md`.

## Tests and gate

- `scripts/verify.sh` (`make verify`) must pass before every push: templ
  freshness, gofmt, vet, no Cyrillic, no real email addresses or committed env
  files, UI8Kit
  screen rules, `go test -race ./...`.
- End-to-end cycles against a real PocketBase in a temp dir:
  `internal/platform/pb/checkout_test.go`, `refunds_test.go`,
  `download_test.go`, `yookassa_flow_test.go`, `cabinet_flow_test.go`
  (browser flow through the declared route list).
- Unit tests next to each package (`internal/payments/yookassa`,
  `internal/mail`, `internal/mail/pbmail`, `internal/account`, ...).

## Must know

- Rule zero (ADR-0007): no network call inside a write transaction; claim,
  call, apply. Providers call `tx.AssertNoTx`.
- New routes must be listed in the `*Patterns` slices, or the PocketBase
  router never forwards them (`web.AllPatterns`).
- Run one process per `pb_data`: the inbox and outbox workers assume a
  single instance.
- The outbox retries every failed effect with backoff up to 8 attempts;
  provider `ErrPermanent` is not yet short-circuited.
- Generated files (`*_templ.go`, skin CSS, `ui/kit`) are committed; never
  edit them by hand (ADR-0014).
- No secrets, real personal data or Cyrillic in the repo (ADR-0016).
