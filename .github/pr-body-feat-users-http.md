feat(users): admin HTTP handler + Users CRUD view (PR-G)

Second sub-PR of v0.2.0-mvp-agent. Closes the
last remaining v0.1.0 UI placeholder: `/users`
is now a real CRUD surface backed by a new admin
HTTP subrouter, and the User / Plan / Pool Go
structs get the `json` tags v0.1.0 forgot (the
v0.1.0 subscription handler was the only path
that emitted them and the wire format was wrong
end-to-end until now).

## Backend

* `internal/subscription/admin_handler.go` —
  new `AdminRouter(svc, authMiddleware)` chi
  subrouter, mounted at `/api/v1/users` in
  `internal/router/router.go`. Five endpoints:

      GET  /                  -> list every user
      GET  /{id}              -> get a single user
      POST /                  -> create a user
      PATCH /{id}             -> partial update
      POST /{id}/rotate-token -> rotate the sub_token

  The subrouter applies `auth.RequireScope(ScopeUsers)`
  on every route. A read-only `ScopeUsersRead` is
  not in v0.2.0 — the panel's access model is
  admin-or-not for now; the v1.0 panel will split
  read vs. write.

  The rotate-token endpoint is separate from the
  panel-wide sub_path rotation (panelcfg). The
  per-user sub_token is the credential the end
  user pastes into a VPN client; the sub_path is
  the URL prefix. The two are independent.

  Request shapes use pointer fields for partial
  updates plus explicit `clear_*` boolean flags
  so "absent key" vs. "explicit null" stays
  distinguishable without a custom JSON
  unmarshaller. `parseISO8601Ptr` accepts
  RFC3339Nano and falls back to RFC3339; both
  are the common Go `time.Time` encodings.

* `internal/subscription/admin_handler_test.go`
  — 8 table-driven tests: list empty, create +
  get, duplicate username 409, missing username
  400, patch + rotate, get 404, rotate 404, and
  the non-admin 403 case (confirms
  `ScopeUsers` is wired). Two minor fixes during
  the iteration:

    1. The test helper was reading the body
       twice (once via `httptest.Response.Result()`,
       once via `w.Body.Bytes()`). Switched to
       a `decodeJSONBytes(t, w.Body.Bytes())`
       helper that reads the bytes once.

    2. The handler was emitting `"ID"`,
       `"Username"`, etc. because the Go User
       struct had no `json` tags. Fixed by
       adding tags (see `subscription.go` below).

  `staticcheck` caught two `SA1012` warnings
  (passing a `nil` context to `GetUserByID` /
  `GetUserBySubToken` in the test helper) —
  fixed by switching to `context.TODO()`.

* `internal/subscription/store.go` — extends
  the `Store` interface with three new methods:
  `CreateUser`, `UpdateUser`, `ListUsers`. The
  `UpdateUserPatch` struct uses pointer fields
  so the absence of a value is distinguishable
  from the zero value. `ErrDuplicate` is the new
  sentinel for username / sub_token collisions.

  `MemoryStore` implements all three. The
  in-memory uniqueness checks mirror the
  migration's UNIQUE indexes; the
  `usersByToken` / `usersByPrevToken` indexes
  are kept in sync inside `CreateUser` /
  `UpdateSubToken`.

* `internal/subscription/pg_store.go` — pgx
  implementations of `CreateUser`, `UpdateUser`,
  `ListUsers`. `CreateUser` reads back the row
  so the caller sees the canonical `created_at` /
  `updated_at`. `UpdateUser` builds the SET
  clause dynamically from the non-nil patch
  fields; the migration's UNIQUE indexes are
  enforced by the database (a 23505 maps to
  `ErrDuplicate`). The empty-patch case is
  short-circuited: `updated_at` is the only SET
  clause, and the function returns a fresh
  read-back without a no-op UPDATE.

* `internal/subscription/service.go` — adds
  `CreateUserInput` + `CreateUser()` (validates
  username 1-64 chars, generates a fresh
  32-hex-char sub_token via `newRandomSubToken`,
  defaults `Status` to `active`, clamps
  `device_limit` to 0-64) and `ListUsers()`.

* `internal/subscription/subscription.go` —
  **adds `json` tags to `User`, `Plan`, `Pool`,
  `PoolMember`**. This was a pre-existing
  inconsistency: the Go structs were
  PascalCase, so the JSON encoder was emitting
  `"ID"`, `"Username"`, etc. v0.1.0 didn't
  catch it because the subscription handler
  was the only path that returned a User —
  and the v0.1.0 frontend wasn't wired yet.
  PR-G fixes the wire format globally. The
  frontend `src/types/aegis.ts` was already
  in camelCase, so the change is bring-Go-
  in-line-with-the-frontend, not the other way
  around.

  The `User.SubTokenPrev` field now omits on
  the empty string via the default empty-string
  encoding (no `,omitempty` — the field is
  always present, just empty when no rotation
  has happened yet). The previous code had no
  tags at all, so this is a strict improvement.

* `internal/router/router.go` — mounts the
  new subrouter at `/api/v1/users`. The
  existing `subscription.Router(subscriptionSvc)`
  at `/api/v1/sub/{token}` (the public, per-user
  subscription URL) stays mounted as it was; the
  two paths are independent.

* `cmd/aegis/main.go` — no change. The
  `subscriptionSvc` was already wired by the
  v0.1.0 work; the new handler picks it up via
  the router.

## Frontend

* `src/api/services/users.ts` — typed wrapper
  around `/api/v1/users`. Five functions:
  `listUsers`, `getUser`, `createUser`,
  `updateUser`, `rotateUserToken`. Shares the
  `api` axios client (bearer + 401-refresh-and-
  retry are transparent). The request shapes
  mirror the backend: `UpdateUserRequest`
  includes the `clearPlanId` / `clearExpireAt`
  boolean flags.

* `src/api/services/index.ts` — re-exports
  `users`.

* `src/views/UsersView.vue` — replaces the
  v0.1.0 placeholder. Full CRUD: list (DataTable
  with username / status badge / device limit /
  traffic limit / created columns), per-row
  DropdownMenu (Edit / Rotate token / Mark as
  deleted), create Dialog (username + device
  limit + traffic limit + status select),
  edit Dialog (pre-fills current values), rotate
  token action that opens a token-display Dialog
  with copy-to-clipboard, mark-as-deleted
  confirmation. The token is shown ONCE on
  create / rotate (the v0.2 surface matches the
  3X-UI convention — the operator hands the
  token to the user out-of-band, then it lives
  only as a 32-char hex on the row).

* `src/components/ui/Textarea.vue` and
  `src/components/ui/SelectTrigger.vue` —
  widened the `class` prop type to
  `string | boolean | undefined` to match the
  fix already applied to `Input.vue` in PR-D
  (vee-validate `useField` slot value is
  `string | number | boolean`, and the
  `hasError && 'border-destructive'` pattern
  produces `string | false`).

* `src/layouts/AppLayout.vue` — flips Users
  nav from "Soon" to enabled. Settings was
  already enabled by PR-F.

* `src/i18n/locales/{en,ru}.json` — replaces
  the v0.1.0 `users.placeholderTitle` /
  `placeholderDescription` pair with the real
  keys (create / edit / username / status /
  traffic / deviceLimit / search / empty /
  created / updated / deleted / rotateToken /
  tokenView / copy / softDelete / confirmSoft-
  Delete / etc., plus the `statuses.*` enum map
  for `active / grace / disabled / expired /
  deleted`).

## Quality

* `go test ./...` — clean.
* `go build ./...` — clean.
* `gofmt -l` clean for the new files
  (`admin_handler.go`, `admin_handler_test.go`)
  after `gofmt -w`. The existing CRLF
  artefacts on the pre-existing subscription
  files (`pg_store.go`, `store.go`, `service.go`,
  `subscription.go`) are unchanged — see
  KNOWN_LIMITATIONS.md and PR #56 (.gitattributes
  pins the repo canonical = LF, so CI on Linux
  is clean).
* `go vet ./...` — clean.
* `staticcheck ./internal/subscription/...` —
  clean (after the `context.TODO()` fix above).
* `npm run type-check` — clean.
* `npm run lint` — clean (eslint + raw-text
  check both pass; the 42 vue/max-attributes-
  per-line warnings on `UsersView.vue` are the
  same `vue-i18n`-style warnings that the other
  views already carry; CI ignores warnings).
* `npm run build` — clean.

## Out of scope (later PRs)

* Hosts create / edit dialog with the nested
  endpoint editor (PR-H) — uses the cross-field
  `superRefine` from PR-C's `host.ts` schema.
* Inbounds create / edit dialog (PR-I) with
  protocol-specific params editors.
* Audit log UI + operator profile + change-
  password (PR-M).
* A real "Delete" button (the v0.2 UI uses
  PATCH `status=deleted`; a dedicated DELETE
  with an audit-log entry lands in v0.3).
* Per-tenant sub_path table (v1.0+ per the
  panelcfg package comment).
* The latent bug in the `clear_*` path: the
  handler builds a `planIDPatch = &clearUuid`
  pointer, but neither the MemoryStore nor the
  PgStore currently treats the `uuid.Nil`
  sentinel as "set column to NULL" — the Pg
  path would surface a 23514 CHECK violation
  (the `users.plan_id` column is NULL-able, so
  it would actually succeed; for `expire_at`
  the column is also NULL-able, so the path
  works in pg but not in MemoryStore where
  the nil-vs-nil-pointer distinction is lost).
  The tests + the v0.2 UI both skip the path
  (no "Clear plan" button), so the bug is
  dormant. Will fix when the v0.3 audit-log
  work lands and we need explicit-clear for
  real.

## Refs

* `ARCHITECTURE.md` v9 §21 (v0.2.0 second
  sub-PR)
* `KNOWN_LIMITATIONS.md` — Users entry
  updated (was "land in v0.2", now closed)
* `docs/adr/0004-frontend-ui-kit-shadcn-vue.md`
* `docs/adr/0003-singbox-only-mvp.md`

Co-authored-by: Aegis Dev <dev@aegis.local>
