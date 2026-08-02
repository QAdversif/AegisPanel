# test(cores): end-to-end integration test for BatchedApplier + FlushFn

Closes PR E2 (the second half of audit #2 from the
2026-08-01 colleague review). PR #157 wired the
real FlushFn + Enqueue; this PR adds the
integration test that pins the end-to-end
panel→agent pipeline against a real Postgres.

## What

The smoke test in `flushfn_smoke_test.go`
exercises the panel→sing-box pipeline with the
MemoryStore path. That test runs on every
`go test ./...` and catches the wiring
regressions. This PR adds the real-PgStore
half:

- `testutil.MustNewPool` (CI's backend job
  spins up pg via the `services: postgres`
  block in `.github/workflows/ci.yml`)
- `*inbounds.Service` + `*users.Service` run
  against their `PgStore` variants
- The FlushFn reads through that PgStore on
  every window, so the integration test is
  the only place a "the panel wrote to pg and
  the FlushFn picked it up via SELECT"
  regression surfaces

## File

- new `backend/internal/cores/builder/flushfn_integration_test.go`
  (220 lines, `//go:build integration`)
  - `TestIntegration_EndToEnd_RealPgCreateUserTriggersApply`
    is the headline test: the panel persists a
    user via `users.Service.Create` (real pg
    INSERT), the post-commit enqueue reaches
    the per-node BatchedApplier, the 200ms
    window fires, the FlushFn re-renders the
    sing-box config (reading through the
    inbounds PgStore), and the fake agent
    receives a POST /v1/apply with the correct
    envelope shape. The body is asserted to
    carry exactly one vless inbound tagged
    `vless-integration` with the
    `11111111-2222-3333-4444-555555555555`
    UUID we seeded in `inb.Params["uuid"]`.
  - Skip semantics: the test self-skips when
    `INTEGRATION_DATABASE_URL` is unset
    (CI's `go test -count=1 -tags=integration`
    job sets it; local `go test ./...` skips
    cleanly).

## Behaviour changes

None. Test-only addition.

## Verification

- `go test -count=1 ./...` — 23/23 packages PASS
  (the new file is `//go:build integration` so
  it is excluded from the default build)
- `go test -count=1 -tags=integration ./internal/cores/builder/`
  — local run SKIPs (no pg on this dev box);
  CI's backend job runs the full integration
  suite against the `services: postgres`
  container
- `go vet -tags=integration ./internal/cores/builder/`
  — clean
- `golangci-lint run ./...` — 0 issues
- `gofmt -l .` — clean

## Follow-ups (deferred)

- A second integration test exercising the
  "user Update with SetLimit emits DeltaSetLimit
  with the correct bytes payload" path. The
  unit tests cover the fan-out logic; the
  integration test would be the byte-exact
  Apply body. Skipped to keep this PR focused
  on the headline scenario; the SetLimit path
  is a Phase 2 multi-user-renderer concern.
- A test that asserts the FlushFn is
  idempotent: two consecutive Enqueues for
  the same user coalesce to a single Apply.
  The BatchedApplier's unit tests cover this
  already; the integration variant would
  assert the wire side.
