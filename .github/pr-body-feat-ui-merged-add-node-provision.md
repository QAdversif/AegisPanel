# feat(ui): merge "Add node + Provision" into a single dialog

## Что внутри

Объединяет v0.8.x 2-step flow (Add node → Provision) в один dialog с optional provision секцией. Чистый UX-фикс, без backend изменений.

| Раньше | Стало |
|---|---|
| (1) Click "Add node" → fill form → save → node in `new` state | (1) Click "Add node" → fill form (create fields + optional provision fields) → save |
| (2) Click "Provision" on new row → fill auth → save | **One step. The provision runs inline if the checkbox is on** |
| 2 dialog opens + 2 form fills | 1 dialog open + 1 form fill (default) |

## UX flow

1. Operator clicks "New node" → merged dialog opens
2. Fills name, region, address, capacityHint
3. **Default**: "Provision this node after registering" checkbox is **on**; auth-method radio + key/password + ssh_user/ssh_port/tofu_policy/fingerprint fields visible
4. Submits → `createNode` then `provisionNode` in sequence → node goes straight to `online`
5. **Opt-out**: uncheck the box → form reverts to plain "Register only" (v0.8.11 behaviour preserved for operators who prefer it)

## Edge cases handled

- **Partial success**: if `createNode` succeeds but `provisionNode` fails, the operator sees a non-fatal toast "Node registered, but provisioning failed" with the API error message + a hint to retry from the row's Provision entry. The form closes either way (the operator can re-provision).
- **Per-row Provision stays**: the row's "Provision" dropdown entry still exists for re-provisioning offline nodes. It keeps the three-way radio (key/password/**stored**) — the "Stored panel key" option is enabled only for state `offline` (a previously-provisioned node). The merged dialog hides "stored" entirely (a brand-new node has no panel-stored key yet).
- **Schema enforcement**: the new `nodeAddSchema` extends `nodeCreateSchema` with the provision fields + a `provisionNow` discriminator. When `provisionNow` is `true`, the `superRefine` enforces the same XOR + conditional-required + tofu_policy rules as `nodeProvisionSchema`. The `stored` auth method is rejected at the schema level for first-time installs.

## Что в коде

| Файл | Изменение |
|---|---|
| `frontend/src/schemas/node.ts` | Новый `nodeAddSchema` (~85 lines) — extends create schema, superRefine enforces provision rules when `provisionNow` is on. `NodeAddInput` type export |
| `frontend/src/views/NodesView.vue` | `createForm` теперь использует `nodeAddSchema`. `provisionAfterCreate` ref + `setProvisionAfterCreate` setter (для template event handler). 2-step submit: createNode → optionally provisionNode. Dialog template: добавлен checkbox + auth-method radio + key/password/ssh_user/ssh_port/tofu_policy/fingerprint fields gated by `v-if="provisionAfterCreate"`. Submit button text changes: "Register and provision" (default) / "Register only" (checkbox off) |
| `frontend/src/i18n/locales/en.json` + `ru.json` | 9 новых ключей: `provisionAfterCreate`, `provisionAfterCreateHint`, `registerAndProvision`, `registerOnly`, `createdAndProvisioned`, `createdProvisionFailed`, `createdProvisionFailedHint` + 2 more |
| `CHANGELOG.md` [Unreleased] | Новая секция "Added (merged 'Add node + Provision' dialog)" |
| `KNOWN_LIMITATIONS.md` | Закрыт entry "Merged 'Add node + Provision' dialog" + новый "closed in this PR" с migration note |
| `docs/ROADMAP.md` v0.8.x row | Marked "merged 'Add node + Provision' dialog" shipped |
| `docs/README.md` + `README.md` (root) | v0.8.x-bucket updated |

## Pre-pr.sh

**10/10 ✓**: backend (gofmt/build/vet/test/golangci-lint), frontend (codegen:check/type-check/lint/build), docs markdownlint. 0 errors.

## Diff estimate

```
 CHANGELOG.md                                       |  60 +++
 KNOWN_LIMITATIONS.md                               |  20 ++
 README.md                                          |   2 +-
 docs/README.md                                     |   2 +-
 docs/ROADMAP.md                                    |   2 +-
 frontend/src/i18n/locales/en.json                  |  10 +
 frontend/src/i18n/locales/ru.json                  |  10 +
 frontend/src/schemas/node.ts                       |  87 +++++++++++++-
 frontend/src/views/NodesView.vue                   | 350 +++++++++++++++++++++++++++--
 9 files changed, 530 insertions(+), 13 deletions(-)
```

## Privacy

Никаких privacy-rule items. UX-фикс, чистый frontend, нет ни backend, ни env, ни schema изменений.
