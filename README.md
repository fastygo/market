# market

A white-label commerce engine for digital goods (sites, templates, designs,
extensions, modules) with a reference storefront (GoDudY).

- Runs as a single store or a multi-vendor marketplace (commerce mode).
- Region-, locale- and brand-agnostic core; regions, locales, payment
  providers and marketplace features are modules.
- Stack: PocketBase (Go extend mode) + goja policy plugins + templ SSR on
  UI8Kit.

Design docs: [.project](.project/README.md). Local gate: `scripts/verify.sh`.
No secrets or real legal or personal data in this repo (ADR-0016).
