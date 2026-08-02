# feat(cores): real BatchedApplier FlushFn + Enqueue from user/inbound services

Closes audit #2 (BatchedApplier no-op stub from the
2026-08-01 colleague review).

## What

The v0.4.0-mvp-batched code shipped the
`cores.BatchedApplier` infrastructure
(coalescing window, cancel/replace logic, queue)
but two pieces were missing:

- the per-node `FlushFn` was a `log.Info + return nil`
  no-op, so a window flush produced nothing;
- `BatchedApplier.Enqueue` was never called outside
  tests, so the queue stayed empty and the no-op
  FlushFn was the only thing reachable on a real
  boot.

This PR wires the v0.5.0 real path end-to-end.

## Files

- new `backend/internal/cores/builder/builder.go`
  (~200 lines)
  - `ListInboundsByNode` interface
  - `CoreRenderer` interface (RenderConfig + Apply)
  - `BuildCoreConfigForNode(ctx, src, nodeID) (CoreConfig, error)`
    translates the panel's inbounds table into a
    `cores.CoreConfig` (Disabled inbounds are
    skipped; nil `Params` maps to an empty map so
    the sing-box renderer's `requireString` errors
    usefully rather than nil-derefing).
  - `NewFlushFn(src, renderer, nodeID, name) cores.FlushFn`
    returns the closure BatchedApplier calls. The
    body calls `BuildCoreConfigForNode`, then
    `RenderConfig`, then `Apply`, with structured
    error logging at every step.

- new `backend/internal/cores/builder/builder_test.go`
  (4 unit tests): `NoInbounds`, `SourceError`,
  `Mapping` (headline: every Phase 1 protocol
  produces the right InboundSpec shape), `NilParams`
  (defensive — `Params: nil` becomes an empty
  per-inbound map, not a nil deref).

- new `backend/internal/cores/builder/flushfn_smoke_test.go`
  (2 smoke tests): real `*singbox.Provider` +
  `httptest` fake aegis-agent. The first seeds a
  vless-reality inbound, Enqueues a Delta, waits
  for the window, and asserts the fake agent
  received a POST /v1/apply whose JSON envelope
  contains the seeded tag. The second pins the
  "empty node still POSTs" behaviour — the
  renderer's outbounds-only document is the
  correct response to "no inbounds", and the
  FlushFn must not silently skip the apply.

- `backend/internal/app/app.go` (+99 lines)
  - `App.BatchedAppliers map[uuid.UUID]*cores.BatchedApplier`
    (always non-nil after Build; services iterate
    it on Enqueue and an empty map is a no-op).
  - `App.AddNodeBatchedApplier(ctx, nodeID, name, flushFn) *cores.BatchedApplier`
    wires a per-node applier + spawns the Run
    goroutine + registers the cancel func for
    App.Close() to stop uniformly. Lives on App
    so the cancel map stays an unexported
    implementation detail.
  - `App.Close()` cancels every BatchedApplier
    goroutine alongside the existing webhook
    worker cancel + pg pool close.

- `backend/internal/config/config.go` (+17 lines)
  - `BatchedApplierEnabled bool` (`AEGIS_BATCHED_APPLIER_ENABLED`,
    default `true`). When false, the per-node
    wiring loop in main.go is skipped; the
    applier map stays empty; services'
    `WithBatchApplier` iterates an empty map and
    is a no-op. Lets operators run the panel
    side-by-side with an external config manager
    (Ansible, Terraform) without the panel
    clobbering it.

- `backend/internal/users/service.go` (+125 lines)
  - `Service.batchedAppliers` field +
    `WithBatchApplier(map)` setter (mirrors the
    `WithWebhooks` shape so the existing 167+
    test fixtures stay untouched).
  - `enqueueUserDelta(d Delta)` fans out to every
    registered applier.
  - `Create` → `DeltaAddUser`.
  - `Update` → `DeltaAddUser`, OR
    `DeltaSetLimit{Bytes: TrafficLimitBytes}` when
    `in.TrafficLimitBytes` is the only changed
    field. The payload is JSON
    `{"bytes": <int64>}`.
  - `Delete` → `DeltaRemoveUser` (appliers'
    cancel/replace logic drops this if a
    `DeltaAddUser` for the same `UserID` is
    enqueued in the same window — a quick
    delete-then-recreate).
  - `RotateSubToken` → `DeltaAddUser` (rotation
    is a sub-update; Phase 1's single-user
    renderer does not actually consume the
    token, but the enqueue is symmetric with
    Create/Update so a Phase 2 multi-user
    renderer picks it up for free).

- `backend/internal/inbounds/service.go` (+85 lines)
  - `Service.batchedAppliers` field +
    `WithBatchApplier` setter.
  - `enqueueForNode(nodeID, kind)` narrows the
    fan-out to the single applier for the
    inbound's node (unlike users which fans out
    to every node; the inbound already carries
    the node reference).
  - `Create` → `DeltaAddUser{UserID: uuid.Nil}`.
  - `Update` → `DeltaAddUser{UserID: uuid.Nil}`.
  - `Delete` → `DeltaRemoveUser{UserID: uuid.Nil}`
    (captures `prev.NodeID` BEFORE the row is
    gone so the enqueue targets the right
    applier).
  - The `UserID: uuid.Nil` on inbound deltas is
    the BatchedApplier's coalescing contract:
    "inbound change" is not user-scoped, and the
    appliers' last-write-wins under
    `uuid.Nil` collapses multiple inbound CRUD
    events in the same window to one flush.

- `backend/cmd/aegis/main.go` (-22 +77 lines)
  - `singboxWiring` now takes `*app.App` (not
    `*nodes.Service`) so it can call
    `a.AddNodeBatchedApplier` and
    `a.Users.WithBatchApplier` /
    `a.Inbounds.WithBatchApplier` from a single
    place. The two `WithBatchApplier` calls run
    BEFORE the per-node applier loop so a Create
    handler that fires during boot enqueues into
    a fully-built map.
  - The flag gate (`!a.Config.BatchedApplierEnabled`)
    returns nil after `Configure()`; no
    appliers, no goroutines, no fan-out.
  - The FlushFn body is the one-liner
    `builder.NewFlushFn(a.Inbounds, p, nodeID, nodeName)`.
    The 40 lines of `Build + Render + Apply +
    log` moved into the helper so the test can
    exercise the same code.

## Behaviour changes

- **Default on** (`AEGIS_BATCHED_APPLIER_ENABLED=true`):
  every user/inbound CRUD event enqueues a
  Delta into the per-node applier. Every
  `AEGIS_BATCHED_APPLIER_WINDOW` (default 20s),
  the FlushFn re-renders the node's CoreConfig
  and POSTs it to the agent. The agent's diff
  decides whether the file on disk actually
  changes.
- **Phase 1 caveat**: the sing-box renderer is
  single-user per inbound (the user list inside
  the rendered config carries the operator's
  credential from `inbound.Params["uuid"]` or
  `["password"]`). Multi-user rendering lands
  with the inbound-templates work; until then
  the FlushFn's per-user deltas are advisory and
  the re-rendered config produces a stable hash
  on every flush. The infrastructure is the
  deliverable; Phase 2 fills in the per-user
  mapping.
- **`SetLimit` (per-user TrafficLimitBytes)**:
  enqueued as a `DeltaSetLimit` so a future
  Phase 2 renderer (or a netfilter sidecar
  driven by the agent) can pick it up. Phase
  1 logs and applies; the actual quota
  enforcement is out of scope.
- **Operator escape hatch**: set
  `AEGIS_BATCHED_APPLIER_ENABLED=false` to keep
  the v0.4.0-a behaviour (no auto-apply, no
  goroutines, no fan-out) for installations
  that run an external config manager.

## Verification

- `go test -count=1 ./...` — 23/23 packages PASS
  (new `internal/cores/builder` package with 6
  tests)
- `golangci-lint run ./...` — 0 issues
- `gofmt -l .` — clean
- Local smoke: `go build ./cmd/aegis` produces a
  binary that boots with the dev seed admin;
  `internal/cores/builder/flushfn_smoke_test.go`
  exercises the full `Build → Render → Apply`
  pipeline against a `httptest` fake agent

## Follow-ups (deferred to PR E2)

- Integration test against a live pg + real
  aegis-agent (smoke-local.sh covers part of
  this; the in-tree `go test -tags=integration`
  variant is out of scope for this PR)
- Narrowing `users.enqueueUserDelta` to the
  nodes matching `HostsAllowlist` /
  `HostsBlocklist` once the multi-user renderer
  is in place
- Node state-change trigger (online ↔ offline
  → enqueue + spawn a new applier for the new
  online node) — current behaviour covers the
  online set at boot only
