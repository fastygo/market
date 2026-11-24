# ADR-0010: SSR with templ on UI8Kit templ primitives, Codex view models

- Status: Proposed

## Context

UI8Kit rules live in `ui8kit/contract` (`AGENTS.md`, `.cursor/rules/00..05`).
templ is one of the 8 UI8Kit runtimes; generated templ primitives exist in
`codegen-x/ui8kit packages/kit/templ` and are installed by the ui8kit CLI.

## Decision

- Copy `.cursor/rules` and `.cursor/skills` of `ui8kit/contract` into this
  repo as is (rule source stays upstream).
- Three layers, mapped to folders:
  - `ui/kit`: templ primitives from the ui8kit CLI. Kit zone, never edited,
    never scanned.
  - `ui/blocks` (prototypes): product card, price box, license picker, release
    list, review item... Every rule incl. 8+4 (ui8px gate `max: 0`).
  - `ui/layouts`: header, footer, filters, facets, card grid, pagination,
    account shell. Same strictness as blocks.
  - `internal/web/screens` (app): pages assembled from layouts + blocks + data;
    brand style free, no 8+4; retag, ARIA, tokens, Lucide, texts still apply.
- Every block opens on `Block`, one per render path, never in a loop (a
  repeated card is its own file). Primitives only, `tag` retag, `Title as`.
- Data contract: handlers build view models from Codex v0.4 records
  (`content.Entry`, `taxonomy.Term`, menus, settings) plus typed commerce view
  structs (price, offer, entitlement). Texts from the dictionary
  (`DictionaryV04` shape) or props; no literals in templates.
- Locales: `en` dictionary in repo as the source; other locales (Russian for
  the RU deployment) are content: a `ui_messages` collection editable in
  admin, importable from a deployment content bundle kept outside the repo.
  Legal pages are `pages` records referenced by `storefronts.legal_docs`.
- Money and tax are rendered from `amount_minor`, `currency` and the
  storefront locale (`golang.org/x/text/currency`), never with a hardcoded
  symbol or tax label; tax labels come from the dictionary per region.
- No SPA. Progressive enhancement with small vanilla JS where needed (search
  suggestions, license picker); forms work without JS. CSRF on all POSTs.
- Caching: `ETag` on catalog pages; product pages cacheable 60 s at the proxy
  for anonymous users.

## Consequences

- Same blocks can be exported to other UI8Kit runtimes later.
- `ui8px` reads `.templ` and `.go` natively, so the 8+4 gate runs without an
  extract step.

## Revision 2026-11-24 (owner: region-neutral core)

Locale texts are content in the database; money formatting by currency and
locale.
