# ADR-0020: Vendor model (Envato Market style): sellers, profiles, discovery

- Status: Proposed (owner direction t229u/t230u, 2026-11-24; docs only, no
  code yet)

## Context

The first deployment is branded "StorEEE Market" (the 2.0 relaunch of an
older supplier-search platform; slogan "Easy to find, Easy to learn, Easy
to Sell"). GoDudY is no longer the store brand: it is one seller (vendor)
with its own profile and portfolio. The engine stays white-label: the
StorEEE name, slogan, base URL and skin are storefront config and dictionary
entries (ADR-0016, ADR-0018), never code.

The target is an Envato Market style marketplace: many authors sell digital
items on one storefront under one operator; buyers find items by category,
tags and search, see author profiles and portfolios, follow authors, rate
and comment items; authors earn a share and are paid out. This ADR covers
who sellers are and how buyers meet them. Money is ADR-0021, item
moderation and licensing ADR-0022, referral attribution ADR-0023.

Existing decisions this builds on: `seller` column on every seller-relevant
row and Go-only money writes (ADR-0005), both sale models in the ledger
(ADR-0006), `marketplace` Go module and commerce mode (ADR-0017), region
`LegalProfile` (ADR-0015), product types as modules (ADR-0019).

## Decision

### Brand as config (StorEEE)

- `storefronts.brand` (json, validated): `name` ("StorEEE Market"),
  `short_name`, `tagline`, `alternate_names` (spellings users type, used
  only in structured data and the search synonyms list), `logo`,
  `favicon`, `skin`, `og_image`. Page titles, `og:site_name`, the
  schema.org `Organization`/`WebSite` JSON-LD and email sender name read
  from it. The exact spelling with three E's is data, entered once.
- Base URL: `storefronts.hosts` plus env `MARKET_BASE_URL` for absolute
  links (emails, sitemaps, canonical, feeds). Example deployment value:
  `https://market.storeee.ru`; it appears only in deployment config and in
  this example, never as a default in code.
- Marketing texts (tagline, home lead, meta descriptions) are dictionary
  keys (`ui.brand.*`, `ui.meta.*`) or content records, per locale.

### Seller (author) lifecycle

`sellers.status`: `draft` -> `applied` -> `active` <-> `suspended` ->
`closed`; `applied` -> `rejected` (may re-apply after a cooldown).

- Application: a signed-in user submits `seller_applications` (display
  name, desired slug, portfolio links, product categories, legal form,
  contract version accepted, consents). One open application per user.
- KYC-lite: legal data needed by the region (`LegalProfile`, RU: INN,
  OGRN/OGRNIP, legal form `ooo`, `ip`, `npd`) is admin/runtime data in
  `sellers.legal_ids` and `sellers.tax_profile`; supporting files go to
  `seller_documents` (private storage, superusers only, retention policy).
  None of it is ever in the repo, seeds or logs (ADR-0016). Automated
  checks (RU: registry lookups, self-employed status) are region pack Go
  modules called outside write transactions; results are stored as
  `verification` (json: source, checked_at, result code), never the raw
  response with personal data.
- Approval: an operator with `sellers.approve` decides; on approval the
  `sellers` row becomes `active`, `seller_members(owner)` is created, the
  contract version and sale model are snapshotted. Invariants (Go):
  `active` requires sale model, accepted contract version, and the legal
  data the region requires; `agent` only in `marketplace` mode (ADR-0005).
- Suspension (`suspended`, with `suspension_reason` code and note): new
  sales stop (offers not purchasable, items `unlisted`), profile shows a
  neutral notice, payouts are held, earnings keep accruing reversals;
  buyers keep their entitlements and downloads (a buyer is never punished
  for a seller's breach). Lifting it restores listings that were
  `published` before.
- Closure (`closed`): no new sales, profile becomes a tombstone page,
  balance is settled by the last payout after the hold period (ADR-0021);
  the row is never deleted (money rows reference it).
- Every transition is an `audit_log` entry with actor and reason.
- 289-FZ style duties (RU, when they apply): notice periods for contract
  changes and appeal of suspension are region pack rules
  (`seller.policy@1` capability: notice days, appeal window); the core only
  stores the dates and the appeal record.

### Seller types and region

Seller legal form and tax regime are region codes in `tax_profile`
(`legal_form`, `regime`), validated by the region `LegalProfile`; the core
has no country-specific columns. The region pack declares per legal form:
allowed sale models, whether payouts need a seller-issued document (for
example a self-employed receipt), and whether the operator acts as agent on
tax documents. The operator seller (`is_operator`) is the platform's own
catalog and has no earnings account.

### Public profile and portfolio

- URL: `/authors/{slug}` (path prefix is storefront config, default
  `authors`); seller portal stays `/seller/{slug}` (ADR-0005). Slugs are
  unique, lower-case, with a reserved list (`admin`, `api`, `seller`,
  `authors`, `catalog`, ...), and a change keeps a redirect
  (`seller_slug_history`).
- Profile fields on `sellers`: `display_name`, `headline`, `bio` (sanitized
  markdown), `avatar`, `cover`, `country` (optional, public), `website`,
  `links` (json, allowlisted kinds), `support_policy` (text or URL),
  `joined_at`. Contact data shown only if the seller enters it as public.
- Portfolio = the seller's `published` items (sort: featured, newest,
  best-selling) plus optional `portfolio_items` (showcase entries that are
  not for sale: title, image, url, sort) so an author can show work done
  elsewhere.
- Public counters on the profile (items, sales, followers, rating) are
  cache columns recomputed by cron, never money (ADR-0004).
- Author levels and badges: `author_levels` (code, title key, min lifetime
  earnings or sales count per currency, sort) and the cached
  `sellers.level`; badges (`exclusive`, `top_seller`, `featured_author`,
  `years_n`) are computed into `sellers.badges` (json) by a daily cron from
  the ledger and products.
- Followers: `seller_follows(user, seller)`; following feeds an outbox
  notification on new item or major update (digest, rate limited).

### Buyer interaction

- Ratings and reviews: existing `reviews` (entitlement required, one per
  product and user, rating 1..5, seller reply, moderation status).
- Item comments (public Q&A and pre-sale questions): `item_comments`
  (product, seller, user, parent, body, status, `is_author`, `is_buyer`
  snapshots). Support for buyers within `support_until` is shown on the
  comment and in the account; ticket systems are out of scope (sellers may
  link their own via `support_policy`).
- Reports: `reports` (reporter, ref_type, ref_id, reason code, note,
  status) for items, comments, reviews and profiles, handled in the
  operator queue.

### Discovery

- Categories (tree, max depth 3) and tags (normalized slug, max 15 per item,
  seller proposed, moderator editable); category `attribute_schema` stays
  phase 3 (ADR-0004).
- Search: SQLite FTS5 over item title, excerpt, tags, category title and
  author display name; synonyms list from config (brand spellings, common
  typos). Facets: category, tags, price range, rating, sales, updated,
  compatible-with (from product type attributes). Sorts: relevance, best
  sellers, newest, rating, price. Author search on `/authors?q=`.
- Featured: `features` (kind `item` or `author`, ref, slot such as
  `home_hero`, `weekly`, `category:{slug}`, starts_at, ends_at, sort),
  curated by operators; free files of the month are a slot plus a zero
  price offer, no special code path.
- Analytics for sellers: `item_stats_daily` (product, seller, day, views,
  unique_views, sales_count, earnings_minor, currency, refunds_count).
  Views are counted in memory per process and flushed once a minute in one
  short transaction (upsert per product and day); sales and earnings are
  derived from the ledger by the nightly cron. The seller portal reads these
  rows, never the ledger per request.

### Future seam: suppliers and wholesale feeds

The platform's heritage is supplier search. Importing suppliers' catalogs
(YML/XML feeds, CommerceML, APIs) with stock and prices is a future module
(`suppliers`), not designed here. Seams it will use: a seller of kind
`supplier` (profile as above), a product type module for its goods
(ADR-0019), an import job outside write transactions with per-feed
schedules, and `products.external_ref` (feed id + item id, unique per
seller). No core change is planned for it now.

### Single store keeps working

In `single_store` none of the above routes or crons is registered; the
collections exist (same schema in both modes, ADR-0017) and stay empty; the
operator seller remains the only seller. Switching to `marketplace` stays a
config change plus enabling the module.

## Collections (new or changed)

| Collection | Fields | Unique / invariants |
| --- | --- | --- |
| `sellers` (changed) | + status (`draft`,`applied`,`active`,`suspended`,`closed`,`rejected`), suspension_reason, headline, bio, avatar, cover, website, links (json), support_policy, joined_at, level, badges (json), exclusivity (`exclusive`,`non_exclusive`), items_count, sales_count, followers_count, rating_avg, verification (json) | slug unique + reserved list; `active` invariants above; counters are cache |
| `seller_applications` | user, display_name, desired_slug, portfolio_links (json), categories (json), legal_form, contract_version, status (`submitted`,`in_review`,`approved`,`rejected`,`withdrawn`), reviewer, decision_reason, decided_at | one open application per user |
| `seller_documents` | seller, kind (region code), file (private), uploaded_by, status (`received`,`accepted`,`rejected`), retain_until | superuser only; never public; deleted after `retain_until` unless the law requires keeping |
| `seller_slug_history` | seller, slug, changed_at | slug unique (no reuse by another seller) |
| `portfolio_items` | seller, title, image, url, sort | max N per seller (config) |
| `author_levels` | code, title_key, min_earnings_minor, currency, min_sales, sort | code |
| `seller_follows` | user, seller, created | (user, seller) |
| `item_comments` | product, seller, user, parent, body, status (`published`,`pending`,`hidden`), is_author, is_buyer | depth 2 |
| `reports` | reporter, ref_type, ref_id, reason_code, note, status (`open`,`actioned`,`dismissed`), handled_by | (reporter, ref_type, ref_id) |
| `features` | kind, ref_id, slot, starts_at, ends_at, sort | (slot, ref_id, starts_at) |
| `item_stats_daily` | product, seller, day, views, unique_views, sales_count, earnings_minor, currency, refunds_count | (product, day, currency) |

All seller-owned rows carry `seller` (ADR-0005). API rules: public read
only for `published`/`active` data; seller staff write only their own
profile, portfolio and comment replies through Go routes; the rest is Go or
superuser only.

## Consequences

- StorEEE is a deployment of the engine; another brand on the same code is
  a new storefront row and skin.
- GoDudY becomes seller data (profile, portfolio); its seed stays a `draft`
  seller with placeholder legal data.
- Resource fit (`slo.md`, 1 vCPU / 2 GB): profile and catalog pages read
  indexed rows and cached counters; view counting adds one batched write per
  minute; nightly crons scan the day's ledger entries only; no new
  resident caches beyond the view counter map (bounded, a few MB).
