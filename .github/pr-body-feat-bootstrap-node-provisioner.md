feat(bootstrap): BYO Node provisioner + state machine (v0.3.0-a)

First sub-PR of v0.3.0-mvp-byo-node. v0.3.0-a
ships the backend half of the bootstrap pipeline:
state machine, bearer-secret minting, the SSH
client wrapper, the install workflow, and the
HTTP handler. v0.3.0-b will add the operator
UI ("Add node" dialog + provisioning status
badge); v0.3.0-c will swap the placeholder
agent for the real one (the v0.4.0 Batched
Apply milestone).

## Backend

* `internal/bootstrap/state.go` — the v0.3.0
  state machine. The set of valid transitions
  is hard-coded in the constructor
  (`new -> online`, `new -> offline`,
  `offline -> new`, plus reserved v0.5+ edges).
  Pure function (no I/O) so the unit tests are
  deterministic and the graph is the single
  source of truth. The state type is a
  `string` defined here (not imported from
  `internal/nodes`) to break the
  nodes<->bootstrap import cycle.
* `internal/bootstrap/secrets.go` — bearer
  secret generation. 32 bytes of
  `crypto/rand` (256-bit entropy per
  NIST SP 800-63B), hex-encoded for
  transport. The hash is the placeholder for
  the v0.5.0 challenge-response
  verification.
* `internal/bootstrap/ssh.go` — the SSH
  client wrapper. `Client` is an interface
  (4 methods) so the install tests can
  substitute a mock without a real `sshd`.
  The real implementation is `sshClient`,
  using `golang.org/x/crypto/ssh` for the
  handshake + `github.com/pkg/sftp` for the
  file transfer subsystem. Host-key
  verification is the security-critical
  closure; the `TofuPolicy` enum
  (`TofuReject` / `TofuAcceptAndAppend`)
  controls first-contact behaviour, and
  the operator-supplied
  `ExpectedFingerprint` is the safety net
  that catches a MITM in the TOFU path.
  `appendKnownHosts` writes entries to
  the operator's `known_hosts` file
  atomically (write-to-temp + rename).
* `internal/bootstrap/installer.go` — the
  per-node install workflow. Five steps
  after Connect: upload the agent binary
  to `/usr/local/bin/aegis-agent`,
  `chmod 0755` it, write
  `/etc/aegis/agent.env` with the bearer
  secret (mode 0600), write the systemd
  unit, `daemon-reload && enable --now
  aegis-agent`. The post-install verify
  polls `systemctl is-active` for up to
  5s; the placeholder agent is
  `sleep infinity` so the unit goes
  `active` immediately.
* `internal/bootstrap/provisioner.go` —
  the `Service` that ties the state
  machine, the installer, the audit log,
  and the nodes store together. The
  `Provision` function is synchronous
  (the install is sub-second on a healthy
  network); every state transition writes
  a row to the audit log (the v0.2.0
  audits package is the writer). The
  pre-condition guard `isProvisionable(s)`
  accepts only `new` and `offline` as
  start states; the HTTP layer maps the
  `errInvalidStartState` sentinel to a
  409.
* `internal/bootstrap/handler.go` — the
  HTTP handler. Mounted at
  `POST /api/v1/nodes/{id}/provision`
  by the nodes router (a thin shim
  inside the existing v0.2.0 nodes
  surface). Snake_case wire format
  matches the v0.1.0 / v0.2.0 panel
  handlers. The handler returns 200 with
  the new state on success, 409 on a
  pre-condition violation, and 502 on
  an upstream SSH failure.

## Wiring

* `internal/nodes/handler.go` — `Router`
  now takes a third parameter: the
  `bootstrapProvider` interface. The
  provision endpoint is mounted only
  when the bootstrap service is wired
  (v0.3.0-a: pass `nil` to mount only
  the v0.2.0 CRUD surface; main.go will
  pass the real service in a follow-up).
  A `BootstrapNodeProvider` adapter in
  the nodes package wraps
  `nodes.Service` and exposes the
  `bootstrap.NodeProvider` interface
  the provisioner depends on — the
  adapter is in `nodes` (not `bootstrap`)
  to keep the import cycle out.
* `internal/router/router.go` — `Build`
  takes a new `bootstrapSvc` parameter.
  The router test passes `nil`; main.go
  passes the real service in the v0.3.0-a
  wire-up PR.
* `cmd/aegis/main.go` — passes `nil` for
  the bootstrap service in v0.3.0-a. The
  follow-up PR wires the real
  `bootstrap.NewService(...)` once we
  have a stable placeholder agent
  (the v0.3.0-c Ansible role).

## Quality

* `go test ./...` — 24 bootstrap tests,
  all pre-existing tests pass. The
  bootstrap tests cover: every state
  transition (no-op and matrix), secret
  uniqueness and SHA-256 shape,
  `appendKnownHosts` create, append,
  preserve, and reject-empty, the install
  happy path and every failure stage, the
  provisioner happy path, offline
  transition, pre-condition guard, and
  retry-from-offline, and
  `EnsureKnownHosts` create, idempotent,
  and reject-empty.
* `golangci-lint v2` — clean (the new
  code is `unparam`-clean: no test
  helper takes a single-value parameter,
  no exported function takes a status
  that is always the same).
* `gofmt` / `goimports` — clean for the
  new files (the pre-existing CRLF on
  the v0.2.0 files is unchanged; the
  Go files written by the `write` tool
  this round are LF + no-BOM).
* `go vet -tags=integration ./...` —
  clean (the integration test files are
  v0.4.0+ scope; v0.3.0-a ships unit
  tests + a mock-based install
  workflow).

## Out of scope (v0.3.0-b / v0.3.0-c / v0.4.0)

* **Frontend** ("Add node" dialog +
  provisioning status badge in the
  nodes list). v0.3.0-b is a focused
  Vue 3 + zod + vee-validate PR that
  reuses the existing dialog
  primitives.
* **Real agent binary.** v0.3.0 uses
  a placeholder `sleep infinity` so
  the bootstrap pipeline can be
  verified end-to-end. v0.4.0 ships
  the real `aegis-agent` Go binary
  (Batched Apply).
* **Ansible role `install_agent`.**
  v0.3.0-c extends the v0.2.0-dev
  Ansible scaffolding to a real
  install role (the bootstrap state
  machine is the source of truth for
  the target layout).
* **Challenge-response handshake.**
  v0.3.0 ships a one-shot secret
  install; v0.5.0 adds a mutual-TLS
  handshake on the agent callback
  using the SHA-256 hash from
  `secrets.go`.
* **Async provisioning.** v0.3.0 is
  synchronous (the install blocks
  for the duration of the workflow).
  v0.5.0 adds a "kick off + poll"
  mode for large fleets.
* **Audit log table for the
  decommissioning / heartbeat-miss
  transitions.** v0.5.0 wires the
  `draining`, `disabled`, and
  `heartbeat-miss` state transitions
  to the operator's action menu and
  the agent's heartbeat loop.
