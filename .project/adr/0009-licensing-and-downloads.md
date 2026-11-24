# ADR-0009: Entitlements, purchase codes, signed short-lived downloads

- Status: Proposed (revised after review 2026-11-24)

## Decision

- The `paid` transition inserts one `Entitlement` per order item in the same
  DB transaction (unique `order_item`, so a replay is a no-op), with
  `user` (always a user, no guest in phase 1), `seller`, a random UUIDv4
  `purchase_code`, `support_months_snapshot` from the order item, and
  `support_until = paid_at + support_months_snapshot`. Live offers are never
  read for this.
- Entitlement covers all published releases of the product; yanked releases
  are hidden. If every release is yanked, the entitlement stays `active`, the
  product page and account show "no download available, update pending", and
  an operator alert asks the seller for a new release (no automatic refund).
- Download: account link -> handler checks entitlement `active` and release
  `published` -> 302 to `/dl/{token}`, token = HMAC-SHA256 (entitlement,
  release, user, expiry 10 min). `/dl/{token}` re-checks entitlement `active`
  and release `published` before streaming, so a refund or dispute inside the
  token window stops the download. File streamed by Go (`http.ServeContent`,
  ranges); later a pre-signed object storage URL with the same pre-check.
- Every download is logged (`downloads`); soft limit with alert on abuse.
- Status changes: refund (phase 1: full only) -> `refunded`; dispute ->
  `disputed` (downloads stop) -> `refunded` or back to `active`; operator
  revocation command -> `revoked`. Partial refund of one digital line is not
  offered until a rule exists; with a cart, refunds attach to `order_item`.
- Release files: immutable once published (Go hook rejects file, sha256,
  size changes); a new file is a new version. Upload checks: size limit,
  sha256, archive type allowlist. Malware scan: phase 2 question.
- Phase 2: `GET /api/v1/purchase-codes/{code}` for sellers (rate limited,
  returns product, license type, support_until only).

## Consequences

- Files are never public URLs; leaked links expire in minutes and die at once
  on refund.

## Review 2026-11-24 (BalunMY)

Applied P1-1 (disputed), P1-2 (full refunds only), P1-5 (support from
snapshot), P1-6 (re-check at `/dl/{token}`), P1-8 (immutable release), P2
(all releases yanked).
