# feat(cores): BatchedApplier + real Apply transport + AgentBearer storage

v0.4.0-mvp-batched lands. This is the first of three
sub-PRs that close the 3 end-to-end blockers from the
post-v0.3.0 audit:

- [x] `cores/singbox.Apply` → HTTP-клиент к агенту
      (this PR; v0.3.0 stub replaced)
- [ ] Agent `/v1/apply` writes to disk and reloads sing-box
      (v0.4.0-b)
- [ ] Ansible `install_singbox` role
      (v0.4.0-c)

When all three merge, the panel can drive a real sing-box
node end-to-end: render config → POST /v1/apply → agent
writes `/etc/sing-box/config.json` → `systemctl reload
sing-box`.

## What changed

### New: `internal/cores/batched.go` + `batched_test.go`

The core-agnostic BatchedApplier. The §7.5 pseudocode from
ARCHITECTURE.md is now real, tested Go. Key design
points:

- `Delta{Kind, UserID, Payload, Enqueued}` with
  `DeltaKind = "add_user" | "remove_user" | "set_limit"`.
- `BatchedApplier.Run(ctx)` loop: receives deltas on
  `b.queue`, merges them into `b.pending` (with the
  §7.5 cancel/replace rules), flushes either when the
  ticker fires (default 20s) or when `len(pending) >=
  maxQueue` (default 1000).
- Cancel/replace: `AddUser + RemoveUser` for the same
  `UserID` within the same window cancel out (both
  dropped). Same-kind deltas collapse last-write-wins.
  SetLimit always updates.
- `FlushFn func(ctx, deltas []Delta) error` callback
  with the actual render+apply logic — the applier
  itself does not know about cores, nodes, or HTTP.
  Errors are logged, not returned (the next window
  must still happen).
- Per-node design: each node gets its own
  `BatchedApplier`, with its own queue. One
  `Run()` goroutine per node.

Tests (`batched_test.go`, all passing locally):
- `TestBatchedApplier_CoalescesDeltasInWindow` — 5
  deltas in 100ms → 1 flush
- `TestBatchedApplier_CancelReplace` — AddUser +
  RemoveUser same user → no-op; AddUser + AddUser
  → collapse
- `TestBatchedApplier_MaxQueueTriggersImmediateFlush` —
  10 deltas with `maxQueue=10` and a 1s window → 1
  flush without waiting for the ticker
- `TestBatchedApplier_FlushErrorDoesNotCrashLoop` —
  FlushFn returns an error → logged, loop continues
- `TestBatchedApplier_GracefulShutdownDrains` —
  `ctx.Cancel()` → `Run()` returns `ctx.Err()` and
  does NOT flush (best-effort drain only)

### Changed: `internal/cores/singbox/apply.go`

The v0.3.0 stub returning `ErrApplyNotImplemented` is
replaced by a real HTTP POST. The wire contract is
identical to what the v0.3.0 (and v0.4.0-b) agent expects:

- URL: `http://<node.Address>/v1/apply`
- Method: `POST`
- Headers: `Authorization: Bearer <bearer>`,
  `Content-Type: application/json`
- Body: `{"config": <rendered>}` (the v0.3.0
  envelope; v0.4.0-b does not change it)
- Response: any 2xx → success; 4xx/5xx → wrapped
  error with the body (truncated to 512 bytes)

Dependency injection via `Configure(nodes, client)`:

- `NodeResolver` interface (defined in singbox
  package to avoid a nodes import cycle) returns
  `(address, bearer string, err error)` for a
  node UUID.
- `httpClient` interface is satisfied by
  `*http.Client` and by `httptest.Server.Client()`
  in tests.
- `NewHTTPClient()` returns the default
  `*http.Client` with a 30s per-request timeout
  (the BatchedApplier's window is the effective
  budget).
- The exported `ErrApplyNotConfigured` replaces
  the v0.3.0 `ErrApplyNotImplemented`. The
  old test was updated accordingly.

Tests (`apply_test.go`, all passing locally):
- `TestApply_HappyPath` — agent returns 200, body
  shape matches, `Authorization: Bearer <bearer>`
  set, `Content-Type: application/json` set
- `TestApply_AgentError4xx` / `_5xx` — wrapped
  error with status + body
- `TestApply_NetworkError` — connection refused
- `TestApply_InvalidNodeID` — non-UUID → error
  before any HTTP call
- `TestApply_ResolverError` — propagated, no HTTP
  call
- `TestApply_NotConfigured` — `ErrApplyNotConfigured`
  when `Configure` was never called
- `TestApply_EmptyAddress` — resolver returns ""
  → error
- `TestApply_ContextCanceled` — `ctx.Cancel()`
  before `Apply` → error

### Changed: `internal/cores/singbox/singbox.go`

- `ProviderVersion` bumped from `"1.8.0"` to
  `"1.14.0-beta.2"` to match the version the
  `install_singbox` Ansible role will deploy
  (v0.4.0-c). The Go provider's render output
  is backward-compatible across the 1.8.x →
  1.13.x range; if a regression in 1.14.0-beta.x
  breaks the rendered config, the fix is a
  bump back to a 1.13.x GA release.
- `Provider` struct gets two new fields:
  `nodes NodeResolver` and `client httpClient`.
  Zero value still works for every method
  except `Apply` (which returns
  `ErrApplyNotConfigured` until `Configure` has
  been called).

### New: migration `0013_nodes_agent_bearer.sql`

Adds `agent_bearer TEXT NOT NULL DEFAULT ''` to the
`nodes` table. v0.3.0 minted a fresh bearer on every
Provision and only wrote it to the node's
`/etc/aegis/agent.env`; v0.4.0 stores it on the row
so the panel can ship `POST /v1/apply` later. The
column defaults to `''`; v0.3.0 nodes that were
provisioned before the migration will have an
empty bearer until a Re-Provision regenerates it.

### Changed: `internal/nodes/*`

- `Node` struct gets `AgentBearer string` field
  (`json:"-"` — never serialised to the panel
  HTTP API; consumed only by the singbox
  package's HTTP transport).
- `Store` interface gets `SetAgentBearer(ctx, id,
  bearer) error`. Both `MemoryStore` and
  `PgStore` implement it.
- `PgStore`'s `nodeWithTagsSelect` reads the new
  column. `insertNode` writes it.
- `BootstrapNodeProvider.GetByID` populates
  the new `NodeRow.AgentBearer` field.

### Changed: `internal/bootstrap/provisioner.go`

The provisioner now persists the freshly-minted
bearer to the node row after a successful install.
Failure to persist (DB transient error) flips the
row to `offline` so the operator knows the panel
cannot talk to the agent until re-provisioned.

### Changed: `cmd/aegis/main.go`

Wires the singbox `Configure(nodesSvc, client)` at
boot and spawns one `BatchedApplier` per
`state = online` node with a `Run()` goroutine.
The `FlushFn` is a no-op for this PR — the
user-management layer that calls
`BatchedApplier.Enqueue` is the next slice (v0.4.0-b
or rolled into a follow-up). The wiring is
otherwise complete.

## Why no user-management Enqueue calls

This PR is the infrastructure. The user-management
layer (AddUser, RemoveUser, SetLimit) is its own
slice — it touches the `internal/users/` package,
the `users` table, the subscription views, the
host-allocation logic, etc. Mixing it into v0.4.0-a
would inflate the PR past the 1k-line threshold
where review quality drops. The split is the
v0.3.0 pattern (a / b / c) applied to v0.4.0.

## What did NOT change

- No source-level changes to any other CoreProvider
  (the `noop` provider still does nothing — the
  new `Configure` is opt-in via a separate method
  on the singbox package).
- No changes to the public HTTP API surface. The
  new `Apply` flow is internal to the cores
  package + nodes package; no new endpoints.
- No changes to the agent (still v0.3.0: receives
  `POST /v1/apply`, validates JSON, ACKs).
  v0.4.0-b makes the agent do the actual work.

## Files

```
backend/cmd/aegis/main.go                         | 136 ++++++++++++++++-
backend/internal/bootstrap/provisioner.go         |  63 ++++++-
backend/internal/bootstrap/provisioner_test.go    |  12 ++
backend/internal/cores/batched.go                 | 226 ++++++++++++++++ (new)
backend/internal/cores/batched_test.go            | 215 ++++++++++++++++ (new)
backend/internal/cores/singbox/apply.go           | 217 ++++++++++++++++++------
backend/internal/cores/singbox/apply_test.go      | 257 +++++++++++++++++++ (new)
backend/internal/cores/singbox/singbox.go         |  42 +++-
backend/internal/cores/singbox/singbox_test.go    |  16 +-
backend/internal/nodes/handler.go                 |  20 ++-
backend/internal/nodes/node.go                    |  13 ++
backend/internal/nodes/pg_store.go                |  27 ++-
backend/internal/nodes/store.go                   |  36 ++++
backend/migrations/0013_nodes_agent_bearer.sql    |  36 +++ (new)
```

Total: 14 files, +1,316 / -27.

## Verified

- `go build ./...` — clean
- `go test ./internal/... -short` — all packages
  green; 5 new BatchedApplier tests + 9 new
  Apply tests + the updated
  `TestProvider_Apply_NotConfigured` all pass
- `go vet ./...` — clean
- No lint changes (the existing
  `golangci-lint v2` config picks up
  the new code without flags)
