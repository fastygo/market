# SLO

Targets for production, phase 1 (single node). Status "Target" until the
measurement exists; "Measured" once the SLI is on the dashboard. Window:
30 rolling days unless noted. Time in UTC.

## Objectives

| SLI | Objective | Error budget / 30 d | Owner | Status |
| --- | --- | --- | --- | --- |
| Storefront availability (GET `/`, category, product; non-5xx, external probe from RF every 60 s) | 99.5% | 3 h 36 min | Operator | Target |
| SSR latency, catalog and product pages (origin, server time) | p95 <= 300 ms, p99 <= 800 ms | 5% of requests over 300 ms | Dev | Target |
| Checkout start (POST `/checkout` to provider redirect, non-5xx) | 99.5% success, p95 <= 1.5 s | 0.5% | Dev | Target |
| Webhook intake (verified event stored and 2xx returned) | 99.9%, p95 <= 200 ms | 0.1% | Dev | Target |
| Paid to entitlement (provider `succeeded` time to `entitlements.granted_at`) | 99.9% within 2 min, 100% within 30 min (reconciliation) | 0.1% | Dev | Target |
| Purchase email sent (entitlement to outbox `done`) | 99% within 10 min | 1% | Dev | Target |
| Fiscal receipt registered (payment success to receipt `registered`) | 99.9% within 15 min, 100% same day | 0.1% | Operator | Target |
| Download start (authorized request to first byte, non-5xx) | 99.9% success, p95 TTFB <= 1 s | 0.1% | Dev | Target |
| Backup freshness | Litestream lag <= 5 min; full backup age <= 24 h | none | Operator | Target |
| Restore drill | RTO <= 4 h, RPO <= 15 min, quarterly | none | Operator | Target |

Excluded from the error budget: 4xx (auth, validation, 404), provider-side
outages proven by provider status (counted separately as "provider
availability"), planned maintenance announced 24 h ahead (max 2 h a month).

## How measured

- HTTP middleware records `route`, `status`, `duration` into a Prometheus
  histogram; `/metrics` behind basic auth on localhost, scraped by
  Prometheus or VictoriaMetrics.
- External probe (Uptime Kuma or a hosted RF checker) for availability.
- Business SLIs are SQL over our own tables: `payments.updated` vs
  `entitlements.granted_at`, `outbox` attempts and age, `fiscal_receipts`
  status age. A cron job exports them as gauges every minute.
- Logs: PocketBase request logs + structured `slog` JSON; no personal data in
  logs (email hashed, IP hashed).

## Alerts (burn rate)

- Page: 2% of the 30-day budget burned in 1 h (fast burn, 14.4x).
- Ticket: 5% burned in 6 h (6x).
- Direct alerts: any `payment_events` unprocessed > 10 min; any paid order
  without entitlement > 30 min; any receipt `failed`; backup lag > 15 min.

## Error budget policy

When a budget is spent: feature work stops for that surface, fixes and
reliability work only, until the 30-day window is back under budget. Money
paths (webhook, entitlement, receipt) have priority over catalog.

## Phase 2+ targets

Availability 99.9% after a warm standby with Litestream restore is drilled.
