# ADR-0003: fastygo reuse: Codex yes, backend no, FormSet later

- Status: Proposed

## Context

Read at `fastygo/backend 3278ba1`, `fastygo/codex v0.4.2 (ae5f746)`,
`fastygo/formset f98b533` (details in `../research.md`).

## Decision

- `fastygo/backend`: not imported, not deployed. It is a full BaaS overlapping
  PocketBase, and its own non-goals exclude storefront SSR, carts and
  checkout. Reused as patterns: capability authz, implicit default tenant,
  SLO/SLI format, golden conformance tests.
- `fastygo/codex` pinned at `v0.4.2`, used only in:
  - `web/viewmodel`: project records to `content.Entry`, `taxonomy.Term`,
    menus, settings for UI8Kit blocks (UI8Kit rule 1, contract first);
  - `canonical.JCS`/`Digest` for order snapshot and license file digests;
  - `validation` codes for user-facing issues;
  - `schema.RuleMoney` semantics in category attribute schemas (phase 3).
  Not used: `revision`, `migrate`, Codex as storage for commerce entities.
- `fastygo/formset`: not in phase 1. PocketBase fields are the builder for
  typed columns. In phase 3 seller category attributes live in one JSON field
  validated by a Codex `RecordType` through FormSet, once FormSet is tagged
  against Codex v0.4.2.

Balance rule: a field we filter, sort, index, price or join on is a
PocketBase column (migration). A descriptive, category-specific attribute is
JSON validated by Codex schema. Never both for the same field.

## Consequences

- One small, dependency-free fastygo import; no second storage or auth.
- Codex version bumps follow UI8Kit contract bumps; pin, no `replace`.
