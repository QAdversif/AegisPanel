# refactor(backend): extract `internal/app.Build` from main.go

Closes audit finding #1 (God-object `main.go` from the
2026-08-01 colleague review).

## What

`cmd/aegis/main.go` was 728 lines and owned the entire
composition root: pg pool open, migrations, nine
`switch cfg.XBackend` blocks, eleven service constructors,
router wiring, http.Server, retry worker, and signal
handling. After this PR the binary is 199 lines and
keeps only the cmd-level concerns:

- logger setup
- subcommand dispatch (`aegis migrate`, `aegis admin`)
- singbox per-node BatchedApplier wiring
- signal handling + graceful shutdown

Everything else moved to a new `internal/app` package
exposing a single `Build(ctx, cfg) (*App, error)` plus an
`App.Close()` for the retry worker and the pg pool.

## Why manual DI, not google wire

We considered wire. Eleven services is well below the
threshold where wire's compile-time graph validation
pays for the codegen + lost `golangci-lint` reach over
generated code. The colleague review also arrived at the
same conclusion (see below). The result is the wire
sweet-spot pattern from the Go community:

- one generic `MustBuild[T]` helper with a
  `StoreBuilder[T]` struct
- one `App` struct that holds all wired handles
- centralized production-vs-memory check in `MustBuild`

If a future PR crosses 30+ services, revisit wire.
For now the `internal/app` package is ~530 lines and
self-contained.

## Files

- new `backend/internal/app/app.go` (410 lines)
  - `App` struct (Config, Pool, 11 services, SubLimiter,
    Router, Server, webhooksWorkerCancel)
  - `Build(ctx, cfg) (*App, error)`
  - `App.Close()`
  - `openPgPoolIfNeeded`, `needsPg`, `mustHashDevPassword`,
    `newSubscriptionRateLimiter` helpers
- new `backend/internal/app/stores.go` (110 lines)
  - `StoreBuilder[T any] { Name, Backend, PgCtor, MemCtor, Env }`
  - `MustBuild[T any](pool, b) T`
- new `backend/internal/app/app_test.go` (180 lines)
  - `TestBuild_AllMemoryBackends` — wires Build with
    every AEGIS_*_BACKEND=memory, asserts every
    service handle is non-nil and Pool is nil, runs
    Close() twice (idempotency check)
  - `TestBuild_ProductionMemoryBackend_Refused` —
    pins the production+memory ban; documents the
    log.Fatal branch lives in `MustBuild` and is
    covered end-to-end by `tools/scripts/smoke-local.sh`
    from PR #152
- `backend/cmd/aegis/main.go` (-556, +71)
  - imports cleaned (14 packages removed; one
    `internal/app` added)
  - duplicate `mustHash` removed (now
    `mustHashDevPassword` in app.go)
  - duplicate `newSubscriptionRateLimiter` removed
    (now in app.go)
  - `singboxWiring`, `singboxNodeResolver`, `promptPassword`,
    `runMigrate`, `runAdmin`, and the `admin` flag parsers
    stay in main.go per the `internal/app/app.go` docstring
- `backend/internal/router/router.go` (9 lines)
  - `Build` now takes `ctx context.Context` as the
    first parameter and uses it for the
    `panelcfgSvc.GetActive` read at the rotated
    sub_path mount (was hardcoded
    `context.Background()`; now the boot context
    applies, so a SIGINT during boot aborts the
    read)
- `backend/internal/router/router_test.go` (1 line)
  - pass `context.Background()` to `Build` in the
    test helper

## Behaviour changes

None observable from the operator's perspective. The
boot sequence is identical: same env vars, same
warning logs, same dev seed admin, same graceful
shutdown, same SIGINT handling. The only diff is
where the code lives.

## Verification

- `go build ./...` clean
- `go test -count=1 ./...` — 22/22 packages PASS
  (including new `internal/app` smoke tests)
- `golangci-lint run ./...` — 0 issues (touches
  `contextcheck`, `gofmt`; both satisfied)
- `gofmt -l internal/app internal/router` clean
- Local smoke test still runs (aegis binary starts;
  memory auth store; the same dev seed admin is
  wired)
- Integration suite (`go test -tags=integration`)
  and the race detector are out of scope for this
  PR's local verification but the CI backend job
  covers them

## Follow-ups (deferred)

- Audit #2 (BatchedApplier no-op stub) — separate
  PR. The `singboxWiring` function still lives in
  main.go; lifting it into a method on `App` is the
  follow-up once the user-management layer lands.
- Wire `Service.Dispatch` audit-log call-sites —
  separate batch. The pattern is now uniform across
  every service (`WithWebhooks` setter, see PR #148).
