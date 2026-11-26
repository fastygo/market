# ADR-0032: Storefront web performance (LCP, cache, WebP, fonts)

- Status: Accepted (owner t374u, 2026-11-26): implement the performance
  plan from t372u-t373u and deploy to mkt-vps. Fonts and images stay
  self-hosted; no external CDN. Images are resized and served as WebP
  with the original as `<picture>` fallback. Builds on ADR-0018 (skins),
  ADR-0020 (brand), ADR-0029 (semantic CSS).

## Context

Lab and third-party measurements (GTmetrix Vancouver, Lighthouse from
an EU box) showed a fast SSR (~2 ms render on mkt-vps) but:

- TTFB of ~960 ms from North America is almost entirely RTT to the
  Moscow origin (Caddy, TLS 1.3, HTTP/2). The app is not the bottleneck.
- Catalog and product LCP on mobile is the cover PNG (1600x1200,
  ~250 KB) marked `loading="lazy"`, with no `fetchpriority` and no
  srcset.
- CSS and self-hosted fonts were cached for only one hour and fonts
  had no cache-busting hash, so a long `immutable` cache was unsafe.
- Catalog mobile CLS (0.187) came from Istok Web swapping in
  (`font-display: swap`) without fallback metrics.
- `/robots.txt`, `/sitemap.xml` and `/favicon.ico` 404ed. HSTS was
  `max-age=300`. HTML had no `Server-Timing` for the render slice.

The audience is primarily Russian, so a global CDN in front of the
origin was rejected (it can worsen the local path). Static assets stay
on the same host.

## Decision

1. **LCP covers.** The first visible cover (product hero, first catalog
   card in the viewport) is `loading="eager"` and `fetchpriority="high"`,
   and is preloaded in `layout.templ`. Other cards stay lazy. Home-page
   cards stay lazy (below the fold).
2. **WebP ladder.** An idempotent `internal/media` store writes 400 / 800
   / 1200 WebP variants next to the original PocketBase file (never
   overwritten). Markup uses `<picture>` with a WebP `srcset` and the
   original as the `<img>` fallback. Encoding prefers lossy `cwebp`
   (quality 80) when the CLI is installed, otherwise a pure-Go lossless
   encoder so the production binary stays `CGO_ENABLED=0`.
3. **Cache.** `/static` responses with a matching `?v=<12-hex>` digest
   (skins already, fonts after `scripts/fontver`) are served
   `public, max-age=31536000, immutable`. Fonts are fingerprinted in the
   compiled CSS at build time. Variants under `/media/covers/.../<hash>/`
   use the same immutable header.
4. **Fonts.** Preload the Istok Web files used on first paint (400/700
   latin and cyrillic). A metric-matched `Istok Web Fallback` face
   (`size-adjust`, `ascent-override`, `descent-override`) reduces CLS
   from `font-display: swap`. No runtime font or image CDN.
5. **Chrome hygiene.** `/robots.txt`, `/sitemap.xml`, `/favicon.svg` and
   `/favicon.ico`. Catalog card titles are `h2` when the grid has no
   section heading (the page `h1` names the list). Contrast tokens on
   the storeee and godudy skins meet WCAG AA for body text, links and
   buttons (theme-specific `--primary` / `--primary-foreground`).
6. **Proxy.** Caddy `Strict-Transport-Security` is raised to
   `max-age=15552000`. HTTP/3 is enabled only if UDP/443 actually
   answers on the host; otherwise it stays off. Early Hints, Brotli and
   a CDN are not used (origin think-time is ~2 ms; gzip already matches
   zstd on the CSS; audience is local).
7. **Server-Timing.** HTML responses include `Server-Timing: render;dur=`
   in milliseconds (the templ render slice already reported to metrics).

## Consequences

- `cwebp` (Debian/Ubuntu package `webp`) is optional but recommended
  on mkt-vps so covers encode lossy; without it, variants are lossless
  WebP and still smaller than the 1600x1200 PNG after resize.
- `make css` runs `scripts/fontver` after Tailwind. `verify.sh` checks
  the hashes when the Tailwind CLI is installed.
- A new cover triggers a re-derive on record save; `serve` also walks
  existing product covers at start (idempotent, background).
- Critical CSS was skipped: extracting a Tailwind kit subset is not
  cheap, and the CSS is already ~13 KB gzip. Revisit after images and
  font preload if LCP is still CSS-bound.
