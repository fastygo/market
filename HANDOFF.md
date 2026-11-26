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
MARKET_FAKE_GATEWAY=1 ./bin/market serve --http 127.0.0.1:8090   # or MARKET_DEV=1 without the fake gateway
# storefront /, /catalog?q=theme, /products/aurora-theme, /authors/godudy; cabinet /account; admin /_/
```

Migrations and a placeholder catalog apply on start (`internal/migrations`).
Full browser flow with the fake gateway: README "Try the money path".
Production layout and every variable with comments:
`deploy/market.env.example` (mode 0600, never commit values).

## Environment variables (names only)

- Core: `MARKET_STOREFRONT`, `MARKET_PUBLIC_URL` (alias `MARKET_BASE_URL`;
  also canonical, `og:url` and JSON-LD links), `MARKET_TRUSTED_PROXIES`,
  `MARKET_CLIENT_IP_HEADER`, `MARKET_PAYMENT_TIMEOUT`, `MARKET_TERMS_VERSION`,
  `MARKET_CHECKOUT_METHOD`, `MARKET_DEV`, `GOMEMLIMIT`.
- Secrets (32+ characters, different from each other):
  `MARKET_SESSION_SECRET`, `MARKET_DOWNLOAD_SECRET`.
- Files: local by default (`MARKET_FILES_DIR`, default `<pb_data>/files`);
  S3 when `MARKET_S3_ENDPOINT` is set: `MARKET_S3_REGION`, `MARKET_S3_BUCKET`,
  `MARKET_S3_ACCESS_KEY`, `MARKET_S3_SECRET_KEY`, `MARKET_S3_PREFIX`,
  `MARKET_S3_URL_TTL`, `MARKET_S3_PATH_STYLE`.

### Start-up checks (fail-fast)

Production is any start without `MARKET_FAKE_GATEWAY=1` or `MARKET_DEV=1`.
`serve` then refuses to start and lists every problem when:

- `MARKET_SESSION_SECRET` or `MARKET_DOWNLOAD_SECRET` is missing, shorter
  than 32 characters, or both are equal;
- `MARKET_PUBLIC_URL` (alias `MARKET_BASE_URL`) is missing, not `https`, or
  has a path, or both are set to different values;
- `MARKET_TRUSTED_PROXIES` has a bad CIDR, or `MARKET_CLIENT_IP_HEADER` is set
  without it;
- `MARKET_S3_ENDPOINT` is set but region, bucket, keys or prefix are
  missing or invalid, or the local files directory cannot be created;
- a duration (`MARKET_S3_URL_TTL`, `MARKET_PAYMENT_TIMEOUT`) does not parse.

Mail is not checked: the default mailer reads SMTP from the PocketBase admin
settings (ADR-0026). Development mode keeps the old defaults (random
per-process secrets, any public URL) and prints `WARNING` lines to stderr.
Other commands (`superuser`, `migrate`) need no env.

### Client IP

The client address comes from `MARKET_CLIENT_IP_HEADER` (default
`X-Forwarded-For` when proxies are set) only when the direct peer is in
`MARKET_TRUSTED_PROXIES`; the rightmost hop that is not a trusted proxy
wins, otherwise the peer address. Used by the YooKassa IP allowlist and the
download and consent IP hashes. Behind the Incus edge, set
`MARKET_TRUSTED_PROXIES` to that address (documentation example:
`10.0.0.2/32`).

### Release files

Storage keys (`product_releases.storage_key`) are relative. Local mode
serves `<MARKET_FILES_DIR>/<key>` on `/files/` with HMAC-signed, expiring
links (`MARKET_DOWNLOAD_SECRET`); S3 mode pre-signs
`<bucket>/<MARKET_S3_PREFIX><key>`, so one bucket can be shared with other
folders. Upload files to the same path the key names; `.` and `..` key
segments are rejected.
- Payments: `MARKET_YOOKASSA_SHOP_ID`, `MARKET_YOOKASSA_SECRET_KEY`,
  `MARKET_YOOKASSA_API_URL`, `MARKET_YOOKASSA_RECEIPTS`,
  `MARKET_YOOKASSA_REFUND_RECEIPTS`, `MARKET_YOOKASSA_CHECK_IP`;
  optional: `MARKET_PAYMENT_PROVIDERS`, `MARKET_PAYMENT_RULES`,
  `MARKET_PAYMENT_ENTITY`; development only: `MARKET_FAKE_GATEWAY`,
  `MARKET_FAKE_WEBHOOK_SECRET`.
- Mail: `MARKET_MAIL_PROVIDER`, `MARKET_MAIL_FROM`, `MARKET_MAIL_FROM_NAME`,
  `MARKET_MAIL_SENDMAIL`, `MARKET_MAIL_DIR`, `MARKET_MAIL_API_KEY`,
  `MARKET_MAIL_API_URL`.

## Real vs fake

| Area | State |
| --- | --- |
| Catalog, product pages, dark theme | Real (placeholder seed data) |
| Checkout, orders, inbox/outbox workers, refunds, ledger | Real (ADR-0007) |
| Payments: YooKassa | Adapter behind `payments.Gateway` (ADR-0025, ADR-0033). Covered by `httptest` tests; an optional smoke against the YooKassa test shop runs when `YOOKASSA_TEST_SHOP_ID` and `YOOKASSA_TEST_SECRET_KEY` are set (test keys only, never live). Agent (marketplace) receipt lines are rejected until the agent scheme is decided |
| Payments: fake gateway | `MARKET_FAKE_GATEWAY=1` emulates redirect, return and webhook; must be 0 in production |
| Mail | PocketBase mailer with the admin SMTP settings (default); `.eml` files while SMTP is off (ADR-0026) |
| Buyer cabinet | Real: magic link or password sign-in, cookie session, CSRF, orders, downloads (ADR-0027). No password set/reset screen yet; PocketBase's built-in reset API exists |
| Downloads | Signed `/dl` links to signed local files (`MARKET_FILES_DIR`, default) or S3 pre-signed URLs under `MARKET_S3_PREFIX` |
| 54-FZ receipts | Through YooKassa receipts when enabled; fake issuer only with the fake gateway; no issuer with `MARKET_YOOKASSA_RECEIPTS=0` |
| Russian texts | Not in the repo (no Cyrillic rule); only `ui/dict/en.json`. Delivery path is open question A1 |
| Vendor marketplace (phase 3) | Slice 1 real: brand config, seller profiles, author pages `/authors/{slug}`, item cards with authors, catalog search (ADR-0020). Applications, seller portal, earnings split and payouts: docs only (ADR-0020 to ADR-0024) |

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
- The outbox retries transient failures with backoff up to 8 attempts.
  `payments.ErrPermanent` and `mail.ErrPermanent` are not retried: the row
  goes `dead` at once and the worker logs it; a refund or tax document gets
  `failed` plus an alert. PocketBase mailer: SMTP 550-554 is permanent;
  SMTP off (file fallback), 4xx, auth errors and a missing sender address
  stay retryable because they are fixed in the admin UI.
- Generated files (`*_templ.go`, skin CSS, `ui/kit`) are committed; never
  edit them by hand (ADR-0014).
- No secrets, real personal data or Cyrillic in the repo (ADR-0016).
