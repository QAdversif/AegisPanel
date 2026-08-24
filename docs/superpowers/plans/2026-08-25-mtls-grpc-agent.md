# mTLS + gRPC for the aegis-agent

**Date**: 2026-08-25
**Owner**: Mavis (orchestrator) + operator
**Target release**: v0.8.32 (cut HTTP transport). Intermediate: v0.8.29 → v0.8.30 → v0.8.31
**Architecture reference**: ARCHITECTURE.md §0 ("Agent (Go, musl) — связь панель↔нода через HTTP+bearer (Phase 1), миграция на mTLS+gRPC в v1.1.0")
**Versioning constraint** (operator, 2026-08-25): stay in 0.8.x branch. No jumps to 0.9.x or 1.x+. v0.8.29 / v0.8.30 / v0.8.31 / v0.8.32 sequence.

---

## Problem

The panel↔node control plane is plain HTTP + bearer secret. The current
contract (`backend/cmd/aegis-agent/` → `backend/internal/cores/singbox/apply.go`)
uses `http.Client.Do()` POST to `http://<addr>/v1/apply` with
`Authorization: Bearer <token>`. Risks:

- **Bearer secret theft** — single long-lived shared secret. A leaked
  `/etc/aegis/agent.env` (operator mistake, log leak, image backup) gives
  full apply control.
- **No mutual auth** — agent can be impersonated to a fresh panel
  instance (only the panel's outbound URL is the gate).
- **Custom JSON escapers** (closed in v0.8.28.6 #300) only papered over
  the wire format fragility; gRPC + protobuf removes the entire
  class.
- **No streaming** — Apply/Status/Stats are 3 separate HTTP round-trips.

ARCHITECTURE.md §0 committed to a v1.1.0 migration. The operator's
2026-08-25 constraint is to keep the version line in 0.8.x. This plan
resequences the work into 4 minor releases.

## Goal

Cut the HTTP+bearer surface from the agent in v0.8.32. The cut is
**painless** because every step is mechanically separated from the
existing HTTP path. New nodes start on gRPC+mTLS at v0.8.30; old
nodes get a one-shot migration CLI in v0.8.31; v0.8.32 removes
~5 files and 1 env var.

## Non-goals

- Replacing sing-box with Xray (v2.0+, ADR-0003)
- gRPC streaming RPCs (Apply is unary; the future /v1/stats stream is
  out of scope)
- A public external CA — we self-sign a root in the panel's age envelope,
  same pattern as `nodes.ssh_private_key_ciphertext`
- Multi-tenant (the panel is single-tenant by design; ARCHITECTURE.md
  §0)

---

## Architecture

### Wire (proto)

`proto/aegis/v1/agent.proto` — versioned as `aegis.v1` so future
breaking changes land in `aegis.v2` (post-v1.0.0). Pre-1.0 we can
break `v1` since the panel+agent version-lock the wire at install
time.

Service:

```proto
service AegisAgent {
  rpc Apply(ApplyRequest) returns (ApplyResponse);
  rpc Status(StatusRequest) returns (StatusResponse);
  rpc Stats(StatsRequest) returns (StatsResponse);
  rpc Health(HealthRequest) returns (HealthResponse);
}
```

The Apply envelope is the rendered sing-box config as a single `bytes`
field — the agent does not parse it (it writes it to disk and reloads
sing-box). Mirrors the v0.4.0-b `applyEnvelope{Config: cfg}` exactly
so the side-effect path is unchanged.

### Cert model

- **Root CA**: self-signed, generated on first panel boot
  (`internal/agentca/EnsureRoot`). Private key in
  `nodes.cert_ca_key_ciphertext` (age envelope, same pattern as
  `nodes.ssh_private_key_ciphertext`).
- **Per-node server cert**: subject `CN=<node-uuid>`, SAN `DNS:<node-uuid>`,
  `IP:<node-ip>`. Generated on `nodes.Service.Provision` (push via
  installer SFTP, same channel as the agent binary).
- **Per-panel client cert**: subject `CN=aegis-panel`, SAN `URI:spiffe://aegis/panel`.
  Generated once at CA-bootstrap, reused across all nodes.
- **Rotation**: per-node via `POST /api/v1/nodes/{id}/rotate-mtls`
  (mirror of v0.8.3 `rotate-panel-key`). CA rotation is a separate,
  out-of-scope task.

### Transport

- Agent: `grpc.NewServer(creds.NewServerTLSFromCert(&tlsCert))` on
  `AEGIS_AGENT_LISTEN_GRPC` (default `:7001`).
- Panel: `grpc.Dial(addr, grpc.WithTransportCredentials(
  credentials.NewTLS(&tls.Config{RootCAs: caPool, Certificates: []tls.Certificate{clientCert}})))`.
- HTTP bearer stays on `:7000` until v0.8.32 cut.

### Code layout

```
proto/aegis/v1/agent.proto            # wire contract
backend/internal/agentv1pb/           # generated stubs (committed)
  agent.pb.go
  agent_grpc.pb.go
backend/internal/agentgrpc/           # panel-side transport package (v0.8.29)
  client.go            # Client interface { Apply/Status/Stats/Health }
  http_transport.go    # wraps the existing singbox/apply.go HTTP path
  grpc_transport.go    # generated stubs + mTLS dial
  transport.go         # New(env string) → Client, picks via AEGIS_AGENT_TRANSPORT
backend/internal/agentca/             # CA bootstrap (v0.8.30)
  ca.go
  ca_test.go
backend/internal/nodes/
  mtls.go              # Provision/Store/Rotate for certs
backend/cmd/aegis-agent/
  grpc.go              # gRPC server (v0.8.29)
  grpc_apply.go        # handler, calls same writeAtomicConfig + reload
backend/cmd/aegis-admin/ (NEW in v0.8.31)
  main.go              # operator CLI; v0.8.3 rotate-panel-key pattern
  rotate_transport.go  # `aegis admin agent rotate-transport <node-uuid>`
```

---

## Phases

### v0.8.29 — gRPC without mTLS (2-3 PR)

**Scope**: introduce the wire, agent gRPC server, panel gRPC client,
dual-stack default. Default transport is still `http` (backward-compat).

PRs:
1. **proto + codegen** (this PR). `proto/aegis/v1/agent.proto` + generated
   `internal/agentv1pb/` + `make proto` target + CI gate for
   codegen idempotency.
2. **agent gRPC server**. `cmd/aegis-agent/grpc.go` listens on `:7001`,
   calls the existing `writeAtomicConfig` + reload path. Same bearer
   in gRPC metadata as the HTTP path. Tests: in-memory listener,
   e2e Apply / Status / Health.
3. **panel `agentgrpc` package + transport switch**. `singbox/apply.go`
   consumes `agentgrpc.Client` interface. `AEGIS_AGENT_TRANSPORT`
   env (`http|grpc|dual`, default `http`).

### v0.8.30 — mTLS (2 PR)

PRs:
1. **`internal/agentca/` + cert issuance on provision**. Self-signed
   root, `nodes.cert_ca_key_ciphertext` storage, per-node server
   cert on `nodes.Service.Provision`. Agent push via installer SFTP.
2. **mTLS handshake + smoke**. Agent `--mtls-cert/--mtls-key/--mtls-ca`
   flags. Panel client `credentials.NewTLS`. `release.yml` smoke
   test (dev-only at this point; full VM smoke stays in v0.9.0
   per the existing plan).

### v0.8.31 — Migration tooling (1-2 PR)

PRs:
1. **`nodes.agent_transport` column + telemetry + audit event**.
   Migration `0023_add_nodes_agent_transport.sql`. `GET /api/v1/nodes`
   returns `agent_transport: http|grpc`. `agent.transport.rotated`
   audit row (mirror of `node.panel-key.rotated`).
2. **Migration CLI + operator guide**. `aegis admin agent
   rotate-transport <node-uuid> [--all] [--filter transport=http]`.
   Deprecation warning in `GET /api/v1/nodes` if any node is
   `transport=http`. CI grep gate:
   `! grep -rn "agenthttp\|http_transport" backend/ --exclude-dir=archive`.
   `docs/operator-guide.md` + `KNOWN_LIMITATIONS.md` update.

### v0.8.32 — Cut (1 PR, after 1-2 releases of observation)

Trigger conditions (all must hold):
- `GET /api/v1/nodes` shows 0% `transport=http` in prod for at
  least 1 release
- Telemetry confirms 0% HTTP at peak hour for 7 days
- Operator has signed off

What goes:
- `backend/internal/agentgrpc/http_transport.go`
- `backend/internal/agentgrpc/http_transport_test.go`
- `backend/cmd/aegis-agent/http.go`
- `backend/cmd/aegis-agent/apply.go` (the HTTP-only path; the
  side-effect primitives are reused by `grpc_apply.go`)
- `backend/internal/agentgrpc/testdata/http_*.go`
- `AEGIS_AGENT_TRANSPORT` env var
- `backend/internal/agentgrpc/transport.go` default → `grpc` (no env
  switch, just construct the gRPC transport)

What stays:
- `backend/internal/agentgrpc/grpc_transport.go` (now the only impl)
- `backend/cmd/aegis-agent/grpc.go` + `grpc_apply.go` (now the only
  server)
- `internal/agentv1pb/`
- `internal/agentca/`

Rollback: revert the v0.8.32 PR + retag v0.8.31. ~5 files in git
restore; no data migration.

---

## Open risks

1. **PR 1 scope creep** — the proto file pulls in `grpc` to go.mod
   (currently only `protobuf` is there as indirect). A future PR
   can drop the dep if v0.8.29 is rejected.
2. **Cert bootstrap blast radius** — `nodes.Service.Provision` is
   hot code (audit events, BatchedApplier kick). v0.8.30 PR 1 (CA +
   cert issuance) needs to land first and soak 1 release before
   the mTLS handshake PR.
3. **Version skew** — what if a v0.8.28.x panel meets a v0.8.30+ agent?
   The v0.8.30 agent's mTLS is opt-in (only when `--mtls-ca` is set
   in `agent.env`); the panel picks transport per the `nodes.agent_transport`
   column. Pre-v0.8.31 panel won't have the column → fall back to HTTP
   (default `http`). Documented in `KNOWN_LIMITATIONS.md` for the
   v0.8.30 release.
4. **CI resource cost** — pre-release VM smoke is in v0.9.0, not
   v0.8.32. v0.8.32 cut is enforced by `release.yml` linting that
   the package `internal/agentgrpc` no longer references
   `http_transport.go` (file-level check).
5. **Operator comms** — the deprecation warning in v0.8.31 must be
   visible in the operator's daily `GET /api/v1/nodes` check. Operator
   guide + KNOWN_LIMITATIONS + a one-time Slack-style note (we don't
   have Slack, so docs only).

---

## Test plan (per phase)

### v0.8.29
- Proto: `make proto` is idempotent; CI verifies no diff on rerun.
- Agent gRPC: in-memory listener, 4 RPC methods, e2e Apply writes
  the expected bytes to a temp file + invokes a fake reload.
- Panel transport switch: matrix `(transport × node-state)` —
  `http`, `grpc`, `dual` × `online`, `offline`, `401-stale-bearer`.

### v0.8.30
- CA: `agentca.EnsureRoot` is idempotent (re-runs return same cert).
- Cert issuance: 100-run fuzz on `(nodeID, ip)` → all certs are
  well-formed, expiry in 90 days, SANs match.
- mTLS handshake: positive (matching CA), negative (wrong CA →
  `Unavailable` status, no further retries), negative (expired cert
  → `Unavailable`).

### v0.8.31
- Migration CLI: `--all` rotates all HTTP nodes; `--filter
  transport=http` rotates a subset; dry-run flag; idempotent.
- Telemetry: after a rotate, the node's `agent_transport` flips
  to `grpc` and the audit event lands in `audits`.
- CI grep gate: a new `http_transport.go` file in `internal/agentgrpc/`
  fails the gate.

### v0.8.32
- Dead code removal: no caller of the deleted files (compile passes
  with the deletes).
- Release smoke: `release.yml` runs the agent gRPC server in a
  disposable container, dials it from the panel image, applies a
  rendered config. No HTTP listener is started.

---

## Decision log

- 2026-08-25 — version stays in 0.8.x (operator constraint).
- 2026-08-25 — package name `aegis.v1`, not `agent.v1` (mirrors
  the existing `aegispanel` module path).
- 2026-08-25 — migration CLI scope: per-node only, no auto-migration
  on next contact (operators with 50+ nodes get a script they can
  run; for 1-2 nodes, manual rotation is fine).
- 2026-08-25 — pre-release smoke stays in v0.9.0 (separate plan,
  per the existing ROADMAP). v0.8.32 cut is enforced by CI lint,
  not by an end-to-end boot test.
