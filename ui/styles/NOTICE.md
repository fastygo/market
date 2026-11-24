# Style sources

- `kit.css` and `skins/default.css` (was `theme.css`, the neutral default
  skin): copied verbatim from `ui8kit/site` (`generator/assets/styles/`,
  commit `fe247e6`, MIT License, UI8Kit). Do not edit.
- `skins/godudy.css`: GoDudY skin written by the owner (t224u, 2026-11-24),
  kept verbatim, including `--spacing: 0.24rem` (see `.project/plan.md`,
  ui8px note).
- `fonts.css`: self-hosted `@font-face` for Istok Web, Fenix and Fira Code
  (SIL OFL 1.1; files in `internal/web/static/fonts/`, latin, latin-ext,
  cyrillic, cyrillic-ext subsets), so the skin needs no runtime CDN.
- `entries/<skin>.css`: Tailwind v4 build entries (skin, fonts, kit CSS,
  sources), compiled to `internal/web/static/skins/<skin>.css` by
  `make css` (build time only).
