# ADR-0034: Design kit generated from ui8kit/market-design

- Status: Accepted (owner t396u, 2026-11-26: "generate the set for templ go
  locally; the design repo is JS only, do not change it").
- Builds on ADR-0010 (SSR templ on UI8Kit), ADR-0018 (skins), ADR-0029
  (semantic class names), ADR-0032 (web performance).

## Context

`ui8kit/market-design` is the brand product of StorEEE Market and the
GoDudY seller workspace: a Bun monorepo where JSON specs
(`packages/specs/json/{compose,blocks}`) are the single source of truth
and the Svelte and React apps are generated from them. It has 27
compositions over the UI8Kit primitives, 73 blocks and parts and 14
screens, a shadcn token set (light and dark) and Istok Web with Cyrillic.

The storefront renders templ on the server. ADR-0029 step 2 (brand
classes on app screens) was never done: screens carried about 137
utility `Class:` literals.

## Decision

1. `scripts/designgen` (Go, build time only) reads a local checkout of
   market-design (`make design DESIGN=../market-design`; `bun install`
   there once for `lucide-static`) and writes:
   - `ui/design/*.templ`: one templ component per block and part with a
     typed `<Name>Props` struct; the data shapes of
     `packages/specs/src/types.ts` become Go structs (`types.go`);
   - `ui/design/compose.templ`: the 27 compositions for app screens
     with runtime recipe classes `mk-<name>`, `mk-<name>-<variant>`,
     `mk-<name>-<axis>-<value>`;
   - `ui/design/icons.go`: the Lucide glyphs used by the blocks plus
     `scripts/designgen/icons.txt` (screens and data);
   - `ui/styles/generated/design.css`: semantic classes with `@apply`.
2. Blocks render the UI8Kit templ primitives directly (`ui/kit/ui/*`;
   checkbox, radio, select, textarea, dialog and disclosure were added
   to `scripts/ui8kit.sh`). Each element gets one class
   `<block>-<node name>` whose rule is the composition recipe merged with
   the node classes at generation time (UI8Kit `Cn`), so the server never
   merges classes and the markup carries no utility chains. Conditional
   classes become `<block>-<node>-isN`; dynamic recipe axes enumerate
   their literal values (`<class>-<value>`). `group` and `peer` markers
   stay literal because Tailwind cannot apply them.
3. CSS layers: `components.kit` < `components.design` (`kit` mk-* <
   `blocks` < `state`) < `components.brand` (app screens,
   `ui/styles/screens.css`) < utilities. Identical rules share one
   selector.
4. JS expressions of the specs (a small subset: paths, `??`, ternaries,
   comparisons, `String()`, `.replace`, `.includes`, spreads) are
   translated to Go with types. Event handlers have no server meaning;
   they become `data-on<event>="<block>.<handler>"` (+ `data-value`) for
   the small progressive-enhancement script. Hash routes in literal
   hrefs (`#/x`) become paths. `scripts/designgen/patches.json` adds
   server-only props (form names, ids) to spec nodes without touching
   the design repo.
5. The `storeee` skin takes the market-design tokens; `godudy` and
   `default` stay. The design repo is never modified or pushed from here.
6. `scripts/verify.sh` runs `designgen -check` when a checkout exists at
   `$MARKET_DESIGN` (default `../market-design`), otherwise prints
   "not verified".

## Consequences

- A design change is: update market-design, `make design css generate`,
  commit the generated files. Generated files are never edited by hand.
- Russian copy of the design stays out of the repo (ADR-0016, no
  Cyrillic); words come from the dictionary (`ui/dict/en.json`, database
  locales per ADR-0018).
- The design CSS adds about 12 KB gzip to each skin; one class per
  element keeps HTML small and render cost flat (ADR-0029 numbers).
