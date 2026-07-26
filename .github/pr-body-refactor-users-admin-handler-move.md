# refactor(users): move `subscription/admin_handler.go` to `users/admin_handler.go` (d-refactor.3)

## TL;DR

The user-CRUD admin surface (mounted at `/api/v1/users`)
moves out of `internal/subscription` and into
`internal/users`. The subscription package is now a pure
render orchestrator: no user-CRUD store, no user-CRUD
service methods, no admin handler. The render handler
(`/sub/{token}`) consults `*users.Service` directly for
the `sub_token`-→-user lookup.

This is the second sub-step of the v0.4.0-d Path C
consolidation. d.1 + d-refactor.1 aligned the wire
format; d-refactor.2 dropped the subscription-side
Store / MemoryStore / PgStore user-CRUD; this PR drops
the Service-level thin wrappers and moves the admin
handler. d-refactor.4 (the next PR) is the cleanup
pass + `docs/ROADMAP.md`.

## What's in this PR

### `internal/users` — owns the user-CRUD admin surface

- `admin_handler.go` (new, ~370 lines) — moved
  verbatim from `internal/subscription/admin_handler.go`.
  The handler methods are now methods on
  `*users.Service`; the chi router is
  `users.AdminRouter(usersSvc, authMiddleware)`.
  The d-refactor.2 `writeUserError` (which knew
  both error spaces) shrinks back to the users
  package's own error space: `users.ErrNotFound` /
  `users.ErrDuplicate` / `users.ValidationError` (via
  `errors.Is` / `errors.As`).
- `admin_handler_test.go` (new) — the 11-test
  admin-handler suite moves with the handler.
  Updated to use `users.StatusActive` /
  `users.StatusGrace` (no more `UserStatusActive`
  aliasing through the subscription package).
- `rotation_test.go` (new) — the d.0 sub_token
  rotation tests (also moved from
  `internal/subscription/rotation_test.go`) now
  exercise `users.Service.RotateSubToken` and
  `users.Service.GetBySubToken(ctx, token, true)`
  directly. The `DefaultSubTokenRotationGrace = 24h`
  constant is re-exported from the users package;
  d-refactor.4 will either drop it (it duplicates
  the users-internal default) or keep it as a
  canonical public alias.
- `service.go` — added a public `Store() Store`
  accessor on `*users.Service` so tests can
  reach the underlying `*users.MemoryStore` for
  in-memory mutations (e.g. force-expire a user
  to test the 403 path). Documented as test-only;
  production code would not call this.

### `internal/subscription` — pure render orchestrator

- `admin_handler.go` — DELETED (moved to
  `internal/users`).
- `admin_handler_test.go` — DELETED (moved with the
  handler).
- `rotation_test.go` — DELETED (moved to
  `internal/users/rotation_test.go`).
- `service.go` — drops `users` / `usersSvc` fields
  from the Service struct; drops the four thin
  wrappers (`GetUserBySubToken` / `RotateSubToken` /
  `CreateUser` / `ListUsers`) that delegated to
  `users`; the 2-argument NewService signature
  becomes a 4-argument one
  (`store, hostsSvc, nodesSvc, inboundsSvc`).
  `SetClock` no longer propagates to the users
  package.
- `errors.go` — drops `ErrNotFound` and
  `ErrDuplicate` sentinels (the user-CRUD error
  space now lives in `users`); keeps
  `NotFoundError` / `ValidationError` /
  `UserNotLiveError` (used by the render-side
  resolvers).
- `subscription.go` — drops `CreateUserInput`
  alias (the admin handler is in `users` now and
  uses `users.CreateInput` directly); keeps
  `User` / `UserStatus` aliases (the render code
  reads `*User` for `username`, `status`, etc.).
- `handler.go` — `Router` / `RouterWithLimiter` /
  `NewHandler` now take a `*users.Service`
  reference (for the `sub_token` lookup). The
  render handler's `lookupUserBySubToken` is a
  thin wrapper over `users.Service.GetBySubToken`
  that translates `users.ErrNotFound` into
  `subscription.NotFoundError` so the existing
  `writeServiceError` 404 mapping keeps working.
- All 6 test files in the subscription package
  updated: `NewService(store, hosts, nodes, inbounds)`
  signature; `users` import dropped where unused.

### `cmd/aegis` + `internal/router` — wire users

- `cmd/aegis/main.go` — passes `usersSvc` (the
  already-constructed `*users.Service`) to both
  `subscription.NewService` (no — see below) and
  `router.Build`. The `NewService` call drops the
  `usersStore` and `usersSvc` arguments.
- `internal/router/router.go` — `Build` takes
  `usersSvc *users.Service` as a new parameter;
  the `/users` mount switches to
  `users.AdminRouter(usersSvc, authSvc.Middleware())`.
  The `/sub` mount passes `usersSvc` to
  `subscription.RouterWithLimiter`.

## Why this is the right cut

The d-refactor.2 PR's 4 thin wrappers kept the
external surface stable (router.Build, subscription.AdminRouter,
etc. all kept their old signatures). That was a
"safe intermediate state" for the diff review.
But the wrappers added ~120 lines of pure pass-through
code that had no real consumers outside the
admin handler (which itself was about to move).

By cutting the wrappers AND the admin handler in
one PR, we get a single coherent state where:

- The `users` package owns everything user-CRUD.
- The `subscription` package owns the render path
  and the plan / pool / member join tables.

The alternative (move the handler first, drop the
wrappers in a follow-up) would have produced an
intermediate state where the handler is in `users`
but the `Service.GetUserBySubToken` etc. are still
on the `subscription` package — two callers
(`admin_handler_test.go` and the render handler)
would have to learn the new package layout in two
separate PRs.

## Behaviour changes

None. The d-refactor.2 PR's two behaviour changes
(64-hex `sub_token`, 24h rotation grace) carry
through unchanged. The d-refactor.2 thin wrappers
were exact pass-throughs; dropping them is a no-op
for callers because the call sites were inside the
moved admin handler.

## Follow-ups (d-refactor.4)

- Drop the `DefaultSubTokenRotationGrace`
  re-export in `users/rotation_test.go` (or move
  it to a `users` package-level public constant).
- Add `docs/ROADMAP.md` with the milestone ladder
  (v0.5.0 / v0.6.0 plans / v0.7.0 webhooks / v1.0
  soft launch).
- Trim the `subscription` package doc comment
  (remove the "AEGIS_USERS_BACKEND" reference; the
  d-refactor.2 doc update is now redundant).
- Audit `internal/users` for any test fixture that
  still constructs `users.NewMemoryStore(nil)`
  (the canonical pattern in d.1 is to pass a
  `clock` callback; the few nil-clock sites are
  fine but worth one sweep for consistency).

## Verification

- `go build ./...` clean
- `go test ./...` all green
- `go test -tags=integration -run xxxxxxxx ./...`
  compiles (full integration suite runs in CI
  with a service-container Postgres)
- `golangci-lint run --config backend/.golangci.yml ./...`
  with `GOFLAGS=-tags=integration`: 0 issues
