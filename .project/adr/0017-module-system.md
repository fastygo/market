# ADR-0017: Module system: Go modules for money, goja plugins for policy

- Status: Proposed (owner direction 2026-11-24: commerce engine)

## Context

`fastygo/market` is a commerce engine (Medusa-like) plus a reference
storefront (GoDudY). Region, locale and brand specifics must be modules, not
core code. Money paths must stay typed, tested Go (ADR-0011, review P0).

## Decision

### Two module kinds

| Kind | Runs as | Used for | Trust |
| --- | --- | --- | --- |
| Go module | compiled into the binary, registered in `cmd/market` | money-critical ports: payment `Gateway` adapters, tax document issuers (fiscal port implementations), storage, notification transports | full; reviewed Go code |
| Plugin (goja) | JS files run in the engine's own sandboxed goja pool | declarative region and locale policy: tax rate tables and rules, consent and legal document lists, tax document field mapping (receipt or invoice fields), checkout field requirements, legal id validation rules, locale formatting | none: pure functions returning data |

A region pack (ADR-0015) is a bundle: one plugin module (`region-ru`) for
rules and mappings, plus the Go modules it needs (for RU: one provider
adapter that implements `InlineDocumentGateway`). Locales are modules too
(ADR-0018).

### Manifest (`module.json`, same fields for Go modules as a Go struct)

```json
{
  "id": "region-ru",
  "version": "1.0.0",
  "engine": ">=0.1.0 <0.2.0",
  "kind": "plugin",
  "provides": ["tax.rules@1", "taxdocs.mapping@1", "consent.docs@1", "legal.profile@1", "checkout.fields@1"],
  "requires": ["locale.format@1?"],
  "after": [],
  "entry": "plugin/index.js",
  "config_schema": "config.schema.json",
  "tests": "tests/"
}
```

- Capabilities are versioned contracts (`tax.rules@1`): Go owns the input and
  output types and their JSON Schema; a breaking change is `@2`, both can be
  supported during migration.
- `version` is semver; `engine` is the supported engine range; a mismatch
  fails start-up.
- Cardinality per storefront: singular capabilities (`tax.rules`,
  `taxdocs.mapping`, `legal.profile`, `checkout.fields`) have exactly one
  provider; plural ones (`payment.gateway`, `consent.docs`) may have several.
  Conflicts or a missing required capability fail start-up.
- Load order: core, then Go modules, then plugins in topological order of
  `requires`/`after`; cycles fail start-up.
- Config: per storefront in `storefronts.modules` (`{ "region-ru": {...} }`),
  validated against `config_schema`. Secrets are never module config: a
  module names the env variables it reads (ADR-0016).
- Enabled modules and their versions are logged and exposed in `/readyz`
  details for support.

### Plugin sandbox (goja)

- A separate goja runtime pool owned by `internal/modules`, not PocketBase's
  `pb_hooks` VM: no `$app`, no DAO, no `$http`, no `require` of PocketBase
  bindings, no filesystem, no network, no timers.
- Pure functions: `export function resolveLine(input) { return {...} }`.
  Input is JSON built by Go (including `now`, so results are deterministic);
  output is JSON decoded into Go types and validated (schema + Go
  invariants, e.g. rate within 0..10000 bp, known modes). Invalid output is
  an error, never a default.
- Limits: per-call timeout (default 50 ms, `vm.Interrupt`), input size cap,
  output size cap, pool size; a plugin that times out or panics fails the
  call and is counted in metrics.
- Plugins never write collections, never call providers and never see
  secrets. Go applies the returned data inside the normal transaction.
- Contract tests: each plugin ships `tests/*.json` (input -> expected
  output). They run in `go test` and as a start-up self-test; a failing
  required plugin blocks start-up.

### Commerce mode: single-store or marketplace

`storefronts.commerce_mode` (`single_store` | `marketplace`) is config; the
marketplace features are the Go module `marketplace` (capability
`commerce.marketplace@1`). The schema is the same in both modes: the
`seller` column is always present and always filled.

| | `single_store` (default, no module) | `marketplace` (module enabled) |
| --- | --- | --- |
| Sellers | one implicit default seller created from the operator (`sellers.is_operator = true`) | many sellers; the operator seller may still sell |
| Sale model | always `principal` | per seller: `principal` or `agent` |
| Onboarding, seller portal, moderation queue | off (routes not registered) | on |
| Commission, `seller_payable`, payouts, agent reports | off (no postings to those accounts) | on |
| Tax documents | operator as seller | agent data on `agent` lines |
| Legal (RU) | plain store (not a 289-FZ platform) | platform duties per region module |

Rules: core code never branches on the mode for money math; posting rules
and document payloads branch on the line's `sale_model`, which is always
`principal` in `single_store`. Switching `single_store -> marketplace` is a
config change plus enabling the module, with no migration; the reverse is
allowed only while no non-operator seller has orders.

### What stays Go-only

Checkout protocol, payment state machines, inbox/outbox, reconciliation,
ledger and posting rules, entitlements, downloads, authz, write protection,
document issuing calls. Plugins may influence amounts only through data that
Go validates (tax rate and mode per line), never by computing totals.

## Consequences

- A new region or locale is a module, usually without recompiling (plugins)
  unless it needs a new provider (Go module).
- Policy changes (a tax rate) are a plugin version bump with contract tests.
- Two goja runtimes exist (PocketBase hooks for glue, the engine sandbox for
  policy); ADR-0011 rules apply to both.
