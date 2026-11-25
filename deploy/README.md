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
