# ADR-0027: Buyer cabinet, sessions and SSR checkout

- Status: Accepted (owner t236u, 2026-11-24). Code: `internal/account`,
  `internal/web/account.go`, `internal/web/screens/{account,checkout}.templ`,
  `internal/platform/pb/account.go`.

## Context

Buyers need a place to see orders and download purchases that is separate
from the PocketBase admin UI (`/_/`). Checkout must work from the product
page without JavaScript, for signed-in buyers and for guests.

## Decision

- Accounts are PocketBase `users` records. Sign in by password or by a
  magic link sent by mail. The session cookie `market_session` holds the
  PocketBase auth token: httpOnly, SameSite=Lax, Secure when the public URL
  is https, 7 days. The JSON API still accepts the `Authorization` header.
- Magic links: HMAC-SHA256 over `v1|user|expiry|next` with
  `MARKET_SESSION_SECRET` plus the user's token key, so rotating the token
  key (password change) revokes links. `next` is confined to `/account`.
  Sign-in links live 20 minutes, links in purchase mail 72 hours. Using a
  link marks the email verified. Requests for unknown emails get the same
  answer (no account enumeration).
- CSRF: random httpOnly cookie `market_csrf` plus a hidden HMAC field on
  every form, and an Origin check against `MARKET_PUBLIC_URL`.
- Pages (SSR templ, UI8Kit primitives only, dictionary texts, dark theme):
  `/account` (sign in), `/account/orders`, `/account/orders/{number}` with
  download buttons only for active entitlements (refunded and revoked show
  the status instead), sign out (POST).
- SSR checkout form on the product page: signed-in buyer, or guest email.
  A guest gets an account created (unverified, no password) and is not
  signed in; the purchase mail carries the cabinet magic link. The form
  idempotency key is generated per render, so a double submit is one order.
- `/checkout/return/{order}` renders an HTML status page for browsers; the
  order link is shown only to the signed-in owner.
- Local release files (`MARKET_FILES_DIR`) are served by `/files/` behind a
  signed, expiring URL for development and small installs; S3 stays the
  production path (ADR-0009).

## Consequences

- `MARKET_SESSION_SECRET` must be stable across restarts, otherwise links
  and CSRF tokens become invalid.
- Password setup for guest accounts (reset flow) is a follow-up.
