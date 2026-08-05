# feat(ui): NodesView "Show stored key" debug surface (v0.8.5, read-side mirror of the v0.8.1 persistent key)

## What this PR ships

The v0.8.1 post-install hook and the v0.8.3/v0.8.4
rotation flows both encrypt the generated ed25519
private key with the operator's age envelope and
persist the ciphertext in
`nodes.ssh_private_key_ciphertext` (migration 0020).
The next re-provision decrypts and reuses it. This
is a round-tripping feature: the operator never sees
the key in cleartext through normal operation. This
PR closes the **read side**: the operator asks
"what does the panel currently have stored for this
node?" without rotating it.

- **New endpoint** `GET /api/v1/nodes/{id}/stored-key`.
  Same handler signature shape as the v0.3.0
  `/provision` and the v0.8.4 `/rotate-panel-key`
  endpoints (mounted under the same `{id}` subrouter
  with `auth.RequireScope(ScopeNodes)` already enforced
  by the parent nodes router). Takes no body, returns
  the public-key line + SHA-256 fingerprint.
- **"Show stored key" dropdown entry in the NodesView**
  per row, with an `Eye` icon. Visible for ANY state
  (the entry is a read, not a write, so the state
  machine does not gate it).
- **"Show stored key" dialog** that fires the GET on
  open, shows a spinner, then either surfaces the
  public surface (with copy-friendly public key
  line + fingerprint + last-updated timestamp) or a
  "no stored key yet" hint for the un-installed
  case.

## Why a separate endpoint (not the CLI)

The v0.8.3 CLI's raison d'être was "no UI, no
endpoint, give the operator a way to back-fill legacy
nodes". With the v0.8.4 endpoint + button the CLI
becomes the operator-side fallback for `rotate`, the
UI button is the primary path for `rotate`, and
this new v0.8.5 endpoint is the read-side debug
surface for the same persistent-key feature. The
`Service.GetStoredKey` method is the canonical
decrypt-and-derive surface; the CLI and the HTTP
handler both go through it (the v0.8.3 CLI is
out-of-scope for v0.8.5 — it does not have a
"show stored key" subcommand, and the operator
can use the new HTTP endpoint for that).

## Implementation notes

- **`nodes.Service.WithEnvelope` setter** — the v0.8.1
  envelope (the same age cipher the webhooks Store
  uses) is now wired into the nodes Service so the
  stored-key read can decrypt the column. The
  setter is nil-safe (a nil cipher disables the
  stored-key read path, same fail-closed shape as
  the v0.8.4 rotate-panel-key handler).
  `internal/app/app.go` wires it from the same
  `cipher` variable the webhooks Store uses.
- **`StoredKey` type + wire shape** — the field set
  is intentionally minimal: the public key line
  (which already embeds the OpenSSH key comment as
  the third whitespace-separated token), the
  fingerprint, the algorithm, and the row's
  `updated_at`. The OpenSSH key comment is NOT a
  separate field; the
  `golang.org/x/crypto/ssh` v1.5 parser's public
  API does not surface the comment on the returned
  `crypto.PrivateKey`, so pulling it would require
  either a custom OpenSSH-wire parser or shelling
  out to `ssh-keygen -l`. Neither is worth the
  complexity: the comment is parseable from
  `public_key_line` via `line.split(' ', 3)`.
- **`Store.byID` is a private field** — the unit
  tests in `stored_key_test.go` reach into it via
  `store.byID[cp.ID] = &cp` (same package). This
  is the same pattern the existing
  `provisioner_test.go` uses for the mock
  `NodeProvider` — the test fixture and the
  MemoryStore live in the same package so the
  private field is accessible.
- **Audit log** — every read records a
  `node.stored-key.read` row with the operator's id
  from the JWT claims + the node id. The
  fingerprint is NOT in the audit row (per-server,
  would be a 100x write-amplification for a read
  that may happen frequently); the fingerprint is
  in the response body so the operator can
  correlate the audit row with the read they
  performed (the audit log UI shows timestamp +
  node id, the operator's screen shows the same
  timestamp + fingerprint).
- **OpenAPI spec + codegen** — new
  `NodeStoredKey` schema in `docs/openapi.yaml`;
  the generated `frontend/src/types/api.d.ts`
  carries the new type automatically.
- **i18n** — 9 new strings per locale
  (`nodes.inspect` / `nodes.inspectTitle` /
  `nodes.inspectDescription` /
  `nodes.inspectLoading` /
  `nodes.inspectNoKey` /
  `nodes.inspectNoKeyHint` /
  `nodes.inspectSurfaceTitle` /
  `nodes.inspectSurfaceHelp` /
  `nodes.inspectKeyUpdatedAt` /
  `nodes.inspectFailed`). The Russian translations
  follow the v0.8.x "operator voice, not translator
  voice" style (e.g. «У этой ноды пока нет
  сохранённого ключа панели» rather than «У ноды
  отсутствует хранимый ключ»).

## Security shape

- The endpoint exposes the public key (which is
  already in the node's `~/.ssh/authorized_keys`)
  and the fingerprint (a one-way hash). Neither is
  a secret; the public key adds no new attack
  surface (any operator with shell on the node can
  `cat authorized_keys` and see the same line), and
  the fingerprint is irreversible.
- The private key stays in the panel process only
  for the duration of the decrypt; the response
  carries no private-key material. The audit log
  records every decrypt (the
  `node.stored-key.read` action) so the operator
  can see who looked at the stored key in the
  audit UI.
- A nil envelope returns 500 (server config); the
  same fail-closed shape the v0.8.4
  rotate-panel-key handler uses. The operator must
  set `AEGIS_WEBHOOKS_SECRET_AGE_*` and restart the
  panel.

## Error mapping

| HTTP | When                                                                              |
| ---- | --------------------------------------------------------------------------------- |
| 200  | stored key (or no-key) is read                                                   |
| 400  | malformed UUID                                                                   |
| 404  | node row not found                                                                |
| 500  | panel booted without an envelope                                                 |
| 502  | stored ciphertext is unreadable (the age identity has been rotated out of the operator's reach, or the column was written with a different envelope version) |
| 401  | missing or invalid bearer token (enforced by the parent nodes router)             |

## Tests

- **4 Service tests** in
  `backend/internal/nodes/stored_key_test.go`:
  - `TestGetStoredKey_HappyPath_PopulatesAllFields` —
    round-trip with a real ed25519 key (generated,
    encrypted, persisted, decrypted, public key
    derived, all fields populated, fingerprint
    starts with `SHA256:`)
  - `TestGetStoredKey_RowWithoutCiphertext_ReportsNoKey` —
    `HasStoredKey: false`, no decrypt attempt
  - `TestGetStoredKey_NilEnvelope_FailsClosed` —
    fail-closed, no row mutation
  - `TestGetStoredKey_NodeNotFound_Propagates` —
    the underlying `ErrNotFound` propagates
- **6 HTTP handler tests** in the same file:
  - 200 happy path (correct body shape + audit
    row recorded with the right action /
    resource_id)
  - 200 no-stored-key (audit row still recorded)
  - 400 malformed UUID
  - 404 node not found
  - 500 envelope not configured
  - 502 decrypt failure (simulated by storing
    random non-PEM bytes; the parser fails with
    "not a PEM block" → handler maps to 502)

## Files

- `backend/internal/nodes/stored_key.go` (new,
  ~250 lines): `GetStoredKey` Service method +
  `StoredKey` type + `parseOpenSSHPrivateKey` helper.
- `backend/internal/nodes/stored_key_test.go` (new,
  ~400 lines): 10 unit tests.
- `backend/internal/nodes/service.go`:
  `WithEnvelope(cipher envelope.SecretCipher)`
  setter; `envelope` field on the Service struct.
- `backend/internal/nodes/handler.go`:
  `handleGetStoredKey` HTTP handler;
  `r.Get("/stored-key", ...)` mount in the
  `{id}` subrouter; `contains` string helper
  for the error-mapping switch.
- `backend/internal/app/app.go`:
  `a.Nodes.WithEnvelope(cipher)` after the
  webhooks Service is wired.
- `docs/openapi.yaml` (+160):
  `/nodes/{id}/stored-key` path +
  `NodeStoredKey` schema.
- `docs/api/index.md`:
  v0.8.5 paragraph, "stored-key" row in the
  endpoints table, the "later version" section
  update.
- `docs/ROADMAP.md`:
  v0.8.4 status ✅, new v0.8.5 row ⏳, the
  v0.8.x "show me stored public key debug
  surface" item is removed (it lands in v0.8.5).
- `CHANGELOG.md`:
  new v0.8.5 [Unreleased] section + the
  v0.8.4 entries are now in a proper
  `[0.8.4] - 2026-08-04` section (the v0.8.4
  PR added them to [Unreleased] but never moved
  them; the v0.8.5 PR fixes the structural
  mistake).
- `frontend/src/types/aegis.ts`:
  `NodeStoredKey` interface.
- `frontend/src/api/services/nodes.ts`:
  `getStoredNodeKey(id)` function.
- `frontend/src/views/NodesView.vue`:
  new dropdown item, dialog with 4 states
  (loading / no-stored-key / error / success),
  styles for the state-specific paragraphs.
- `frontend/src/i18n/locales/en.json` /
  `frontend/src/i18n/locales/ru.json`: 9 new
  strings per locale.
- `frontend/src/types/api.d.ts`:
  auto-regenerated by `npm run codegen` from
  the updated OpenAPI spec.

## Release

This PR closes the v0.8.5 item from the v0.8.x
backlog ("'show me stored public key' debug
surface with fingerprint viewer"). The next
release (v0.8.5) will be a one-PR release — the
same tag-on-docs-commit pattern as v0.8.1 /
v0.8.2 / v0.8.3 / v0.8.4. Live deploy remains
deferred per the user's "пока что отложим
деплой" decision; the live VPS sits on v0.8.0
and is 5 releases behind in GHCR.
