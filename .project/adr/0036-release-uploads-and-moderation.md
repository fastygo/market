# ADR-0036: Vendor release uploads with manual moderation

- Status: Accepted (owner t414u-t419u, 2026-11-27: vendors attach the
  sellable file in the cabinet; "no antivirus", manual moderation instead;
  moderation lives in the market, not in the PocketBase admin; the admin
  UI stays a fallback).
- Builds on ADR-0009 (downloads), ADR-0022 (releases and licensing),
  ADR-0035 (cabinet roles). Supersedes the "S3: the operator uploads
  release files" rule of ADR-0035.

## Decision

### Upload (vendor, `POST /seller/items/{id}/releases`)

- The item page has a "Releases" panel: the list (version, file, size,
  date, status, moderator note) and a form (version, changelog, `.zip`).
  A new item shows a hint instead: save the card first, the next page
  has the form.
- The body is streamed (`multipart.Reader`), never parsed into memory:
  the fields come first in form order (CSRF token, version, changelog),
  so the CSRF check, store membership and version (format and duplicate)
  are verified before a byte of the file is read. The file goes to a temp
  file on disk (`<MARKET_FILES_DIR>/.uploads` locally, `<pb_data>/uploads`
  with S3) while SHA-256 and size are computed. Body cap 512 MB + 1 MB
  (`http.MaxBytesReader`); the PocketBase 32 MB default body limit is
  unbound for this one route (`web.LargeBody`); read and write deadlines
  are extended to 30 minutes for this request only. Stale temp files are
  removed at start.
- `upload.Inspect` refuses only what is unsafe for the platform: not a
  zip, too many entries, zip bombs (declared and real decompressed size,
  ratio), path traversal, absolute or drive paths. It also writes the
  report kept on the row (`manifest`): the file list (first 500 entries
  with sizes, totals) and content flags.
- Storage key: `<seller-slug>/<item-slug>/<item-slug>-<version>.zip`
  (segments reduced to `[a-z0-9._-]`, never `.` or `..`; the file name is
  what the buyer saves). Objects are never overwritten: local mode links
  the temp file with an exclusive create; S3 mode sends a signed `HEAD`
  then one signed `PUT` (SigV4 header auth, the signed payload hash is
  the file SHA-256, so the storage rejects a corrupted body) with
  `Content-Type: application/zip` and `Content-Disposition: attachment`.
  An in-process key claim stops two parallel uploads of one version (one
  process per `pb_data`).
- Then `pb.Releases.Create` inserts the row in `review` inside the quota
  transaction. If the insert fails (quota, duplicate, any error) the
  stored object is deleted, so no orphan file stays behind.

### Flags, not verdicts

No antivirus. Inspect flags, never blocks:

- `executable`: extensions `.exe .dll .msi .bat .cmd .com .scr .ps1 .vbs
  .jar .apk .app .sh .so .dylib .bin .elf`;
- `binary`: ELF, PE (MZ header with a PE signature) and Mach-O magic;
- `secret`: credential files (`.env*` except examples, `.npmrc`, `.netrc`,
  SSH keys, `.pem/.key/.p12/.pfx`, ...) and patterns in the first 2 MB of
  each text entry (private key blocks, AWS, GitHub, GitLab, npm, Slack,
  Stripe live and Google API keys, JWTs, `password = ...` assignments).

`Limits.DenyExt` still rejects extensions when a deployment sets it; the
default is empty.

### Statuses and moderation

`product_releases.status`: `review` (new upload) -> `published` |
`rejected`; `published` -> `yanked`. `draft` stays valid for older rows
(migration `1764000014` moves drafts with a file to `review`).

- Moderators are users with the `editor` role (`market role grant <email>
  editor`) and PocketBase superusers who sign in to the market with the
  same verified e-mail. They work at `/editor/releases` (queue with tabs
  per status) and `/editor/releases/{id}`: item, vendor, version, size,
  SHA-256, flags, the archive listing, a download link
  (`/editor/releases/{id}/file`: the local file, or a 2-minute pre-signed
  S3 URL) and Approve / Reject. A rejection needs a note; the vendor sees
  it on the item page. `reviewed_by` (moderator e-mail) and `reviewed_at`
  are stored on the row, `released_at` on approval; the log has
  `release moderated` and `release file opened by moderator`.
- Vendors cannot publish. They delete a release in `review`, `rejected` or
  `draft` (row and file) and withdraw (yank) a published one, except the
  only published release of an item with active entitlements (buyers
  would lose the download): publish a newer version first.
- Buyers download only `published` releases (unchanged queries); an item
  goes on sale only with one (ADR-0035 guard).
- The PocketBase admin (tunnel only) remains the fallback: changing
  `status` there works, the immutability guard on published and yanked
  rows still applies.

### Deleting an item (2026-11-27)

- A vendor deletes an item for good from the cabinet only when it is
  `draft` or `archived` and no order line, entitlement or review
  references it; otherwise the item page explains why and archiving stays
  the way to take it down. The confirm page (`GET /seller/items/{id}/delete`)
  needs a ticked checkbox (no JavaScript), the POST is CSRF-checked and the
  user must be a member of the item's store.
- One transaction deletes the releases, the offers and the product
  (PocketBase removes the cover and gallery files after the commit and
  clears the item from editorial collections). The release files are
  removed after the commit; a file the storage refuses to delete is logged
  as `release file orphaned` with its key and the vendor is told some files
  were left. No row ever points at a missing file.

### Storage permissions

S3 mode needs `s3:PutObject`, `s3:DeleteObject` and `s3:GetObject` (HEAD
and pre-signed GET) on `<bucket>/<MARKET_S3_PREFIX>*` for the key in
`MARKET_S3_ACCESS_KEY`. No new variables.

## Consequences

- One `PUT` per release (S3 single-part limit 5 GiB, far above the 512 MB
  cap). The edge proxy must allow request bodies of 513 MB and long
  uploads (Caddy has no body limit by default).
- A moderator downloads and opens the archive on a workstation: flagged
  files are not proof of malware and an empty flag list is not proof of
  safety.

## Later

- Automated validation of archives in an isolated BuildY lab (unpack,
  build, scan and preview in a sandbox, report attached to the release)
  before the moderator decides. Not built; the `manifest` report is where
  its results would go.
