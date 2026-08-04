# feat(ui): NodesView "Rotate panel key" button (v0.8.3 item 2, HTTP mirror of #184)

## What this PR ships

v0.8.3 deferred two pieces of work from PR #184: the
HTTP mirror of the `aegis admin node rotate-panel-key`
CLI, and an admin UI button for it. This PR closes
both — the v0.8.3 item 2 follow-up.

- **New endpoint** `POST /api/v1/nodes/{id}/rotate-panel-key`.
  Same handler signature shape as the v0.3.0
  `POST /{id}/provision` (mounted under the same `{id}`
  subrouter with `auth.RequireScope(ScopeNodes)` already
  enforced by the parent nodes router). Takes the
  operator's existing private key (PEM, no passphrase)
  and optional `ssh_port` / `ssh_user` overrides.
  Returns the new public key line and SHA256 fingerprint
  in the 200 body so the operator can verify the
  rotation in the UI.
- **"Rotate panel key" dropdown entry in the NodesView**
  per row, with a `RefreshCw` icon. Visible for
  `online` / `offline` / `draining` / `disabled`; hidden
  for `new` (the panel cannot SSH into a never-installed
  node because no key is in `authorized_keys`).
- **"Rotate panel key" dialog** mirroring the provision
  dialog's PEM-textarea + ssh_port + ssh_user fields.
  On success the dialog swaps to a read-only "rotation
  result" card showing the new public key line + SHA256
  fingerprint (the operator can copy the fingerprint
  before closing).

## Why a separate endpoint (not the CLI)

The v0.8.3 CLI's raison d'être was "no UI, no
endpoint, give the operator a way to back-fill legacy
nodes". With the v0.8.4 endpoint + button the CLI
becomes the operator-side fallback (e.g. scripted
batch rotation), the UI button is the primary path.
The two call sites share the same
`bootstrap.Service.RotatePanelKey` method; the wire
format is identical.

## Implementation notes

- **`bootstrap.Service.RotatePanelKey` now returns
  `(RotationResult, error)`** (was just `error`).
  `RotationResult` carries the new public key line
  and `ssh.FingerprintSHA256(pub)` so the HTTP
  handler can surface both in the 200 body. The
  shared `generateAndPushKey` (used by both
  `RotatePanelKey` and the v0.8.1 password-install
  post-install hook) gets the same signature change.
  The CLI's call-site (`runAdminNodeRotatePanelKey`)
  is updated to ignore the result via `_, err :=`.
  The post-install hook discards it the same way.
- **`newSSHClientForRotate` indirection** in
  `bootstrap/handler.go`: a package-level variable
  that defaults to `NewClient` and is overridden by
  the unit tests via a `withMockSSHClient(t, mock)`
  helper. Avoids a real SSH dial in unit tests.
- **`bootstrapProvider` interface in
  `nodes/handler.go`** adds `HandleRotatePanelKey()`.
  The router mounts it on the same conditional as
  `/provision` (both routes live or both are 404).
- **Audit**: the handler records `node.rotate-panel-key`
  via `audits.RecordFromRequest` AFTER the row's
  ciphertext is persisted (after-commit ordering;
  same as the other v0.7+ write paths). The
  `After.fingerprint` field mirrors the response
  body so the operator can grep the audit log for a
  specific rotation.
- **OpenAPI**: new `NodeRotatePanelKeyRequest` /
  `NodeRotatePanelKeyResponse` schemas. The generated
  `frontend/src/types/api.d.ts` carries the new types
  automatically.
- **zod schema for the rotate form**:
  `nodeRotatePanelKeySchema` in
  `frontend/src/schemas/node.ts` (the `ssh_private_key`
  field is required, the overrides are optional with
  the same 1..65535 port range as the provision
  schema).
- **i18n**: 12 new strings per locale
  (`rotate` / `rotateTitle` / `rotateDescription` /
  `rotateSshPrivateKey` / `rotateSshPrivateKeyHint` /
  `rotateAction` / `rotateResultTitle` /
  `rotateResultHelp` / `rotatePublicKeyLine` /
  `rotateFingerprint` / `rotated` / `rotateFailed`).
  The Russian translations follow the v0.8.x "operator
  voice, not translator voice" style (e.g. «Ключ
  панели сменён на {name}», not «Смена ключа панели
  для узла {name} выполнена успешно»).
- **Existing `NodeProvisionRequest` in
  `frontend/src/types/aegis.ts`**: the v0.3.0 canonical
  "required key" shape was made optional in v0.8.1
  (the `authMethod: 'stored'` path sends an empty
  auth object). The v0.8.4 PR fixes the duplicate
  interface: the canonical v0.8.1+ shape is the
  primary `NodeProvisionRequest`; the v0.3.0 "required
  key" variant is renamed to `LegacyV030NodeProvisionRequest`
  and marked `@deprecated`. The v0.3.0 codegen
  consumers still type-check (both shapes are
  structurally compatible on the wire — the v0.8.1+
  form's `ssh_private_key?: string` accepts both
  defined and undefined values).

## Why `isRotatable(state) !== 'new'`

A node in `new` state has never been installed — its
`~/.ssh/authorized_keys` is empty. The panel SSHes in
using the operator's PEM, but the panel has no record
of the operator's PEM on the node (the v0.8.1+
post-install hook only runs after a successful
password-based install). The CLI does not enforce the
state machine either, but the UI's dropdown is the
operator's first impression of "what does this button
do?" — showing it for `new` and then failing with 502
"ssh connect: no such host key" would be a worse UX
than not showing it at all. The state `draining` and
`disabled` are valid rotation sources: the operator
may want to rotate a key on a node they later intend
to re-enable.

## Tests

- **7 new unit tests for `HandleRotatePanelKey`** in
  `backend/internal/bootstrap/handler_rotate_panel_key_test.go`:
  200 happy path (asserts the public key line is
  non-empty, the fingerprint starts with `SHA256:`,
  the mock SSH client received at least one Upload +
  one Run call), 400 missing key, 400 malformed JSON,
  404 node not found, 500 envelope not configured,
  502 SSH connect failure, audit shape on success
  (asserts the `node.rotate-panel-key` action, the
  resource_id matches the node UUID, the
  `After.fingerprint` field is populated, the
  `ActorID` comes from the JWT claims).
- **2 existing tests updated** for the new
  `(RotationResult, error)` signature:
  `TestRotatePanelKey_NilEnvelopeFailsClosed` and
  `TestRotatePanelKey_NilClientFailsClosed` in
  `backend/internal/bootstrap/rotate_panel_key_test.go`.

## Error mapping

| HTTP | When                                                                              |
| ---- | --------------------------------------------------------------------------------- |
| 400  | malformed UUID, missing `ssh_private_key`, malformed JSON body                    |
| 404  | node row not found                                                                |
| 500  | panel booted without an envelope (set `AEGIS_WEBHOOKS_SECRET_AGE_*` and retry)     |
| 502  | SSH-side failure (Connect / Upload / Run / `SetSSHPrivateKeyCiphertext`)          |
| 401  | missing or invalid bearer token (enforced by the parent nodes router)             |

## Files

- `backend/internal/bootstrap/handler.go` (+259):
  `rotatePanelKeyRequest` /
  `rotatePanelKeyResponse` types, `HandleRotatePanelKey()`
  handler, `newSSHClientForRotate` indirection.
- `backend/internal/bootstrap/handler_rotate_panel_key_test.go` (new, ~270):
  7 unit tests.
- `backend/internal/bootstrap/rotate_panel_key.go`:
  `RotatePanelKey` now returns `(RotationResult, error)`;
  `RotationResult` struct; `generateAndPushKey` returns
  `(RotationResult, error)` (the public key line +
  fingerprint are surfaced from the gen step).
- `backend/internal/bootstrap/provisioner.go`:
  `buildPersistentSSHKeyHook` discards the new result.
- `backend/internal/bootstrap/rotate_panel_key_test.go`:
  2 existing tests updated for the new signature.
- `backend/cmd/aegis/admin_node.go`:
  CLI's `runAdminNodeRotatePanelKey` ignores the result.
- `backend/internal/nodes/handler.go`:
  `bootstrapProvider` interface adds `HandleRotatePanelKey()`;
  router mounts it on the same conditional as `/provision`.
- `docs/openapi.yaml` (+169):
  `/nodes/{id}/rotate-panel-key` path +
  `NodeRotatePanelKeyRequest` / `NodeRotatePanelKeyResponse`
  schemas.
- `docs/api/index.md`:
  v0.8.4 paragraph, "rotate-panel-key" row in the
  endpoints table, the "later version" section update
  (the CLI is the operator-side fallback now).
- `docs/ROADMAP.md`:
  v0.8.2 status ✅, new v0.8.3 row ✅, new v0.8.4 row
  ⏳, the v0.8.x "rotate-panel-key CLI + admin UI
  button" item is split out (CLI lands in v0.8.3, UI
  lands in v0.8.4).
- `CHANGELOG.md`:
  `[Unreleased]` section with the v0.8.4 entries.
- `frontend/src/api/services/nodes.ts`:
  `rotateNodePanelKey(id, req)` function
  (returns `NodeRotatePanelKeyResponse`).
- `frontend/src/schemas/node.ts`:
  `nodeRotatePanelKeySchema` (zod).
- `frontend/src/types/aegis.ts`:
  `NodeCreateRequest`, `NodeProvisionRequest` (v0.8.1+
  canonical), `NodeProvisionResponse`,
  `NodeRotatePanelKeyRequest`, `NodeRotatePanelKeyResponse`,
  `LegacyV030NodeProvisionRequest` (the v0.3.0
  shape, `@deprecated`).
- `frontend/src/types/api.d.ts`:
  auto-regenerated by `npm run codegen` from the
  updated OpenAPI spec.
- `frontend/src/views/NodesView.vue`:
  per-row dropdown gains a "Rotate panel key" item;
  new dialog + success card; `isRotatable()` helper
  gates the dropdown entry.
- `frontend/src/i18n/locales/en.json` / `ru.json`:
  12 new strings per locale.

## Release

This PR closes v0.8.3 item 2. The next release
(v0.8.4) will be a one-PR release — the same tag-on-
docs-commit pattern as v0.8.1 / v0.8.2 / v0.8.3.
Live deploy remains deferred per the user's
"пока что отложим деплой" decision; the live VPS
sits on v0.8.0 and is 4 releases behind in GHCR.
