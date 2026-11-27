# ADR-0035: One cabinet, four roles

- Status: Accepted (owner t396u, 2026-11-26: "decide yourself how to organise
  the cabinet for four roles: editor, author, vendor, partner"; "one
  PocketBase instance while users are few").
- Builds on ADR-0027 (buyer cabinet), ADR-0023 (partners), ADR-0021
  (payouts), ADR-0028/0031 (seller items and EditorY), ADR-0034 (design kit).

## Decision

One `users` auth collection and one session (`market_session`) serve every
role. A user holds any set of roles at once; the role is never a field
on `users`, so a buyer who becomes a vendor keeps the purchases.

| Role    | Stored as                                        | Granted by                                   | Cabinet home        |
|---------|--------------------------------------------------|----------------------------------------------|---------------------|
| buyer   | every signed-in user                             | sign-in, guest checkout, register            | `/account/purchases`|
| vendor  | `seller_members` (seller, user, owner or staff)  | editor approves a `role_applications` row    | `/seller`           |
| author  | `user_roles` role `author` (vendors write too)   | editor approves an application               | `/studio/articles`  |
| partner | `partners` (code, parent, level_path, rate_bps)  | editor approves an application               | `/partner`          |
| editor  | `user_roles` role `editor`                       | CLI only: `market role grant <email> editor` | `/editor`           |

- Vendor is per seller entity (a team): the owner adds staff by email at
  `/seller/team`; staff cannot remove the owner. An approved vendor
  application creates the seller in `pending_review` with commission 50 %
  (ADR-0021); the store opens (status `active`) when an editor publishes
  its first product.
- `cabinet.Principal` is loaded once per cabinet request and drives the
  navigation: the header account menu and the mobile menu list one entry
  per held role; each section has its own sidebar (design SellerSidebar /
  AccountSidebar / SideNav).
- Every handler checks the role server-side (`requireRole`); there are no
  client-side checks. Every new collection has nil API rules: only the SSR
  cabinet (Go) reads and writes them. `payouts` is in `LockedCollections`.

## Content and moderation

- `articles` (draft, in_review, published, rejected, archived): authors and
  vendors write Markdown (`internal/content`, same sanitiser as product
  descriptions); a published article edited by its author goes back to
  draft; editors publish, reject with a note, or archive.
- Products: the vendor creates a draft (title, slug, category, excerpt,
  stack, tags, demo URL, regular and extended prices, cover, gallery,
  description in EditorY) and submits it; the editor publishes it or
  returns it to draft with a `review_note`.
- `pages` (legal, info) are seeded with English placeholders (terms,
  privacy, offer, refund, licenses, about, help, sell, rules, contacts) and
  edited at `/editor/pages`; they render at `/pages/{slug}`.
- `collections` are curated product lists edited at `/editor/collections`.
- `reviews`: a buyer with a paid order of the product reviews it once;
  the editor publishes and may reply; the product rating is recomputed.
- `ui_messages` (locale, key, value) holds database locales (ADR-0018);
  `market i18n import <locale> <file.json>` upserts a bundle. Russian texts
  stay out of the repository.

## Money

Payouts are data and screens only: balances are computed from paid order
lines (seller: line minus commission; partner: `rate_bps` of referred paid
orders); a payout request may not exceed the available amount; an
operator pays outside the system and marks the row `paid`. Nothing calls
a payment provider. Partner attribution: `?ref=<code>` sets a 30-day
cookie and records a click; checkout snapshots the partner into
`orders.referral`; a partner's own purchase is not attributed.

## Migration

`1764000013_roles_content` is expand-only: new collections, plus
`orders.referral` and `products.review_note`. It is idempotent (each step
checks for existing collections and fields) and does not touch existing
rows.
