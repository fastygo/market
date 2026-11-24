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
jurisdiction (slo.md). Release files never live on the host: the app
redirects downloads to pre-signed S3 URLs (ADR-0009).
