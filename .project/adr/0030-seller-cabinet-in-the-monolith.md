# ADR-0030: Seller cabinet in the monolith; AI and heavy jobs in an isolated worker

- Status: Accepted (owner t373u, 2026-11-26). Builds on ADR-0005 (one
  SQLite database, seller-scoped rows), ADR-0009 and ADR-0022 (release
  files), ADR-0027 (buyer cabinet, session, CSRF), ADR-0028 (one account
  with role-based cabinet sections). Does not replace ADR-0028: that ADR
  still covers authoring fields, i18n and routes, and stays Proposed
  until those slices land.

## Context

The storefront is one Go process (PocketBase extend mode, SQLite WAL,
one writer). The catalog target discussed with the owner is about 10 000
products and 500 vendors. The seller cabinet needs a rich editor, later
AI help, and heavy file work (archives, previews). Those features do not
need storefront latency, which tempts a split: a second binary or a
second Incus container that opens the same database.

That split is a poor fit for SQLite. Two processes in two containers
must not open the same database file: there is one writer, and a
cross-container lock is not a supported production setup. A separate
cabinet would therefore talk to the monolith over an API anyway, adding
a second deploy, a second auth path and a network failure mode, with
almost no gain. A rich text editor is a browser island, not a reason for
a second backend. AI calls and heavy file jobs are different: they are
slow, they hold API keys, they need egress, and they can run untrusted
or bulky code. Those belong outside the storefront process.

## Decision

### 1. Cabinet UI and data stay in the monolith

The seller (and buyer) cabinet is more routes and screens in the same
binary, under the `/account` prefix of ADR-0027 and ADR-0028. Writes go
through Go handlers with membership checks. The cabinet and the
storefront share the session cookie, the CSRF scheme, and the single
SQLite writer. A slow cabinet page does not require a separate process:
it is just another handler.

### 2. AI and heavy file jobs run in an isolated Incus worker

A worker container, on the same host, handles long-running work:

- AI requests (rewrite, improve a description, later similar jobs);
- heavy file processing (archive inspection beyond the request path,
  preview generation, future image variants).

The worker never opens `pb_data` and never mounts the database file.
It talks only to an internal HTTP API on the monolith, over the Incus
bridge (or another private address), bound so Caddy does not publish
it. Authentication is a shared `MARKET_INTERNAL_KEY` (or equivalent)
known to the monolith and the worker; the key is not in the public
env of the storefront container. Worker egress is allowlisted to the
AI provider and to the object storage it needs. AI provider keys live
only in the worker.

### 3. A `jobs` table in the same database, distinct from the outbox

The payment and mail outbox (ADR-0007, ADR-0026) stays the
transactional path for effects of a money or fulfilment transition.
Long-running cabinet work uses a new `jobs` collection, for example:

- `type` (such as `improve_text`, `preview_release`);
- `status` (`queued`, `claimed`, `succeeded`, `failed`);
- `payload` and `result` (JSON);
- `seller` (required), optional `product`;
- `attempts`, `claimed_by`, `claimed_until` (lease, same idea as
  other workers);
- timestamps.

The cabinet enqueues a row in the same SQLite transaction as any
related draft write. The worker claims a job through the internal API,
does the work, and posts the result back through the API. The cabinet
polls (or later receives an event) and applies the result only after
the seller confirms it.

## Consequences

- One binary to deploy for the storefront and the cabinet; the worker
  is a second unit with a narrower attack surface and its own scale
  and restart policy.
- SQLite remains a single writer: job claims and results are ordinary
  row updates, not a second process on the file.
- The internal API and the jobs collection are new surface to design
  and test (auth, leases, idempotency, size limits on payload/result).
- Release downloads and pre-signed S3 transfers stay as in ADR-0009
  (they do not go through the app, and they do not need the worker).

## When to revisit

- A separate team wants to ship the cabinet on another stack and
  release cycle: then a cabinet SPA (or similar) may talk to the same
  monolith API; it still must not open the database file.
- The store moves to Postgres, or needs many writers / many app
  servers: a split becomes possible, but is still optional; the worker
  isolation remains the right place for AI keys and heavy jobs.
- Worker load itself needs more than one replica: that is a scale-out
  of the already isolated worker, not a reason to split the cabinet
  out of the monolith.
