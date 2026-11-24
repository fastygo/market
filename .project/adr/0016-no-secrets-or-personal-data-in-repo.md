# ADR-0016: No secrets, legal-entity data or personal data in the repo

- Status: Accepted (owner rule 2026-11-24)

## Decision

- The repository, seeds, fixtures, docs and examples contain no secrets
  (API keys, webhook secrets, passwords, private keys, tokens), no legal
  identifiers of real entities (INN, OGRN, KPP, VAT ids, company numbers,
  bank accounts, IBAN), no real addresses, emails or phone numbers, and no
  personal data.
- Where values are needed:
  - secrets and provider credentials: env (`MARKET_*`), documented in
    `deploy/env.example` with placeholders;
  - operator and seller legal data, support contacts, legal page texts:
    admin data entered on each deployment;
  - seeds: only neutral data (slugs, display names such as the brand
    GoDudY, license type codes); legal fields empty, status `draft`, so a
    seller cannot sell until an operator fills them in admin.
- Examples use reserved placeholders: `example.com` / `example.org` emails,
  `+00 000 000 0000` phones, `0000000000` ids, `<provider-secret>` keys.
- `scripts/verify.sh` greps tracked files for obvious patterns (private key
  blocks, live/test API key prefixes, AWS keys, emails outside reserved and
  noreply domains, `inn`/`ogrn`/`kpp`/`iban` followed by digits, phone-like
  `+` numbers, committed `.env` files) and for Cyrillic. A hit fails the
  gate; false positives are fixed by using placeholders, not by allowlists,
  except the explicit domain allowlist in the script.

## Consequences

- The repo can be public and reused by any operator.
- Real data never needs history rewriting.
