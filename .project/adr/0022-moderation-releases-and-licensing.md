# ADR-0022: Item submission, moderation queue, releases and licensing

- Status: Proposed (owner direction t229u, 2026-11-24; docs only)

## Context

Envato-style quality depends on review before sale: every new item and
every update is checked by a reviewer, with reasons a seller can act on.
Releases are already immutable once published (ADR-0009), products have
`in_review` and license types exist (`regular`, `extended`). Product types
are modules (ADR-0019), so checks and license sets may differ per type.

## Decision

### Submissions

- A seller works on a `draft` product and its `draft` release; "submit"
  creates a `submissions` row (kind `new_item`, `update` for a new release,
  `metadata` for a change of title, description, category, media or price
  outside the auto-approved fields). Only one open submission per product.
- States: `submitted` -> `in_review` -> `approved` | `soft_rejected` |
  `hard_rejected`; `submitted`/`soft_rejected` -> `withdrawn`.
  `soft_rejected` means "fix and resubmit" (a new round on the same
  submission, `round` + 1); `hard_rejected` closes the item (resubmission
  only as a new item after a cooldown).
- Queue: reviewers claim the oldest submission per category with a lease
  (`claimed_by`, `claimed_until`), the same lease pattern as the workers;
  SLA target per kind is config (shown to sellers as an estimate).
- Decision: reason codes from `rejection_reasons` (code, title and help as
  dictionary keys, `hard` flag, category scope) plus a free note; on
  approval the product becomes `published` (new item) or the release becomes
  `published` (update) and buyers following the item get a notification.
- Automated pre-checks run before a submission enters the queue, outside
  write transactions: archive type and size limits, sha256, duplicate hash
  across sellers (flag), required fields per product type, preview URL
  reachable. Results are stored on the submission (`checks` json). Malware
  scanning is an external scanner behind a port (phase 3c question).
- Release files are uploaded to private S3 storage through pre-signed PUT
  URLs (never through the app, slo.md); the release row stores the key,
  size and sha256 after the upload is verified.

### Releases

- `product_releases.status`: `draft` -> `in_review` -> `published` ->
  `yanked`; `in_review` -> `rejected`. File, size and sha256 are immutable
  from `in_review` on (the Go hook of ADR-0005 extends to `in_review`).
- Version is semver, increasing per product; changelog required for
  updates. Buyers always get all `published` releases (ADR-0009).
- A seller may yank a release; yanking the last published release keeps
  entitlements and shows "update pending" (ADR-0009).

### Licensing

- `license_types` stay global codes with versioned terms; a product type
  module declares which license types it allows (default `plain`: `regular`
  and `extended`). `license_types` gains `product_types` (json list) and
  `terms_url`/`terms` per locale via content records.
- An offer is (product, license type, currency); the seller prices each
  license within `price_rules` (ADR-0021). The license terms version is
  snapshotted on the order item (already in ADR-0004).
- Purchase codes and the license verification API stay as ADR-0009
  (phase 2, sellers read their own items only).

### Moderation of other content

Profiles, comments and reviews use the same operator queue view (by
`reports` and `pending` statuses), without `submissions` rows. Every
moderator action is in `audit_log`.

## Collections (new or changed)

| Collection | Fields | Unique / invariants |
| --- | --- | --- |
| `submissions` | product, seller, release (for `update`), kind (`new_item`,`update`,`metadata`), status, round, payload (json: proposed metadata diff), checks (json), claimed_by, claimed_until, reviewer, reasons (json list of codes), note, decided_at | one open submission per product; transitions by Go only |
| `rejection_reasons` | code, title_key, help_key, hard (bool), categories (json), active | code |
| `product_releases` (changed) | + status `in_review`, `rejected`; submission | immutability from `in_review` |
| `products` (changed) | + exclusive (optional per-item), external_ref (supplier seam, ADR-0020); status keeps `draft`,`in_review`,`published`,`unlisted`,`archived` and adds `rejected` | |
| `license_types` (changed) | + product_types (json) | code |

## Consequences

- Nothing reaches buyers without an approved submission in `marketplace`
  mode; in `single_store` the operator publishes directly (no queue routes),
  as today.
- Reviewer work is a lease, so two reviewers never decide the same item.
- Resource fit: pre-checks read archive metadata with ranged reads from
  storage, one at a time in a background job; queue pages are indexed
  queries on `(status, kind, created)`.
