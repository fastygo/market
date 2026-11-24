# ADR-0009: Entitlements, purchase codes, signed short-lived downloads

- Status: Proposed

## Decision

- Payment success grants one `Entitlement` per order item (idempotent on
  `order_item`), with a random UUIDv4 `purchase_code` (Envato style) and
  `support_until = paid_at + support_months`.
- Entitlement covers all published releases of the product; yanked releases
  are hidden.
- Download: account page link -> handler checks entitlement `active` and
  release `published` -> 302 to `/dl/{token}` where token = HMAC-SHA256
  (entitlement, release, expiry 10 min, user). The file is streamed by Go
  (`http.ServeContent`, range support); later a pre-signed object storage URL.
- Every download is logged (`downloads`); soft limit with alert on abuse.
- Refund sets entitlement `refunded`; downloads stop.
- Phase 2: `GET /api/v1/purchase-codes/{code}` for sellers to verify buyers
  (rate limited, returns product, license type, support_until only).
- Release files are checked on upload: size limit, sha256 stored, archive
  type allowlist. Malware scan: open question for phase 2.

## Consequences

- Files are never public URLs; leaked links expire in minutes.
