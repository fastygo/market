# Medusa v2 reference (generated)

Machine-readable summary of the Medusa v2 commerce data model, used as a
reference for our PocketBase collections (see `../medusa-mapping.md`).

- Source: https://github.com/medusajs/medusa, tag `v2.23.0` (2026-11-20),
  commit `6369161c5ee22c1ffc92859838d6e72b54de3fc4`.
- NOTICE: derived from Medusa, Copyright (c) 2021 Medusajs, MIT License.
  Medusa's repository also contains Enterprise Edition materials (RBAC, SSO
  / OIDC) under a separate commercial license (`ENTERPRISE-LICENSE.md`);
  those paths are excluded by the generator and nothing from them is here.
- Generator: `scripts/medusa-ref.mjs` (Node, no dependencies).

| File | Content |
| --- | --- |
| `models.json` | module -> model -> fields (type, relation target, enum values, nullable, default, id prefix) from `packages/modules/*/src/models` |
| `links.json` | module links (cross-module link tables) from `link-modules/src/definitions` |
| `providers.json` | `AbstractPaymentProvider` methods, `ITaxProvider` methods, payment status enums |
| `workflows.json` | workflow ids by area from `core-flows` |
| `routes.json` | Store and Admin HTTP routes with verbs from `packages/medusa/src/api` |

Regenerate:

```sh
git clone --depth 1 --branch v2.23.0 --filter=blob:none --sparse https://github.com/medusajs/medusa.git /tmp/medusa
cd /tmp/medusa && git sparse-checkout set --no-cone '/LICENSE' '/packages/modules/*/src/models/**' \
  '/packages/modules/link-modules/src/definitions/**' '/packages/core/utils/src/**' \
  '/packages/core/types/src/payment/**' '/packages/core/types/src/tax/**' \
  '/packages/core/core-flows/src/**/workflows/*.ts' '/packages/medusa/src/api/store/**/route.ts' \
  '/packages/medusa/src/api/admin/**/route.ts'
node scripts/medusa-ref.mjs /tmp/medusa .project/reference/medusa
```

Counts at this tag: 26 modules, 124 models, 35 links, 254 workflows, 305
routes (48 store, 257 admin). Known parser limits: three enums declared as
TypeScript unions have no values (index and search modules, not relevant).
