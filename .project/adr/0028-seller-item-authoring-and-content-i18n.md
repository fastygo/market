# ADR-0028: Seller item authoring, one account with role-based cabinet, content i18n

- Status: Proposed (owner t350u, t351u, 2026-11-25; docs only). Requested
  as "ADR-0021", but 0021 is already seller earnings and payouts, so this
  is the next free number. Builds on ADR-0005, ADR-0009, ADR-0018,
  ADR-0019, ADR-0020, ADR-0022, ADR-0027.

## Context

The owner asks how a seller (GoDudY first) creates a product and sets up
its card, what PocketBase (v0.40.6, `go.mod`) can do for that, how to get
a second language "in the admin", and wants a cabinet for vendors and for
buyers (t351u). State of the code on 2026-11-25 (`238c02d`):

### What exists

| Area | Today |
| --- | --- |
| `products` | seller (relation, required), slug, product_type, category (single), title, excerpt, description (plain text, 20000), status (`draft`,`in_review`,`published`,`unlisted`,`archived`), cover (one image, 5 MB), demo_url, attributes (json), rating/sales counters, published_at |
| `offers` | product, seller, license_type, price_minor, currency, tax_category, support_months, active; unique (product, license_type, currency) |
| `product_releases` | product, seller, version, changelog (editor), storage_key (text, not a PB file), size, sha256, status (`draft`,`published`,`yanked`), released_at; file fields immutable once published (Go guard) |
| `categories` | slug, title, sort, parent, attribute_schema; flat use in the UI |
| `sellers` | slug, display_name, headline, bio, avatar, website, joined_at, status, legal fields (private) |
| Tags, gallery, compatibility, translations | none (tags are in ADR-0004/0020 only) |
| API rules | catalog collections have `nil` rules = superuser only; the SSR reads server-side. Money collections are locked by Go guards even for superusers |
| Seller cabinet | none. There is no link between a `users` account and a seller (`seller_members` of ADR-0020 is not built) |
| Buyer cabinet (ADR-0027) | `/account` sign-in (password or magic link), `/account/orders`, `/account/orders/{number}` with download buttons for active entitlements, sign out. No profile page, no password set/reset screen, no "downloads" list across orders, no locale switch |
| Authoring today | only the PocketBase admin UI `/_/` as a superuser |

### PocketBase facts that shape the decision

- The dashboard is a superuser tool. Superusers bypass all API rules
  ("The API Rules are ignored when the action is performed by an authorized
  superuser", pocketbase.io/docs/api-rules-and-filters/); there are no
  per-collection admin roles, so a seller can never be given `/_/` access
  limited to their own items.
- The dashboard is English only. The maintainer has stated there are no
  plans for admin UI i18n (github.com/pocketbase/pocketbase issues #330,
  #3798, PR #85 referenced there). A community fork with translations
  exists (4444TENSEI/pocketbase-ui-i18n) but is a separate frontend, not
  the embedded UI of our binary; we do not adopt it.
- API rules can express ownership through relations, for example
  `seller.members.user ?= @request.auth.id` or
  `@collection.seller_members.user ?= @request.auth.id &&
  @collection.seller_members.seller ?= seller`, plus `:changed`/`:isset`
  guards on fields (pocketbase.io/docs/api-rules-and-filters/). We still
  write seller changes through Go routes (ADR-0020), not open REST rules.
- Files (pocketbase.io/docs/files-handling/): a non-protected file field is
  served by `/api/files/{collection}/{record}/{file}` to anyone who knows
  the URL; checked in v0.40.6 `apis/file.go`: the view rule is consulted
  only for `Protected` fields. So covers and avatars are already public
  even with `nil` rules (the "covers need a public rule" gap of ADR-0020
  slice 1 is not real). Thumbs (`?thumb=WxH`) work for jpg/png/gif and
  partly webp. Default 5 MB per file; storage is `pb_data/storage` or the
  PB S3 setting (Dashboard > Settings > Files storage).
- Protected file fields need a short-lived file token and the view rule;
  fine for private seller documents, not for multi-GB release archives
  (they would pass through the app; slo.md forbids it, ADR-0009).

## Decision

### 1. One account, one cabinet, role-based sections (owner t351u)

Like Envato: one `users` account; being a seller is a membership, not a
second login. The cabinet lives under one prefix and shows sections by
role:

| Role (how it is known) | Sections |
| --- | --- |
| Buyer (any signed-in user) | Purchases and downloads, Orders, Profile (name, email, password, locale, marketing consent) |
| Seller (row in `seller_members` for an `active` or `draft` seller) | + Items (list, create, edit, submit), Releases (versions, changelog, file upload), Sales and earnings (read-only, ADR-0021), Payouts (ADR-0021, after counsel), Public profile (display name, headline, bio, avatar, links), Become an author (for buyers without a seller: `seller_applications`, ADR-0020) |
| Operator (superuser, later an `operators` role) | Moderation queue (ADR-0022) in SSR later; at first the PocketBase admin `/_/` |

- Routes: keep `/account` for the buyer part (ADR-0027, already linked
  from purchase mail); seller part under `/account/items`,
  `/account/sales`, `/account/profile/author`. One session cookie, one
  CSRF scheme, one layout with a side navigation that lists only allowed
  sections. (`/dashboard/*` was considered; one prefix avoids two
  sign-ins and two cookie scopes.)
- New collection `seller_members` (seller, user, role `owner`/`staff`,
  created): unique (seller, user). The seller part checks membership in Go
  for every request; every write sets `seller` from the membership, never
  from the form.
- Gaps in the buyer part to close in the same slice: profile page and
  password set/change (PocketBase reset API exists, no screen), a flat
  "Downloads" list over all active entitlements, locale switch, empty
  states, receipts link (54-FZ, when real receipts exist).

### 2. Item authoring flow

Phase A (now, operator in `/_/`, single seller GoDudY). Steps per item:

1. `categories`: create the category if missing (slug, title, sort).
2. `products`: seller = GoDudY, slug (lower-case, unique), product_type
   `plain`, category, title, excerpt (one line for cards), description
   (plain text, paragraphs on blank lines), cover (jpg/png/webp up to 5 MB,
   4:3 for cards), demo_url (live preview), status `draft`.
3. `product_releases`: product, seller, version (semver), changelog,
   status `draft`. Upload the archive to the release storage outside PB:
   S3 bucket under `MARKET_S3_PREFIX` (or `MARKET_FILES_DIR` locally) at
   the key you then write to `storage_key`; fill size and sha256; set
   `published` (immutable afterwards).
4. `offers`: product, seller, license_type `regular`, price_minor (kopecks,
   for example 290000 = 2 900 RUB), currency `RUB`, tax_category
   `standard`, support_months, active = true. Price 0 is rejected at
   checkout today (`ordering.ErrFree`, "phase 2"), so free items cannot be
   delivered yet.
5. `products.status = published` and published_at.

Phase B (seller cabinet, SSR, UI8Kit rules: Block roots, primitives only,
dictionary texts, 8+4 inside controls only):

- `/account/items`: list with status chips (draft, in review, published,
  rejected with reason), create button.
- Item editor, one page with sections, saved as `draft`: title, excerpt,
  description (plain text now; Codex blocks later), category, tags
  (max 15), cover, gallery (up to 10 images), live preview URL,
  compatibility and other attributes from the product type schema
  (ADR-0019, `attribute_schema`), price for the license (within
  `price_rules`), support months; per locale tabs (section 4).
- Release form: version, changelog, archive upload by pre-signed S3 PUT
  (browser -> S3 directly; the app only signs and then verifies size and
  sha256), never through the app (ADR-0009, ADR-0022).
- Submit -> `submissions` (ADR-0022): `in_review`; operator approves or
  rejects with reason codes; only approval publishes. In `single_store`
  the operator may publish directly.
- Writes go through Go handlers (`internal/web`, `internal/authoring`
  domain package), not REST rules: the seller never edits status,
  counters, rating, seller, or published releases.

Envato comparison (author upload):

| Envato author flow | Market today | Phase B |
| --- | --- | --- |
| Name, description | title, excerpt, description (plain) | same + per locale |
| Thumbnail and preview images | one cover | cover + `gallery` (multi file field) |
| Main file (zip) | `storage_key` typed by hand | pre-signed upload + verified sha256 |
| Category, tags | single category, no tags | category + `tags` (json list, normalized) |
| Compatibility, attributes | `attributes` json, unused | form from `attribute_schema` |
| Version, changelog | releases (admin only) | release form, semver check |
| Live preview URL | demo_url | same |
| Price | offers (admin) | price field, validated |
| Review status, reasons | none | `submissions` queue (ADR-0022) |

### 3. Storage and gaps

- Covers, gallery, avatars: PocketBase file fields (public URLs, thumbs).
  Keep them in `pb_data/storage` (backed up by restic) or switch PB file
  storage to S3 in its settings later.
- Release archives: private S3 under `MARKET_S3_PREFIX`, never public;
  buyers get signed `/dl` -> pre-signed GET (already built, ADR-0009).
- Gaps to fix before sellers self-serve: no pre-signed PUT upload; no
  `gallery` and `tags` fields; free items blocked by `ErrFree` (decide:
  free download via a zero-price entitlement without payment); no
  `seller_members`; no `submissions`; release `storage_key` is free text.

### 4. Content i18n (second localization)

Two different things:

- (a) Admin UI language: not possible in the embedded PocketBase dashboard
  (above). Our own SSR cabinet (sections 1 and 2) is localized through the
  dictionary like the storefront; that is where sellers and later
  operators work. `/_/` stays an English tool for the operator.
- (b) Content language of items and pages. The SSR already has a
  dictionary layer (`ui/dict/en.json`, `i18n.Dict`, fallback to the key);
  the Russian dictionary goes to the database `ui_messages` (ADR-0018, A1).
  There is no locale routing, no hreflang and no translated content yet.

Options for item texts:

| Option | Pros | Cons |
| --- | --- | --- |
| Per-locale columns (`title_ru`, `title_en`, ...) | simple queries, admin shows them | schema change per language, many columns, not white-label |
| JSON field `i18n` on the product: `{"ru": {"title":..,"excerpt":..,"description":..}}` | one field, any locale, one row read, fits PB admin (json editor) and Go validation | search and unique checks need extracted columns; JSON editing in `/_/` is raw |
| `product_translations` collection (product, locale, title, excerpt, description, slug, status) | clean per-locale moderation and FTS5 per locale, ADR-0018 style (a translation is a record) | one more join, more rows to manage |

Recommendation: the base columns hold the storefront default locale
(source); other locales go to `product_translations` (unique product +
locale), with the same pattern for categories and seller bio
(`category_translations` or a shared `translations` table keyed by
ref_type, ref_id, locale, field). Reasons: per-locale review in the
submission queue, FTS5 per locale, no schema change per language, and the
seller cabinet edits it as tabs. JSON `i18n` stays the fallback if the
owner wants the simplest possible first step.

Routing: default locale at `/`, others under a prefix (`/en/...`), chosen
per storefront `locales`; `hreflang` alternates and `x-default` on every
page, canonical per locale, `og:locale`. Missing translation falls back to
the source text and is not listed in that locale's sitemap. The seller
fills the source language first (required) and optional tabs for other
locales; a submission covers changed translations too.

## Consequences

- No seller ever gets PocketBase admin access; the operator keeps `/_/`.
- One cabinet means one session and one place to grow (buyer -> author).
- New collections: `seller_members`, `submissions` (ADR-0022),
  `product_translations` (or `translations`); new product fields
  `gallery`, `tags`. All seller writes are Go routes with ownership checks.

## Decisions needed from the owner

1. One account with sections under `/account` (recommended) vs separate
   `/dashboard` for sellers.
2. Content i18n: translations collection (recommended) or JSON `i18n`
   field; which locale is the source (ru) and the second one (en).
3. Locale routing: Russian at `/`, English at `/en/` (recommended)?
4. Free items: allow 0 RUB downloads without payment (needs an
   entitlement path) or keep paid only.
5. Moderation in `single_store` now: operator publishes directly
   (recommended) or the queue from day one.
