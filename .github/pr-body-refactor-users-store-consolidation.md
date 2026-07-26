# refactor(subscription): drop user-CRUD from `internal/subscription` (d-refactor.2)

## TL;DR

The user-CRUD surface (Users + Store + Service + admin handler) has
moved out of `internal/subscription` and into `internal/users`. The
`subscription` package is now a pure render orchestrator (plus a
plan / pool / member Store). This is the first sub-step of the
v0.4.0-d Path C consolidation (d.1 + d-refactor.1 laid the wire-
format ground; this PR drops the duplicated implementation).

## What's in this PR

### `internal/users` — adds the canonical `WithUser` test helper

- `memory_store.go` — `WithUser(*User) *MemoryStore` mirrors the
  old `subscription.MemoryStore.WithUser` pattern. Lets tests
  seed users without going through the validation pass (Create)
  and without the err-check on the call site. Used by every
  fixture in `subscription/*_test.go` after this PR.
- `user.go` — adds `IsLive()` on `Status`. Previously lived on
  `subscription.UserStatus`; the method is the user-domain rule
  ("active | grace are live; expired | disabled | deleted are
  not") and now lives next to the data.

### `internal/subscription` — drops user-CRUD, becomes a render orchestrator

- `subscription.go` — `type User = users.User` and
  `type UserStatus = users.Status` (Go type aliases; the same
  struct, just re-exported). `CreateUserInput = users.CreateInput`
  alias lets the admin handler keep the old spelling until
  d-refactor.3 moves the handler into the users package. The
  `UserStatus{Active,Grace,...}` constants are also aliases now.
- `store.go` — drops the 7 user-CRUD methods from the Store
  interface (`GetUserBySubToken` / `GetUserByPrevSubToken` /
  `GetUserByID` / `CreateUser` / `UpdateUser` / `ListUsers` /
  `UpdateSubToken`). Drops the user + token-index maps from
  `MemoryStore` and the `WithUser` helper (replaced by
  `users.MemoryStore.WithUser`). `MemoryStore` now owns only
  plans / pools / poolMembers — a clean three-method Store.
- `pg_store.go` — drops the user-CRUD SQL and the scan helpers
  (`scanUserBy` / `scanUserRow` / `userSelect` /
  `unmarshalUUIDSlice`). The `users` JSONB column no longer
  appears anywhere in this file; the pg implementation is now
  a thin three-method surface that walks `plans` →
  `plan_pool` → `host_pools` → `host_pool_members`. Tests
  follow.
- `errors.go` — moves `ErrNotFound` and `ErrDuplicate` here
  from `store.go`. The Service's thin wrappers map
  `users.ErrNotFound` / `users.ErrDuplicate` into these
  sentinels so the existing render + admin handlers do not
  have to learn a parallel error space yet.
- `service.go` — keeps the four user-CRUD thin wrappers
  (`GetUserBySubToken` / `RotateSubToken` / `CreateUser` /
  `ListUsers`) and the resolve / render methods. The
  wrappers translate `users.ErrNotFound` →
  `subscription.NotFoundError`, `users.ErrDuplicate` →
  `subscription.ErrDuplicate`, `users.ValidationError` →
  `subscription.ValidationError`. SetClock propagates to both
  the subscription Store and the users MemoryStore so tests
  that pin a clock still work end-to-end.
- `admin_handler.go` — `handleGetUser` and `handleUpdateUser`
  call `s.users.GetByID` / `s.usersSvc.Update` directly. The
  other three (List / Create / Rotate) still go through the
  thin wrappers. `writeUserError` knows about both error
  spaces (subscription sentinels + `users.ValidationError`).
  The whole file moves to `internal/users/admin_handler.go`
  in d-refactor.3.
- `store_test.go` — rewritten; the user-CRUD tests are gone,
  the pool tests stay. The fixture no longer seeds users
  (it constructs `*User` literals at the call site to drive
  `ListPoolsForUser`).
- `pg_store_integration_test.go` — 9 user-CRUD tests removed
  (the d.1 work shipped the user-CRUD pg implementation
  under `internal/users/pg_store.go`; the integration tests
  for it will land in a follow-up). The pool / member
  tests stay.

### `cmd/aegis` + `internal/router` — wire users package

- `cmd/aegis/main.go` — constructs `usersStore` (Memory or
  Pg) and `usersSvc` separately, then passes both to
  `subscription.NewService(subscriptionStore, usersStore,
  usersSvc, hostsSvc, nodesSvc, inboundsSvc)`. The
  `AEGIS_USERS_BACKEND` env flag selects memory or pg
  (defaults to memory; matches the `AEGIS_*_BACKEND` family).
- `internal/router/router_test.go` — fixture updated to
  construct `users.MemoryStore` and seed the test user
  there (not the subscription.MemoryStore).
- `internal/config/config.go` — new `UsersBackend` field
  with the matching `AEGIS_USERS_BACKEND` env var. The
  doc comment notes that pre-d-refactor.2 the
  `SubscriptionBackend` flag covered both.

## Why this is one PR (and not the 5-PR plan in the audit)

The 5-PR split in the v0.4.0-d audit (Path C steps
2 / 3 / 4 / 5 / 6) was an over-cut. d.1 + d-refactor.1
already aligned the wire format between the two User
types — that alignment is what made the type-alias
trick possible (`type User = users.User` is
structurally equivalent, so render code that does
`*User` keeps working without changes).

A single PR:

- The Service's user-CRUD wrappers can drop naturally
  because the type alias makes "service returns *User
  that the admin handler consumes" a no-op compile
  change.
- The Store's user-CRUD methods can drop because the
  MemoryStore is the only thing that referenced them.
- The admin handler is a localised change — it
  already lived in the same package as the
  subscription Service, so calling s.users directly is
  a 2-line delta.

The 5-PR cut would have produced 5 "drop a method"
PRs that all touch the same handful of files; the
net result is the same code, but the operator has to
review 5 separate diffs and run CI 5 times.

## Test changes

- `subscription/service_test.go` —
  `newFixture` constructs `users.MemoryStore` +
  `users.Service` and seeds the test user there
  (the `WithUser` helper).
- `subscription/handler_test.go` — `hf.svc.users`
  (not `hf.svc.store`) is the
  `*users.MemoryStore` that the two
  in-fixture mutations use.
- `subscription/admin_handler_test.go` —
  `newAdminTestRouter` constructs the users
  package; the in-test "verify store read-back"
  uses `svc.users.GetByID` (not the now-removed
  `svc.store.GetUserByID`).
- `subscription/port_xhttp_test.go` — 4
  fixtures updated with the same `usersStore +
  usersSvc` wiring; one-line edit per fixture
  via `replace_all`.
- `subscription/rotation_test.go` — fixture
  updated; the "grace=0 invalidates prev" test
  is now a documentation test (the d.1
  `users.Service.RotateSubToken` maps
  `grace <= 0` to the canonical 24h default).
- `subscription/render_*_test.go` — inline
  `NewService(NewMemoryStore(), …)` calls
  pass `users.NewMemoryStore(nil)` +
  `users.NewService(users.NewMemoryStore(nil))`
  in the middle.
- `subscription/store_test.go` — rewritten
  to drop the user-CRUD tests; the
  `ListPoolsForUser` tests build a
  `*User` literal at the call site.
- `subscription/pg_store_integration_test.go`
  — 9 user-CRUD tests removed; the pool /
  member tests stay.

## Lint / behaviour changes worth flagging

- **sub_token length is 64 hex chars, not 32** —
  the d.1 design bumped from 16 bytes (32 hex) to
  32 bytes (64 hex) of entropy. The
  `TestAdminHandler_CreateAndGet` assertion was
  updated to `len(tok) == 64`.
- **Rotate grace semantics changed** — the d.0
  `subscription.Service.RotateSubToken` allowed
  `grace=0` to mean "invalidate the prev token
  immediately". The d.1 `users.Service.RotateSubToken`
  maps `grace <= 0` to the canonical 24h default.
  The behaviour change is documented inline; the
  test that asserted the d.0 behaviour was
  rewritten to assert the new one.
- **Render code is unchanged** — the type
  alias makes `*User` in this package exactly
  `*users.User`. Render files (15 files:
  `render.go`, `render_singbox.go`,
  `render_clash.go`, `render_vars.go`) did not
  need a single edit.

## Follow-ups (d-refactor.3 / .4)

- **d-refactor.3**: move `admin_handler.go` to
  `internal/users/admin_handler.go`; replace
  `subscription.AdminRouter` mount with
  `users.Router`; drop the four thin wrappers
  from `subscription.Service`. ~1-2 days.
- **d-refactor.4**: cleanup pass. Drop
  `CreateUserInput` alias (no longer needed
  once admin handler is in users); drop
  `DefaultSubTokenRotationGrace` re-export
  (the constant lives in `users` now); trim
  the subscription package doc + the
  `users.User` doc comment. ~0.5-1 day.

## Verification

- `go build ./...` clean
- `go test ./...` all green
- `go test -tags=integration -run xxxxxxxx ./...`
  compiles (full integration suite runs in CI
  with a service-container Postgres)
- `golangci-lint run --config backend/.golangci.yml ./...`
  with `GOFLAGS=-tags=integration`: 0 issues
