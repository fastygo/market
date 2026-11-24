# ADR-0019: Product types as modules, plain product page by default

- Status: Accepted (owner t223u, 2026-11-24)

## Context

The engine sells digital goods whose shape is not settled (sites, themes,
plugins, software with unlock keys). The owner wants the product kind to be
a module, with a plain product page (WooCommerce-like) as the default, and
type-specific delivery added later without touching the core.

## Decision

- Core knows one generic `Product` (title, slug, summary, description,
  category, tags, media, seller, status, releases, offers) and a
  `product_type` code (default `plain`). Nothing in catalog, ordering,
  payments, ledger or licensing branches on the type.
- The default product type `plain` renders the product page with two
  actions: Download (the entitled or free release through the download
  path of ADR-0009) and Preview (`preview_url`, an external demo or a
  preview asset). No type-specific fields.
- A product type module (Go module, ADR-0017) may later provide: extra
  validated attributes (json on the product, schema owned by the module),
  a product page section and a delivery strategy behind a `Delivery`
  interface declared by `licensing` (for example S3 release + license or
  unlock token for software). The core calls the interface; `plain` is its
  default implementation.
- No product type module is built in phase 0 to 1, and adding one needs no
  core migration (the `product_type` column and a json `attributes` field
  are the only seams).

## Consequences

- Phase 0-1 ships one type and one page; unclear product shapes do not block
  the catalog.
- Download stays one path (signed, re-checked, ADR-0009) whatever the type.
