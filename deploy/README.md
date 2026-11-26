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

## Edge (Caddy)

The storefront sits behind Caddy (`via: 1.1 Caddy`). A reference file is
`deploy/Caddyfile.example`. After copying it to the host:

- run `caddy validate --config ...` before reload;
- never stop or restart nftables to turn on HTTP/3: only enable `servers { protocols h1 h2 h3 }`
  when UDP/443 already answers on the public address (test with `curl --http3-only`).
  If it does not, leave HTTP/3 off.
- `header Strict-Transport-Security "max-age=15552000"` (180 days). Do not
  add `preload` without a separate decision.
- Caddy already encodes `zstd` and `gzip`; do not add Brotli or a CDN.
  Fonts and images are self-hosted (`/static/fonts`, `/media/covers`).

Optional on the host (not required to start the binary): Debian/Ubuntu
package `webp` (`cwebp`). When present, product-cover derivatives are
lossy WebP at quality 80; otherwise the binary writes lossless WebP
(pure Go, `CGO_ENABLED=0`). `serve` derives missing covers in the
background into `<pb_data>/media/covers` and never overwrites originals
in PocketBase storage.
