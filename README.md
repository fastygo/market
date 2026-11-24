# market

A white-label commerce engine for digital goods (sites, templates, designs,
extensions, modules) with a reference storefront (GoDudY).

- Runs as a single store or a multi-vendor marketplace (commerce mode).
- Region-, locale- and brand-agnostic core; regions, locales, payment
  providers, product types and marketplace features are modules.
- Stack: PocketBase (Go extend mode) + templ SSR on UI8Kit. One Go binary at
  runtime; Node is build-time tooling only.

Design docs: [.project](.project/README.md). Local gate: `scripts/verify.sh`.
No secrets or real legal or personal data in this repo (ADR-0016).

## Run locally

```sh
go build -o bin/market ./cmd/market     # or: make build
./bin/market serve --http 127.0.0.1:8090
# http://127.0.0.1:8090/  /catalog  /products/aurora-theme  (admin: /_/)
```

Migrations (collections and a placeholder seed) apply on start.

## Develop

| Task | Command | Needs |
| --- | --- | --- |
| Regenerate templ | `make generate` | `templ` v0.3.1070 |
| Rebuild CSS | `npm ci && make css` | Node (build time only) |
| Reinstall the UI8Kit kit | `make ui8kit` | Node, `templ` |
| Test | `make test` | Go 1.27 |
| Gate before push | `make verify` | |

Generated code is committed (`*_templ.go`, `internal/web/static/app.css`,
`ui/kit/`), so a clean clone builds with Go alone; regenerate with the
commands above, never edit generated files (ADR-0014).
