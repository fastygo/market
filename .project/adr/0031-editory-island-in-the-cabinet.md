# ADR-0031: Embed EditorY as a cabinet island; store Markdown, render HTML on the server

- Status: Accepted (owner t373s1450, 2026-11-26). First slice implemented.
  Depends on ADR-0030
  (cabinet in the monolith) and ADR-0028 (item authoring). Research:
  github.com/ui8kit/editory at the date of this ADR, packages
  `editory@0.2.0`, `@ui8kit/editory-svelte@0.2.0`,
  `@ui8kit/editory-solid@0.2.1` (MIT), all already on npm.

## Context

Sellers will write longreads and long product descriptions. Today
`products.description` is plain text, at most 20 000 characters, and
the product page splits it on blank lines (`paragraphs` in
`internal/web/screens`). ADR-0028 already deferred "Codex blocks" to
later. The owner asked to embed the project's own editor,
[ui8kit/editory](https://github.com/ui8kit/editory), which already
ships as a Wails 3 desktop app.

EditorY is a framework-independent ProseMirror editor (not Tiptap,
not Lexical) with two UI8Kit shells, Svelte 5 and SolidJS. The
portable document is Markdown (CommonMark, strikethrough, and a
figure HTML block with src/alt/title/caption/credit and box /
container / full layouts). `getJSON()` is a native ProseMirror
document; block ids in that JSON are session-only and must not be
saved (they are assigned fresh when Markdown is loaded). YAML
frontmatter can carry SEO fields; unknown keys are preserved. The
widget does not store files, upload media, publish, call an LLM, or
edit tables and task lists. Unsupported Markdown (tables, task lists,
footnotes, arbitrary HTML) is refused in the visual schema and kept
in source mode. Links and image URLs are filtered
(`https`/`http`/`mailto`, no `javascript:`). The host passes
translated `LongreadLabels`; the demo chrome is not a built-in i18n
catalog. Accessibility: `role="textbox"`, `aria-multiline`, labelled
toolbar and dialogs, `@ui8kit/aria` patterns (dialog, disclosure,
tooltip, alert), checked with `ui8px validate aria`. There is no
editor SSR or hydration: `createEditor` needs a DOM and must not run
during a server render.

The desktop host (Wails 3) uses the Solid shell. Vite builds
`.wails/frontend/dist`; Go does `//go:embed all:frontend/dist` and
serves it from the webview. The UI sees a file name and a Markdown
body only; the path stays in Go. Desktop files are capped at 2 MiB
and must be UTF-8 `.txt` / `.md` / `.mdx`. The Codex cloud host
(`createCodexHost`) reads and writes `content[locale]` with
`If-Match: "vN"`; it is a pattern we can copy, not a service we will
call. Extension points for a market host: `onMarkdown`,
`onInsertReady` (insert a Markdown string), `onLocalLink`, `plugins`
(native ProseMirror plugins). Custom persisted node types are not
exposed in this version; a product-card block would need a schema and
a codec change in EditorY itself, or a conservative Markdown stand-in
(a link or figure) until then.

Measured production-like islands (Vite 6, no app chrome, no demo
fonts), gzip -9 and Brotli q11:

| Bundle | JS raw / gz / br | CSS raw / gz / br | JS+CSS gz / br |
| --- | --- | --- | --- |
| Solid island (`EditorY`) | 449 / 150 / 129 kB | 23 / 6.2 / 5.0 kB | 156 / 134 kB |
| Svelte 5 island (`EditorY`) | 461 / 155 / 133 kB | 23 / 6.2 / 4.9 kB | 161 / 138 kB |
| Core only (`createEditor`) | 355 / 124 / 106 kB | 3.8 / 1.2 / 1.0 kB | 125 / 107 kB |

The full desktop demos are larger (theme, chrome, Nunito): Svelte
174 kB gzip JS+CSS, Solid 181 kB, plus 47 kB of fonts. Most of the
weight is ProseMirror and markdown-it; the shell UI adds about 30 kB
gzip. The island is loaded only on cabinet editor pages, never on the
storefront.

The market binary already embeds `internal/web/static` (`//go:embed`)
and content-hashes skin CSS (`?v=`). There is no Content-Security-Policy
yet; the document shell has one inline theme script that must run
before first paint. CSRF is a hidden HMAC field bound to the
httpOnly `market_csrf` cookie, plus an Origin check (ADR-0027);
`checkCSRF` today reads only `PostFormValue("csrf")`. Image covers
are PocketBase file fields (5 MiB, jpeg/png/webp). `internal/upload`
covers release zip intake and quotas; it is not wired to editor
images.

## Decision

### 1. Runtime: Solid island

Ship `@ui8kit/editory-solid` (plus `editory`) as the cabinet editor.
Reasons: it is the runtime already embedded in the Wails 3 binary, so
desktop and cabinet stay on one shell; the island is slightly smaller
than the Svelte 5 build (about 5 kB gzip); market has neither
framework today, so there is no existing Svelte investment to reuse.
The core stays framework-agnostic if we ever add a second host.

### 2. Build and embed

A `make editor` (bun + Vite) builds one island entry that mounts
`EditorY`, writes hashed assets to `internal/web/static/editor/`
(for example `editory.<hash>.js` and `editory.<hash>.css`), and is
checked in, same as generated skin CSS. `//go:embed static` already
picks them up. `verify.sh` (or `make editor-check`) fails if the
committed assets are stale. The Go module does not grow a bun
dependency at `go build` time. Pin the npm versions in the market
lockfile.

### 3. Load only on cabinet editor pages, as an island

The item editor page (ADR-0028, `/account/items/...`) renders the
normal SSR form, including a `<textarea name="description">` that
holds the Markdown (this is the no-JavaScript fallback and the
regular POST field). A mount node sits beside it. The hashed module
script and editor CSS are included only on that page, not in the
storefront layout.

The island reads the textarea (and a small `application/json` props
block for labels, URLs, CSRF token, and document version — not an
executable script). It mounts `EditorY` with those labels and
`onMarkdown` writing back into the textarea. A custom element
wrapper (`<market-editory>`) is optional sugar around the same
mount. Props are not inlined as a JavaScript literal.

### 4. CSP

Add a Content-Security-Policy on every HTML response:

- `default-src 'self'`
- `script-src 'self'` plus a nonce or sha256 for the existing
  inline theme script (the only inline script we keep)
- `style-src 'self'`
- `img-src 'self' data:` and, if we allow remote figure URLs, a
  tight extra rule (prefer first-party uploaded URLs only)
- `connect-src 'self'`
- `object-src 'none'`; `base-uri 'self'`; `frame-ancestors 'none'`

No `'unsafe-inline'` and no `'unsafe-eval'` in `script-src`. Editor
assets are same-origin hashed files. If a future EditorY or Vite
release needs eval, we stop and fix the build rather than loosen CSP.

### 5. Saving, drafts, versions, size

- Canonical body is Markdown, the format EditorY already treats as
  portable. We do not persist ProseMirror JSON (session block ids).
  We do not treat HTML as the source of truth.
- The island writes the textarea and may also `fetch` an autosave
  endpoint (`PATCH`, JSON) every couple of seconds while the product
  is a `draft`. The request carries the CSRF token in a header
  (`X-CSRF-Token`, same HMAC as the form field) and `If-Match` with
  the document version, following `createCodexHost`. `checkCSRF` is
  extended to accept that header; Origin stays required for these
  requests. A 409 tells the editor the draft changed elsewhere.
- A full form POST remains valid (save, submit for review) and uses
  the existing hidden CSRF field.
- Autosave never publishes and never submits for review.
- YAML frontmatter produced by the SEO panel is stripped on the
  server; title, excerpt, slug and social fields stay in product
  (and translation) columns. A later EditorY option to hide that
  panel is welcome but not required for the first slice.
- Size cap: 200 KiB of UTF-8 Markdown per locale (above the current
  20 000 character plain-text field, well below the desktop 2 MiB
  cap). Existing shorter plain-text descriptions remain valid
  Markdown. The cap is enforced server-side on save.
- Content revisions (`product_revisions`) are a follow-up; the first
  slice keeps `If-Match` plus `updated` and the draft/in_review/
  published status already on the product.

### 6. Storefront rendering and cache

On save, in the same write transaction, the monolith parses the
Markdown with a Go CommonMark library (goldmark, with strikethrough
and a figure renderer that matches EditorY's figure HTML), then
sanitizes the output with a bluemonday allowlist: `p`, headings 1-6,
`blockquote`, `ul`/`ol`/`li`, `pre`/`code`, `hr`, `br`, `strong`/
`em`/`s`/`a` (http/https/mailto only, `rel=noopener noreferrer`),
`figure`/`figcaption`/`img`/`cite` with safe `src` (same-origin
or the image allowlist), and the figure `data-*` layout attributes.
The sanitized HTML is stored in a new `description_html` column
(and the same for translations). The product page injects that HTML
through a trusted path only after sanitization. List routes keep
skipping the body (they already do).

Re-render is a migration or a batch job when the renderer changes,
not work on the read path. Render cost of a 200 KiB body is well
under a millisecond and is paid at write time; the storefront pays
only for copying already-safe HTML. Optional later: fragment cache
of the product body, unnecessary while the HTML column exists.

`paragraphs()` remains for any record that has no `description_html`
yet, so a rolling deploy does not blank pages.

### 7. Images

The image dialog in EditorY 0.2 takes a URL; it does not upload.
The host adds a cabinet upload: `POST /account/items/{id}/images`,
CSRF, `http.MaxBytesReader`, types jpeg/png/webp (and gif if we
want animation), 5 MiB per file (same as covers), streamed and
hashed, stored as a public PocketBase file on the product (gallery
or a `product_images` field). The response is the public URL; the
island inserts it through `onInsertReady` or fills the dialog.
Per-product quota is separate from the release-zip quota in
`internal/upload` (proposal: 50 images or 100 MiB). Remote arbitrary
URLs in the body can be rejected so CSP `img-src` stays tight.

### 8. XSS and sanitization

Defense in depth: EditorY already filters clipboard and URLs; the
server is the authority. Markdown is parsed and HTML is allowlisted
on write; the storefront never renders raw seller HTML. Attributes
such as `href`, `src`, `srcset` and event handlers are not passed
through. `templ.Raw` (or equivalent) is used only on the sanitized
column.

### 9. AI worker later (ADR-0030)

An "Improve text" (or similar) control on the island enqueues a
`jobs` row (`type=improve_text`, payload = current Markdown and
locale). The isolated worker claims it, calls the model, and posts a
result Markdown through the internal API. The cabinet offers Apply,
which uses `onInsertReady` or `setMarkdown`. The editor itself still
makes no network call to a provider. This is not in the first slice.

## Consequences

- The storefront stays free of the 150 kB-gzip editor; only cabinet
  editor pages pay for it.
- `products.description` (and translation bodies) change meaning from
  plain text to Markdown; `description_html` is a derived column.
  The 20 000 character limit must be raised as part of the slice.
- New Go dependencies: a Markdown renderer and bluemonday. New
  verify checks for the committed island assets and for "no EditorY
  script on storefront pages".
- Product-card blocks, tables and collaboration wait for EditorY
  schema work, or a temporary Markdown stand-in.
- A small CSP project (nonce or hash for the theme script) lands
  with this slice and benefits every page.

## Owner decisions (t373s1450, 2026-11-26)

1. Markdown size cap: 200 KiB of UTF-8 per locale.
2. Image upload is in the first slice (5 MiB, sniffed JPEG/PNG/WebP/GIF,
   first-party URLs only).
3. Content revisions are deferred; the first slice uses `If-Match` and
   draft autosave only.
