# feat(agent): real aegis-agent binary + bootstrap service wire + Ansible role (v0.3.0-c)

Closes v0.3.0-c — the last sub-slice of the BYO Node
flow. v0.3.0-a (PR #67) shipped the `internal/bootstrap`
package but **left it unwired** in `cmd/aegis/main.go`
— line 343 had a literal `nil /* bootstrapSvc — wired
in v0.3.0 PR */` placeholder, so
`POST /api/v1/nodes/{id}/provision` was 404 in
production. This PR fixes that bug and ships the real
agent binary that the install was always meant to
upload.

## What's in the box

### 1. Real `aegis-agent` binary (`backend/cmd/aegis-agent/`)

A small Go HTTP server (chi-less, stdlib `net/http`).
The v0.3.0 surface is the minimum the panel needs to
keep the systemd unit `active` after install:

- `GET /healthz` — 200 OK with `{ok, version, started_at}`
- `POST /v1/apply` — 202 Accepted, validates the
  body's inner `config` parses as JSON. v0.4.0 will
  actually write to disk and reload sing-box.
- `GET /v1/status` — 200 OK with the running state +
  last_apply ISO timestamp (in-memory only).
- `GET /v1/stats` — 200 OK with the empty shape; v0.4.0
  wires to the sing-box clash-api listener.

Every endpoint requires the bearer secret from
`AEGIS_AGENT_BEARER` (the panel's
`internal/bootstrap/secrets.go` generates this per
install and writes it to `/etc/aegis/agent.env`).
When the secret is empty, only `/healthz` is
reachable — the "insecure mode" path the
docker-compose smoke uses for the orchestrator's
readyness check.

13 unit tests, all green. A real binary smoke
(process start + `GET /healthz` with bearer
header) returns the expected JSON in <50 ms.

### 2. `cmd/aegis/main.go` — wire the bootstrap service

The line-343 `nil` placeholder is gone. The new
section:

- Builds a `nodes.BootstrapNodeProvider{Svc: nodesSvc}`
  adapter.
- Calls `bootstrap.NewService(...)` with the
  `AgentBinaryPath` / `KnownHosts` / `SSHUser` /
  `SSHPort` from `cfg`.
- Calls `bootstrap.EnsureKnownHosts(...)` at boot
  (idempotent: no-op when the file already exists;
  creates the parent dir + empty file mode 0600
  when it does not).
- Forwards the resulting `*bootstrap.Service` to
  the router, replacing the `nil`.

### 3. `internal/config/config.go` — new env vars

- `AEGIS_AGENT_BINARY` (required) — local path of
  the agent binary. Default `./bin/aegis-agent` for
  dev; production deploys override.
- `AEGIS_AGENT_SSH_USER` (default `root`).
- `AEGIS_AGENT_SSH_PORT` (default `22`).
- `AEGIS_AGENT_KNOWN_HOSTS` (default `./var/known_hosts`).

### 4. `deploy/ansible/roles/install_agent/` — fully wired

The previous role was a half-stub: it had
`templates/agent.env.j2` but no
`defaults/main.yml` (so all variables were undefined)
and no `files/aegis-agent.service` (so the systemd
copy step would fail with "file not found"). The
new layout:

- `defaults/main.yml` — `aegis_panel_url`,
  `aegis_node_name`, `aegis_agent_listen_addr`,
  `aegis_agent_log_level`, `aegis_agent_user`.
- `files/aegis-agent.service` — static systemd
  unit (Type=simple, EnvironmentFile, ExecStart,
  Restart=always, ProtectSystem=strict).
- `templates/agent.env.j2` — corrected to read
  `AEGIS_AGENT_BEARER` (the variable the agent
  actually consumes) instead of the wrong
  `AEGIS_AGENT_BOOTSTRAP_TOKEN`.
- `tasks/main.yml` — replaced the broken
  `get_url` against the non-existent
  `/api/v1/agents/{system}/{arch}/bin` endpoint
  with a `copy` from a local path. The operator
  pre-builds the binary on the control host
  (`go build -o ./bin/aegis-agent ./cmd/aegis-agent/`)
  and passes the path via
  `-e aegis_agent_local_path=./bin/aegis-agent`.
  The bootstrap install path (SFTP) is unchanged
  — the two paths converge on the same on-disk
  layout.

### 5. `Makefile` + `Dockerfile`

- `make build` now also builds the agent
  (`build-agent` is a separate target for the
  subset of callers that only need the agent —
  e.g. the docker-build-agent target).
- `backend/cmd/aegis-agent/Dockerfile` — multi-
  stage build (golang:1.26-alpine → distroless
  static-debian12:nonroot). The static build
  means the binary runs on every Linux distro
  without glibc-vs-musl worries.

## Files

- `backend/cmd/aegis-agent/main.go` (new, 358 lines)
- `backend/cmd/aegis-agent/main_test.go` (new, 13 tests)
- `backend/cmd/aegis-agent/Dockerfile` (new)
- `backend/cmd/aegis/main.go` (+40, -2 — the wire)
- `backend/internal/config/config.go` (+44 — the
  new env vars)
- `backend/Makefile` (+20, -8 — build-agent
  target + Dockerfile wiring)
- `deploy/ansible/roles/install_agent/defaults/main.yml` (new)
- `deploy/ansible/roles/install_agent/files/aegis-agent.service` (new)
- `deploy/ansible/roles/install_agent/templates/agent.env.j2`
  (rewritten — `AEGIS_AGENT_BEARER` instead of the
  wrong `AEGIS_AGENT_BOOTSTRAP_TOKEN`)
- `deploy/ansible/roles/install_agent/tasks/main.yml`
  (rewritten — local-file copy instead of the
  non-existent panel endpoint)

Total: 10 files, 5 new.

## Verified

- `go build ./...` — clean.
- `go test -count=1 ./...` — all packages pass
  (cmd/aegis-agent: 13 tests, bootstrap: 24 tests,
  nodes: 2 tests; the rest unchanged).
- `go vet ./...` — clean.
- Real binary smoke: built `bin/aegis-agent.exe`,
  started with `AEGIS_AGENT_BEARER=test-bearer-...`,
  hit `GET /healthz` with the bearer header,
  got `{"ok":true,"version":"dev","started_at":"..."}`
  in <50 ms. Killed the process; cleaned the
  .exe out of `bin/`.

## Risk

Low. The wire of `bootstrap.NewService` is the only
runtime behavior change — every other change is
additive (new file, new target, new env var with a
default). The smoke proves the agent binary works
end-to-end on a real machine.

The main risk is the agent binary's `POST /v1/apply`
accepts a body and ACKs but does not write to disk
or reload sing-box. That is the v0.4.0 work — the
DoD for v0.3.0-c is "the bootstrap install can put
a binary on a node and see it `active`", which is
exactly what the smoke proves. The real Apply
is the v0.4.0-mvp-batched BatchedApplier concern.

## DoD

- Click "Provision" in the UI for a `new` node →
  the panel connects, uploads the agent, writes
  the env + unit, runs `systemctl enable --now
  aegis-agent`, and the `systemctl is-active`
  poll returns `active` (because the binary is a
  real HTTP server that binds and accepts
  connections, unlike the previous `sleep
  infinity` placeholder).
- The agent is reachable on
  `http://127.0.0.1:8080/healthz` (bearer auth
  required) and on `/v1/{apply,status,stats}`
  (bearer required).
- The next Apply call (v0.4.0) writes the
  sing-box config and runs `systemctl reload
  sing-box`.

## Followups

- v0.4.0: real Apply handler (write to disk,
  reload systemd, wire stats to clash-api).
- v1.1.0: replace bearer with mTLS once the
  panel-side change ships.

## Closes the v0.3.0-c DoD

After this lands:
- `v0.3.0-mvp-byo-node` can be tagged (the
  followup commit).
- v0.4.0-mvp-batched (BatchedApplier + HY2
  load-test) is the only remaining vertical
  slice before v1.0.0-mvp-soft-launch.
