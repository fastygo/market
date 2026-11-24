# Research notes (2026-11-24)

Sources read as code, not only READMEs. Commits are the heads that were read.

| Source | Ref | What it is |
| --- | --- | --- |
| `fastygo/backend` | `3278ba1` | Headless Codex BaaS: own storage adapters (bbolt, SQLite, MySQL, Postgres), identity, sessions, CSRF, media, revisions, REST/GraphQL/OpenAPI, tenants.json (~21k lines Go in `internal/`) |
| `fastygo/codex` | `ae5f746` (= tag `v0.4.2`) | Protocol-neutral content contract, zero third-party deps: `content`, `schema`, `taxonomy`, `revision`, `canonical`, `validate`, `validation`, `conformance` (~5.6k lines) |
| `fastygo/formset` | `f98b533` (main; last tag `v0.2.0` = `822d135` pins Codex v0.3.0) | Form kernel over Codex `schema.RecordType` aliases: `Bind`, `JSONSchemaFromRecord`, `ReviewSchema`, diffs. No renderer, no storage |
| `ui8kit/contract` | `5611b44` | Codex v0.4 TS contract plus the central UI8Kit rules (`.cursor/rules/00..05`, skills) |
| `codegen-x/ui8kit` | `63e01c9` | Reference kit; `packages/kit/templ` holds generated templ primitives (box, button, form, icon, image, input, link, select, ...) installed by the ui8kit CLI from `@ui8kit/registry` |
| PocketBase | `v0.40.6` (2026-10-23) | Go 1.27 min, SQLite (`modernc.org/sqlite`), goja JSVM plugin `plugins/jsvm`, `plugins/migratecmd`. Release notes say future releases move off GitHub to a new host |
| templ | `v0.3.1118` (2026-11-12) | Go HTML templating, compiled components |

## fastygo/backend: take nothing as code

- It is a complete BaaS that overlaps PocketBase one to one: storage, auth,
  sessions, CSRF, media, admin descriptors, REST. Running both means two
  sources of truth for users and files.
- Its own boundary rules exclude us: `docs/quality-attributes.md` non-goals are
  "storefront SSR, carts, checkout/payments"; `AGENTS.md` forbids product
  behavior in the backend.
- Take as patterns only:
  - capability vocabulary and `Principal` in `internal/domain/authz/authz.go`
    (resource.action strings, explicit capability set per role);
  - implicit default tenant (ADR-0011): the single seller is the same object
    as a named seller later, there is no second code path;
  - SLO/SLI table format (`docs/operations/slo.md`, `sli-catalog.md`):
    target vs implemented, owner, evidence;
  - conformance/golden tests (`internal/conformance/shop_golden_test.go`).

## fastygo/codex: take as a pinned library, narrowly

- Zero dependencies, Go 1.25, tagged `v0.4.2`; cheap to import.
- UI8Kit rule 1 (contract first) says blocks read Codex v0.4 records. So the
  SSR view-model layer projects PocketBase records into `content.Entry`,
  `taxonomy.Term`, menus and settings for UI8Kit blocks.
- `canonical.JCS` / `Digest` (RFC 8785) give a stable digest for immutable
  order and receipt snapshots and for the license file.
- `validation` codes (`New`, `Wrap`, `Issue`) give one error vocabulary for
  forms and API.
- `schema.RuleMoney` semantics (integer minor units, max 2^53-1) match our
  money rule and PocketBase's number field limit.
- Not used: `revision` and `migrate` (PocketBase is storage SoT; product
  releases are our own versioning), Codex as the storage model for commerce
  entities (orders, payments are not content entries).

## fastygo/formset: not in phase 1

- PocketBase collections are the builder for typed, indexed fields.
- FormSet is useful later for seller-defined category attributes (one JSON
  field validated against a Codex `RecordType`, JSON Schema for the editor).
- Blocker: no tag for Codex v0.4.2 yet (main only). Adopt after a tag.

## PocketBase facts that shape the design

- Extend mode: `pocketbase.New()` in our `main`, Go hooks, custom routes via
  `OnServe`, Go migrations (`migratecmd`), `RunInTransaction`, `Cron()`.
- JSVM: `jsvm.MustRegister` runs `pb_hooks/*.pb.js` in goja (ES5.1+ partial
  ES6, no npm, pooled VMs). Good for glue, weak for money logic and tests.
- SQLite only: vertical scaling, one writer. Number field max safe integer
  ~2^53-1 (v0.40.0 note). `encoding/json/v2` since v0.40.0.
- Protected file fields with short-lived file tokens exist; we still sign our
  own download URLs (ADR-0009).
