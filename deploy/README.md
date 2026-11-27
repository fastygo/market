# Deploy

Single binary, built off-server (`make build`, `CGO_ENABLED=0`), copied to
the host with its env file. No Node, Postgres or Redis on the host.

| File | Goes to |
| --- | --- |
| `market.service` | `/etc/systemd/system/market.service` |
| `market.env.example` | `/etc/market/market.env` (fill in, mode 0600) |

```sh
useradd --system --home /var/lib/market --shell /usr/sbin/nologin market
install -m 0755 bin/market /opt/market/market
systemctl daemon-reload && systemctl enable --now market
```

Backups of `/var/lib/market/pb_data` go to external storage in the same
jurisdiction (slo.md). Release files live in `MARKET_FILES_DIR` (default
`<pb_data>/files`, include it in the backup) and are served on `/files/`
with signed, expiring links, or, with `MARKET_S3_ENDPOINT`, in S3 under
`MARKET_S3_PREFIX` with pre-signed URLs (ADR-0009). `serve` refuses to start
in production without its required env (HANDOFF.md, start-up checks).

## UI language (ADR-0018, ADR-0035)

The repository ships only the English source dictionary (`ui/dict/en.json`,
no Cyrillic in the repo). The Russian UI is a DictionaryV04 bundle kept
outside the repository (operator copy: `ru.json`, covering every key of the
current source) and imported into the `ui_messages` collection:

```sh
# on the host, as the market user, with the service env loaded
/opt/market/market i18n check ru.json            # lists missing/unknown keys, exit 1 on gaps
/opt/market/market i18n import ru ru.json --dir /var/lib/market/pb_data
# then set MARKET_LOCALE=ru in /etc/market/market.env and restart the service
```

The dictionary is read at start, so restart after every import. `import`
refuses a bundle with gaps unless `--force` is given; at start a missing key
falls back to English and the log shows `ui messages: keys fall back to
English` with the count. Re-run `i18n check` after each release that adds
keys. With `MARKET_LOCALE=ru` the page language is `ru` and prices use the
Russian format (`1 490` followed by the rouble sign). Category names, the storefront tagline,
legal page texts and products are content: edit them in the editor cabinet
or the admin, not in the dictionary.

## Stores and releases (ADR-0035)

- A store opened through a vendor application cannot sell until its VAT
  status is recorded: `market seller vat <store-slug> yes|no` (the RU tax
  policy refuses offers otherwise). The editor sees a message when publishing
  an item of such a store.
- An item goes on sale only with a published release. Vendors upload `.zip`
  releases in the cabinet (512 MB, archive checks, per-store quota) into
  `MARKET_FILES_DIR` (include it in the backup) or, with S3, into
  `MARKET_S3_PREFIX` (the access key needs PutObject, DeleteObject and
  GetObject there). Every upload waits in `/editor/releases` until an
  editor approves it (ADR-0036). The edge must accept 513 MB request
  bodies on `POST /seller/items/*/releases`.

## Edge (Caddy)

The storefront sits behind Caddy (`via: 1.1 Caddy`). A reference file is
`deploy/Caddyfile.example`. After copying it to the host:

- run `caddy validate --config ...` before reload;
- never stop or restart nftables to turn on HTTP/3: only enable `servers { protocols h1 h2 h3 }`
  when UDP/443 already answers on the public address (test with `curl --http3-only`).
  If it does not, leave HTTP/3 off.
- `header Strict-Transport-Security "max-age=15552000"` (180 days) on the
  storefront and, when the apex/www already serve HTTPS correctly, on those
  hosts too. Do not add `preload` without a separate decision.
- Unknown `Host` values must return 404 (see the catch-all in the example).
  Do not 308 them to `https://{host}/`.
- Keep `/_/` and `/api/collections/_superusers*` off the public internet
  (empty 404, no `via`). Directory listing for `/static` is disabled in
  the app.
- Caddy already encodes `zstd` and `gzip`; do not add Brotli or a CDN.
  Fonts and images are self-hosted (`/static/fonts`, `/media/covers`).

Optional on the host (not required to start the binary): Debian/Ubuntu
package `webp` (`cwebp`). When present, product-cover derivatives are
lossy WebP at quality 80; otherwise the binary writes lossless WebP
(pure Go, `CGO_ENABLED=0`). `serve` derives missing covers in the
background into `<pb_data>/media/covers` and never overwrites originals
in PocketBase storage.
