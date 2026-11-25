# SLO

Targets per deployment (single node, one SQLite writer); the same table
applies to every region. Phase 1 = the RU deployment. Status
"Target" until the measurement exists; "Measured" once the SLI is on the
dashboard. Window: 30 rolling days unless noted. Time in UTC.

Rule for money-path objectives: the time limits count while the process is
up ("process-up time"). Node outages are covered by availability, RTO and the
reconciliation that runs at start-up; no objective promises 100%.

## Objectives

| SLI | Objective | Error budget / 30 d | Owner | Status |
| --- | --- | --- | --- | --- |
| Storefront availability (GET `/`, category, product; non-5xx, external probe from the deployment's region every 60 s) | 99.5% | 3 h 36 min | Operator | Target |
| SSR latency, catalog and product pages (origin, server time) | p95 <= 300 ms, p99 <= 800 ms | 5% of requests over 300 ms | Dev | Target |
| Checkout start (POST `/checkout` to provider redirect, non-5xx) | 99.5% success, p95 <= 1.5 s | 0.5% | Dev | Target |
| Webhook intake (verified event stored and 2xx returned) | 99.9% success; latency target set after the first month of measurements (single writer shared with checkout, workers, Litestream, FTS) | 0.1% | Dev | Target |
| Paid to entitlement (provider `succeeded` to `granted_at`) | 99.9% within 5 min of process-up time; the rest within one reconciliation pass after recovery | 0.1% | Dev | Target |
| Purchase email sent (entitlement to outbox `done`) | 99% within 10 min of process-up time | 1% | Dev | Target |
| Download start (authorized request to first byte, non-5xx) | 99.9% success, p95 TTFB <= 1 s | 0.1% | Dev | Target |
| Backup freshness | Litestream lag <= 5 min; full backup age <= 24 h | none | Operator | Target |
| Restore drill | RTO <= 4 h, RPO <= 15 min, quarterly | none | Operator | Target |

Excluded from error budgets: 4xx (auth, validation, 404), provider-side
outages proven by provider status (tracked as "provider availability"),
planned maintenance announced 24 h ahead (max 2 h a month).

## Tax documents: obligation, not a budget

Where the region pack requires tax documents (RU: 54-FZ receipts; EU:
invoices), issuing them is a legal duty, so it has no error budget.
Target: issued within 15 min of process-up time (RU receipts; EU invoices
within 24 h unless the pack sets a stricter rule). Any document that is
`failed` after the retry cap, or still `pending` after 1 h (RU) of process-up
time, is a page alert and an operator task the same day. Reported monthly as
a count of incidents, not as a percentage.

## Single-writer guardrails

- Webhook intake is one insert in its own short transaction.
- `busy_timeout` set; exactly one inbox worker and one outbox worker; cron
  jobs use short transactions and batch limits.
- Metrics export reads only; no write per scrape.

## How measured

- HTTP middleware records `route`, `status`, `duration` into a Prometheus
  histogram; `/metrics` behind basic auth on localhost, scraped by
  Prometheus or VictoriaMetrics.
- External probe (Uptime Kuma or a hosted RF checker) for availability.
- Business SLIs are SQL over our own tables: `payments.updated` vs
  `entitlements.granted_at`, `outbox` and `payment_events` status and age,
  `tax_documents` status age. A cron exports them as gauges every minute.
- Logs: PocketBase request logs + structured `slog` JSON; no personal data in
  logs (email hashed, IP hashed).

## Alerts

- Burn rate page: 2% of the 30-day budget burned in 1 h (14.4x).
- Burn rate ticket: 5% burned in 6 h (6x).
- Direct pages: `payment_events` not processed > 10 min; inbox or outbox row
  `dead`; paid order without entitlement or ledger txn > 15 min; tax document
  `failed`, or `pending` beyond the pack's limit; backup lag > 15 min.

## Error budget policy

When a budget is spent: feature work stops for that surface, only fixes and
reliability work, until the window is back under budget. Money paths have
priority over catalog.

## Phase 2+

Availability 99.9% after a warm standby is drilled: a Litestream replica that
can be promoted. It is still one writer, not multi-master; RTO target then
<= 30 min.

## Review 2026-11-24 (BalunMY)

Applied P1-10: removed "100% within 30 min / same day" (contradicted RTO
4 h), dropped the unmeasured webhook p95 200 ms, fiscal moved out of error
budgets (P0-5), standby described as promote, not multi-master.

## Revision 2026-11-24 (owner: region-neutral core)

SLOs are per deployment; fiscal receipts generalized to region tax documents.

## Target host and runtime profile (owner t223u, 2026-11-24)

- Host: 1 vCPU (3.3 GHz), 2 GB RAM, 30 GB NVMe, 100 Mbit/s, in the
  deployment's region.
- Runtime: one Go binary (PocketBase + templ SSR, embedded CSS and assets)
  behind Caddy; no Node, Postgres or Redis at runtime. Built off-server
  (CI or local), deployed as a binary with systemd.
- Release files: S3-compatible storage in the region, pre-signed URL
  redirect, never through the app (ADR-0009). Product images: same storage
  or the app's static path with long cache headers; phase 5 adds a CDN.
- Backups: Litestream and the daily PocketBase backup go to external
  storage, not the same disk.

### Memory budget (2 GB host)

| Consumer | Budget |
| --- | --- |
| OS, systemd, journald, sshd | 250 MB |
| Caddy | 60 MB |
| Litestream | 60 MB |
| market binary (Go heap + SQLite page cache) | soft limit 512 MiB via `GOMEMLIMIT`, alert at RSS 600 MB |
| SQLite OS page cache (database file) | the rest, about 1 GB |

GOMEMLIMIT rule: about 85% of the hard limit of the box the binary runs
in (incus `limits.memory`, systemd `MemoryMax`), so the GC tightens before
the OOM killer acts; 15% is headroom for goroutine stacks, SQLite memory
outside the Go heap and fragmentation. Example: `limits.memory=768MiB` ->
`GOMEMLIMIT=650MiB`. Without the variable the binary applies
min(512 MiB, 85% of the cgroup limit) itself and logs the value and source.
`go_gc_gomemlimit_bytes` on `/metrics` shows the effective value.

Phase 0 measures idle RSS of the binary (recorded in `plan.md`); SQLite
`cache_size` is set explicitly (start: 32 MiB). Swap: 1 GB swap file as a
safety net, alert on swap-in.

### Load test plan

- Tool: `k6` or `vegeta` run from another host (never on the target).
- Read profile: home, category, product, search mix at 20, 50 and 100 rps
  for 10 min each, warm cache. Pass: p95 < 300 ms, 5xx = 0, CPU < 70%,
  RSS within budget.
- Write profile (phase 1): checkout with the fake gateway at 2 and 5 orders
  per second plus a webhook burst of 50 events in 10 s, while the read
  profile runs at 20 rps. Pass: no `SQLITE_BUSY` surfaced to users,
  lock-wait p99 < 200 ms, webhook intake 2xx 100%.
- Soak: 2 h read profile at 20 rps; RSS stays flat (no growth > 10%).
- Run before go-live and after each minor PocketBase upgrade; results in
  `.project/loadtest/` (date, commit, numbers).
