# market

A white-label commerce engine for digital goods (sites, templates, designs,
extensions, modules). First storefront: StorEEE Market (brand config, not
code); GoDudY is one of its sellers.

- Runs as a single store or a multi-vendor marketplace (commerce mode).
- Region-, locale- and brand-agnostic core; regions, locales, payment
  providers, product types and marketplace features are modules.
- Stack: PocketBase (Go extend mode) + templ SSR on UI8Kit. One Go binary at
  runtime; Node is build-time tooling only.

Design docs: [.project](.project/README.md). Local gate: `scripts/verify.sh`.
No secrets or real legal or personal data in this repo (ADR-0016).

## Run locally

```sh
make build                                   # CGO_ENABLED=0 go build ... -o bin/market ./cmd/market
./bin/market superuser upsert admin@example.com 'a-long-password'   # admin account (any time, also while running)
./bin/market serve --http 127.0.0.1:8090     # data in ./pb_data
# storefront http://127.0.0.1:8090/  /catalog  /products/aurora-theme
# admin UI   http://127.0.0.1:8090/_/  (sign in with the superuser above)
```

Migrations (collections and a placeholder seed) apply on start. Use
`--dir /path/to/pb_data` for another data directory (pass it to
`superuser upsert` too). Development flags: `MARKET_FAKE_GATEWAY=1` (fake
payment provider), `MARKET_DOWNLOAD_SECRET` (stable download links across
restarts); see `deploy/market.env.example`.

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

Phase 1 ships no real payment provider. With `MARKET_FAKE_GATEWAY=1` the
fake gateway plays the provider; downloads redirect to a locally signed URL
unless `MARKET_S3_*` is set (see `deploy/market.env.example`).

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
