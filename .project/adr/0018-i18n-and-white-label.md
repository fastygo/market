# ADR-0018: Locale modules and white-label branding

- Status: Proposed (owner direction 2026-11-24; Russian dictionary location
  is open question A1)

## Decision

### i18n

- All UI strings are keys (`ui.checkout.pay`, ...) in the UI8Kit
  `DictionaryV04` shape; templates never hold literals (UI8Kit rule 04).
- `locale-en` ships with the core (`ui/dict/en.json`) and is the source and
  fallback.
- Other locales are locale modules (`locale-ru`, ...): manifest
  (`provides: ["locale.dict@1"]`, locale code, key coverage) plus the
  dictionary. Default storage: the database (`ui_messages`, editable in
  admin), imported from a bundle outside this repo, because the repo has no
  Cyrillic. Alternative pending owner answer: `modules/locale-ru/` in the
  repo as the single exemption from the Cyrillic check, or a separate repo.
- Fallback chain per request: requested locale -> storefront default ->
  `en`. A coverage check reports missing keys per locale module.
- Formatting (money, dates, numbers) uses Go `golang.org/x/text` by locale;
  a `locale.format@1` plugin may override patterns as data.
- Content records (pages, posts, legal documents) are Codex entries per
  locale (a translation is another record with the same id).

### White label

- Brand = storefront config + UI8Kit skin, never a component fork:
  `storefronts` holds name, logo and favicon (media), hosts, skin id, token
  overrides (validated token names only), email sender name, operator
  reference, legal document references.
- Skins are UI8Kit skins/tokens (published UI8Kit themes or a brand skin
  package); screens read colors, radius and fonts only through tokens.
- Emails and documents use the same brand config and dictionary.
- Phase 1 runs one storefront (GoDudY reference brand) per instance; the
  model supports several storefronts per instance (host -> storefront),
  enabled later.

## Consequences

- A new brand is data and a skin; a new language is a module; neither needs
  core changes.
