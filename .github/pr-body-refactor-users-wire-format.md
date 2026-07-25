# refactor(users): align User type to subscription wire format

## What this PR does

Prerequisite for the v0.4.0-d consolidation (Path C of
the d.1 audit). This change makes `users.User`
wire-format-compatible with the existing
`subscription.User` so the two types can be unified
in a follow-up commit without breaking the public
API.

After this PR, the two `User` structs are JSON-
shape-identical. The two Go types are still distinct
(deferred to d-refactor.2 through d-refactor.5), but
the wire format no longer differentiates them, so a
future migration can swap one for the other without
breaking any API consumer.

## Why this PR

The d.1 audit (recorded in agent memory 2026-07-25)
found that `internal/users/` (new in PR #95) and
`internal/subscription/` (pre-existing) had parallel
`User` implementations with **two wire-format
incompatibilities**:

1. **JSON tag style**: `users.User` used camelCase
   (`planId`, `expireAt`, `subToken`); `subscription
   .User` used snake_case (`plan_id`, `expire_at`,
   `sub_token`).
2. **Hosts allow/block list type**: `users.User`
   used `[]string`; `subscription.User` used
   `[]uuid.UUID`. The DB column is JSONB and accepts
   either, but the Go type differed.

This PR aligns `users.User` to `subscription.User`'s
shape on both axes. **The wire format does not
change** — `subscription.User` is the canonical
shape (already shipped in v0.2.0), and `users.User`
now matches it.

## Files changed

- `backend/internal/users/user.go` — JSON tags
  switched from camelCase to snake_case (matching
  `subscription.User` exactly). `HostsAllowlist` /
  `HostsBlocklist` changed from `[]string` to
  `[]uuid.UUID`. Doc comment updated with a
  "JSON wire format" section explaining the
  canonical-shape decision.
- `backend/internal/users/service.go` —
  `validateStringList` / `ensureStringList` renamed
  to `validateUUIDList` / `ensureUUIDList` with
  `[]uuid.UUID` signature. `CreateInput` /
  `UpdateInput` fields switched to `[]uuid.UUID`.
  The "no zero UUID" check replaces the "no empty
  string" check. The unused `strings` import was
  dropped (the username format validator uses
  byte-indexing, not `strings` primitives).
- `backend/internal/users/pg_store.go` — scan path:
  empty-slice default changed from `[]string{}` to
  `[]uuid.UUID{}` to match the new field type.
- `backend/internal/users/service_test.go` —
  `dup-allowlist-entry` subtest updated to use the
  same UUID twice (the previous `"a", "a"` string-
  dedup pattern doesn't apply to UUIDs which are
  unique by definition; the test now uses a
  package-level `dupUUID` constant).

## Wire format

Before this PR:
- `subscription.User` (canonical, shipped v0.2.0):
  snake_case, `[]uuid.UUID`
- `users.User` (new in #95):
  camelCase, `[]string`

After this PR:
- `subscription.User`: unchanged
- `users.User`: snake_case, `[]uuid.UUID` ← matches

No external API consumer sees a change. The
existing `subscription.AdminRouter` (mounted at
`/api/v1/users` in `router.go:116`) returns the
same JSON it always did. The new `users` package
wasn't yet mounted anywhere in `cmd/aegis/main.go`,
so no caller of the new type is affected either.

## Verification

- `go build ./internal/users/...` — clean
- `go test ./internal/users/...` — 22 unit tests pass
- `go test -tags=integration ./internal/users/...` —
  integration test passes
- `go test ./...` — all 17 packages pass (no
  regressions in `subscription`, `router`, `inbounds`,
  `auth`, etc.)
- `golangci-lint v2` with `backend/.golangci.yml`
  `-tags=integration ./internal/users/...` — 0 issues

## Out of scope (deferred to d-refactor.2-5)

- Move user-CRUD Store methods from
  `subscription.Store` to `users.Store`
  (`GetUserBySubToken`, `GetUserByID`, `CreateUser`,
  `UpdateUser`, `ListUsers`, `UpdateSubToken`).
- Move user-CRUD Service methods from
  `subscription.Service` to `users.Service`
  (`CreateUser`, `ListUsers`, `GetUserBySubToken`,
  `RotateSubToken`).
- Move `admin_handler.go` from `subscription/` to
  `users/`.
- Rewire `cmd/aegis/main.go` + `internal/router/router.go` +
  `internal/router/router_test.go` to use `users.AdminRouter`
  instead of `subscription.AdminRouter`.
- Delete the now-redundant user CRUD from
  `subscription`.

The end state (after d-refactor.5): one canonical
`users` package owns user CRUD; `subscription` owns
only the render layer (sing-box / Clash / base64 /
HTML / port helpers / html_qr) and the public
`/sub/{token}` handler.

## Refs

- PR #95 (d.1 — first sub-PR of v0.4.0-d; landed the
  duplicate `users` package)
- Agent memory "AUDIT 2026-07-25: дублирование `users`
  vs `subscription`" (full Path C plan, recorded
  after the d.1 audit revealed the duplication)
- `internal/subscription/subscription.go:107-124` —
  the canonical `User` shape this PR aligns to
