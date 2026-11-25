# market

A white-label commerce engine for digital goods (sites, templates, designs,
extensions, modules). First storefront: StorEEE Market (brand config, not
code); GoDudY is one of its sellers.

- Runs as a single store or a multi-vendor marketplace (commerce mode).
- Region-, locale- and brand-agnostic core; regions, locales, payment
  providers, product types and marketplace features are modules.
- Stack: PocketBase (Go extend mode) + templ SSR on UI8Kit. One Go binary at
  runtime; Node is build-time tooling only.

Design docs: [.project](.project/README.md). New to the repo: [HANDOFF.md](HANDOFF.md).
Local gate: `scripts/verify.sh`.
No secrets or real legal or personal data in this repo (ADR-0016).

## Run locally

```sh
make build                                   # CGO_ENABLED=0 go build ... -o bin/market ./cmd/market
./bin/market superuser upsert admin@example.com 'a-long-password'   # admin account (any time, also while running)
./bin/market serve --http 127.0.0.1:8090     # data in ./pb_data
# storefront http://127.0.0.1:8090/  /catalog  /products/aurora-theme  /authors/godudy
# admin UI   http://127.0.0.1:8090/_/  (sign in with the superuser above)
```

Migrations (collections and a placeholder seed) apply on start. Use
`--dir /path/to/pb_data` for another data directory (pass it to
`superuser upsert` too). Without `MARKET_FAKE_GATEWAY=1` (fake payment
provider) or `MARKET_DEV=1` the server runs in production mode and refuses
to start without its secrets and `MARKET_PUBLIC_URL` (HANDOFF.md, start-up
checks); see `deploy/market.env.example`.

### Windows (PowerShell)

Needs Go (version in `go.mod`); no C compiler (pure Go SQLite). The CSS and
`*_templ.go` files are committed, so Node and templ are not needed to run.

```powershell
git clone https://github.com/fastygo/market.git; cd market
$env:CGO_ENABLED = "0"
go build -o bin/market.exe ./cmd/market
.\bin\market.exe superuser upsert admin@example.com "a-long-password"
$env:MARKET_FAKE_GATEWAY = "1"               # optional: fake payment provider
.\bin\market.exe serve --http 127.0.0.1:8090  # data in .\pb_data
# storefront http://127.0.0.1:8090/   admin UI http://127.0.0.1:8090/_/
```

`$env:` variables last for the PowerShell session; set them again in a new
window.

## Try the money path locally (fake gateway)

With `MARKET_FAKE_GATEWAY=1` the fake gateway plays the provider (same
redirect, return and webhook sequence as YooKassa); downloads redirect to a
signed `/files/` URL served from `pb_data/files` unless `MARKET_S3_*` is set
(see `deploy/market.env.example`).

```sh
MARKET_FAKE_GATEWAY=1 MARKET_DOWNLOAD_SECRET=dev ./bin/market serve
B=http://127.0.0.1:8090
curl -X POST $B/api/collections/users/records -H 'Content-Type: application/json' \
  -d '{"email":"buyer@example.com","password":"buyer-pass-123","passwordConfirm":"buyer-pass-123"}'
TOKEN=$(curl -s -X POST $B/api/collections/users/auth-with-password -H 'Content-Type: application/json' \
  -d '{"identity":"buyer@example.com","password":"buyer-pass-123"}' | jq -r .token)
curl -X POST $B/checkout -H "Authorization: $TOKEN" -H 'Idempotency-Key: demo-1' \
  -d '{"product":"aurora-theme","method":"card","consents":{"terms":"1"}}'   # -> confirmation_url /fakepay/{id}
curl -X POST $B/fakepay/{id}/succeed                                          # provider side: paid + webhook
curl -H "Authorization: $TOKEN" $B/checkout/return/{order_id}                 # -> "status":"paid"
curl -i -H "Authorization: $TOKEN" $B/products/aurora-theme/download          # 302 /dl/{token}
curl -i $B/dl/{token}                                                         # 302 pre-signed URL
```

### In the browser

Open `/products/aurora-theme`, enter an email in the buy form and accept the
terms: the fake payment page opens, "Pay" returns to
`/checkout/return/{order}`. The purchase mail (with a cabinet magic link)
is sent with the SMTP account from the admin UI (Settings, Mail
settings), the same one PocketBase uses for its verification and reset
mail; while SMTP is off it lands in `pb_data/mail/*.eml` (ADR-0026). The
cabinet lives at `/account` (orders, downloads, sign out).
Set `MARKET_SESSION_SECRET` and `MARKET_PUBLIC_URL` for stable links.

## Mail (ADR-0026)

Recommended for self-hosting: the default `MARKET_MAIL_PROVIDER=pocketbase`.
In the admin UI open Settings, Mail settings, enable SMTP and enter the
server, port, user and password; set the sender name and address under
Settings, Application (or `MARKET_MAIL_FROM` and `MARKET_MAIL_FROM_NAME`).
Use "Send test email" there to check the account. Purchase and sign-in
mail of the shop and PocketBase's own verification, password reset and OTP
mail then share that SMTP account. Without SMTP the shop writes `.eml`
files to `pb_data/mail` and logs a warning. Hosts without SMTP egress can
use `unisender` or `resend` with `MARKET_MAIL_API_KEY`.

## YooKassa (ADR-0025)

1. In the YooKassa personal account choose "Add store" and create a test
   store.
2. Integration, API keys: issue a secret key. Put the shop id and the key
   into `MARKET_YOOKASSA_SHOP_ID` and `MARKET_YOOKASSA_SECRET_KEY` (env or
   secrets file, never the command line).
3. Integration, HTTP notifications: URL
   `https://<public host>/hooks/pay/yookassa`, events `payment.succeeded`,
   `payment.canceled`, `refund.succeeded`.
4. Settings, Online cash register: enable the receipt test mode, or run with
   `MARKET_YOOKASSA_RECEIPTS=0`.
5. Set `MARKET_PUBLIC_URL`; behind a proxy or tunnel also
   `MARKET_TRUSTED_PROXIES=<proxy CIDR>` (the header defaults to
   `X-Forwarded-For`). Test card
   5555 5555 5555 4477 (any future date, any CVC).

Going live changes only the shop id and the secret key.

## Develop

| Task | Command | Needs |
| --- | --- | --- |
| Regenerate templ | `make generate` | `templ` v0.3.1070 |
| Rebuild CSS | `npm ci && make css` | Node (build time only) |
| Reinstall the UI8Kit kit | `make ui8kit` | Node, `templ` |
| Test | `make test` | Go 1.27 |
| Gate before push | `make verify` | |

Generated code is committed (`*_templ.go`, `internal/web/static/skins/*.css`,
`ui/kit/`), so a clean clone builds with Go alone; regenerate with the
commands above, never edit generated files (ADR-0014).
