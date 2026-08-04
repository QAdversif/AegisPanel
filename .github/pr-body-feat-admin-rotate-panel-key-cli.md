# feat(cli): `aegis admin node rotate-panel-key` for v0.3.0..v0.7.x nodes

Closes v0.8.3 item 1 from the roadmap (the
CLI half of the deferred re-provision path
from PR #179). The admin UI button is a
v0.8.x follow-up.

## What this PR does

### Backend (refactor + new public method)

- **`internal/bootstrap/rotate_panel_key.go`**
  (new) — public `Service.RotatePanelKey`
  method + private
  `Service.generateAndPushKey` helper. The
  helper is the shared body of the
  password-install post-install hook AND
  the operator-side CLI rotation; both call
  sites now share the gen + encrypt + push +
  persist flow.
- **`internal/bootstrap/provisioner.go`** —
  `buildPersistentSSHKeyHook` is now a
  one-liner that delegates to
  `generateAndPushKey`. The 168-line body
  that was here is gone; the behaviour is
  identical (the existing
  `TestProvisioner_PasswordInstall_GeneratesAndStoresSSHKey`
  test still passes). The provisioner
  state-machine audit-prefix
  ("post-install-hook:") is preserved.
- **`internal/bootstrap/rotate_panel_key_test.go`**
  (new) — 2 unit tests pinning the
  fail-closed paths:
  - `TestRotatePanelKey_NilEnvelopeFailsClosed`
    — the v0.8.3 CLI is a "never persist
    plaintext PEM" tool; a nil envelope is
    the canonical "this deploy is broken"
    path.
  - `TestRotatePanelKey_NilClientFailsClosed`
    — the function must not panic on a nil
    SSH client; it returns an error without
    touching the DB.

### CLI (new subcommand)

- **`cmd/aegis/admin_node.go`** (new) —
  `aegis admin node rotate-panel-key
  <node-uuid> --key <path-to-pem>`. The
  flow is:
  1. Parse args (node UUID, --key, optional
     --port / --user overrides)
  2. Read the operator's PEM (file path
     or stdin via `--key -`)
  3. Open the pg pool (uses
     `AEGIS_POSTGRES_DSN` like the rest of
     the panel)
  4. Build the nodes Service + Store and
     the age envelope (uses the same
     `AEGIS_WEBHOOKS_SECRET_AGE_*` env vars
     the panel's webhooks Store uses;
     fall-through to `NoopSecretCipher` in
     dev mode with a warning)
  5. Look up the node row
  6. Open SSH using the operator's PEM
     (bootstrap.NewClient with
     ClientConfig.PrivateKey; the row's
     Address + AEGIS_AGENT_SSH_USER / PORT
     / KNOWN_HOSTS env vars)
  7. Call `Service.RotatePanelKey` to
     generate the new ed25519 keypair,
     seal the private half, push the public
     half to authorized_keys, and persist
     the ciphertext
  8. Record the audit entry
     (`node.rotate-panel-key`)
  9. Log success
- **`cmd/aegis/main.go`** — `runAdmin` now
  dispatches `aegis admin node <sub>` to
  `runAdminNode`. `adminUsage` lists the
  new subcommand.

## What this PR does NOT ship

- **No admin UI button.** The v0.8.x
  follow-up: a "Rotate panel key" button
  on the NodesView detail page that calls
  the same `Service.RotatePanelKey`. The
  CLI is the operator-side tool today; the
  UI lands when there's a UX requirement.
- **No "BatchedApplier decrypt-and-use".**
  The original ROADMAP v0.8.3 row mentioned
  this; on review the BatchedApplier uses
  HTTP bearer (`Authorization: Bearer <bearer>`)
  to POST /v1/apply to the agent, NOT SSH.
  The stored panel SSH key is the provisioner's
  auth material (used when re-installing),
  not the BatchedApplier's. The actual
  deferred work from PR #179 was the
  CLI for re-provisioning legacy nodes,
  which this PR ships.
- **No memory-store CLI path.** The node
  store is required to be pg (the
  pre-existing `nodes.Store` interface has
  no memory path; the memory store exists
  for unit tests, not the CLI). The CLI
  fails fast on missing `AEGIS_POSTGRES_DSN`.
- **No audit wiring change.** The CLI
  records the audit entry via the same
  `audits.Service.Record` call site the
  provisioner uses; the audit-log call-site
  wiring from PR #166 is the writer.

## Verification

- `go build ./...` — clean
- `go vet ./...` — clean
- `gofmt -l internal/...` — clean
- `go test -short -count=1 ./...` —
  25/25 packages PASS
- `golangci-lint run ./...` — 0 issues
- `aegis admin node` (without args) prints
  the usage to stderr and exits 2
- `aegis admin node rotate-panel-key` (no
  args) prints the usage to stderr and
  exits 2
- `aegis admin` usage line lists the new
  `node` subcommand

## Pre-PR walk: from a real install to a real rotation

The use case is a v0.3.0..v0.7.x node:

1. Operator SSHed into the panel, ran
   `aegis admin node rotate-panel-key
   <node-uuid> --key ~/.ssh/id_ed25519`.
2. CLI opened a pg pool, looked up the row,
   read the operator's PEM, and SSHed
   into the node using that PEM.
3. The CLI generated a fresh ed25519
   keypair, sealed the private half with
   the operator's age envelope, persisted
   the ciphertext via
   `NodeProvider.SetSSHPrivateKeyCiphertext`.
4. The CLI pushed the new public half to
   `/tmp/.aegis-pubkey` on the node via
   SFTP, then ran a constant shell command
   that appended the line to
   `~/.ssh/authorized_keys` (idempotent via
   `grep -qxF`) and removed the temp file.
5. The CLI logged
   `node.rotate-panel-key: rotated (next
   re-provision is auto-deploy)` and
   returned 0.
6. The next re-provision (via the UI, with
   no auth input from the operator)
   decrypts the stored key and uses it —
   the legacy node is now "auto-deploy"
   the same way a fresh v0.8.1+ node is.

## Pre-existing v0.8.1 behavioural contract

This PR does not change the v0.8.1 install
path. The `buildPersistentSSHKeyHook` was
refactored, but its observable behaviour is
identical (the existing
`TestProvisioner_PasswordInstall_GeneratesAndStoresSSHKey`
test still passes). The hook runs at the
same point in the install flow, writes the
same ciphertext, pushes the same public
key, and surfaces the same audit-prefix
("post-install-hook:") for the failure
stage.
