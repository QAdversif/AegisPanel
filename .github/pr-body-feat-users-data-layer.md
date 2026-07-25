# feat(users): end-user data layer for v0.4.0-d

## What this PR does

Adds the `internal/users/` package — the data layer for the
panel's end-user accounts (the people who pay for VPN access and
use the sing-box config). This is **v0.4.0-d.1** of a multi-step
slice that turns the v0.4.0-mvp-batched BatchedApplier from
"infrastructure ready" to "functional end-to-end with real users".

This PR ships:

- The `User` model (mirrors the `users` table from migration
  0001 + 0011)
- The `Store` interface
- Two implementations: `MemoryStore` (in-process; used by
  unit tests and the dev / docker-compose smoke) and
  `PgStore` (Postgres; used by the panel in production)
- The `Service` layer (validation, ID / timestamp / sub_token
  generation, sub_token rotation per migration 0011)
- 22 unit tests + 1 integration test (build tag `integration`)

Out of scope (deferred to d.2, d.3, d.4, d.5):

- d.2: REST API for users (POST/GET/PATCH/DELETE /v1/users)
- d.3: `CoreConfig.Users` + `singbox.RenderConfig` injection
- d.4: User CRUD → Delta → per-node BatchedApplier
- d.5: FlushFn real work (re-render + Apply)

## Files

- `backend/internal/users/user.go` (new, 165 lines) — User
  struct, `Status` enum (active/grace/disabled/expired/deleted),
  `IsValid()` pre-flight check, `String()` debug helper that
  redacts the sub_token.
- `backend/internal/users/store.go` (new, 110 lines) — Store
  interface (Create / Get / GetByID / GetByUsername /
  GetBySubToken / List / ListByStatus / Update / Delete),
  `ErrNotFound` / `ErrDuplicate` / `ErrInvalid` sentinels,
  `*ValidationError` per-field error type.
- `backend/internal/users/memory_store.go` (new, 280 lines) —
  In-memory store with O(1) lookups via three indexes (byID,
  byUser, byToken) plus the byPrevToken index for the
  migration-0011 sub_token rotation chain. All indexes kept in
  sync on Create / Update / Delete.
- `backend/internal/users/pg_store.go` (new, 320 lines) —
  Postgres implementation using pgxpool. Mirrors the
  MemoryStore interface one-to-one. Maps Postgres SQLSTATE
  23505 (unique_violation) to `ErrDuplicate`. The prev-token
  lookup uses a `WHERE sub_token_prev IS NOT NULL AND
  sub_token_prev_expires_at > NOW()` clause (per migration 0011
  semantics).
- `backend/internal/users/service.go` (new, 415 lines) — The
  business-logic layer. Validation (username format, status
  enum, traffic limit non-negative, telegram ID range, email
  format, hosts allow/block list shape with no duplicates);
  ID / timestamp / sub_token generation on Create; sub_token
  rotation via `RotateSubToken` (parks old token in
  SubTokenPrev with configurable grace window, default 24h).
- `backend/internal/users/user_test.go` (new) — User struct
  and Status enum tests (3 cases).
- `backend/internal/users/memory_store_test.go` (new) — 7
  MemoryStore tests covering CRUD, duplicate detection, the
  prev-token chain (active / expired / usePrev=false), and
  rename collision.
- `backend/internal/users/service_test.go` (new) — Service
  tests: happy path, 14 validation failure subtests, the
  pointer-fields Update semantics, the rename-collision path,
  and the sub_token rotation chain.
- `backend/internal/users/pg_store_integration_test.go` (new,
  build tag `integration`) — Round-trip test for the pgx
  implementation: Create → Get → Update → RotateSubToken →
  List → ListByStatus → Delete, with the prev-token chain
  verified at every step. The fixture takes an advisory
  lock + truncates the `users` table on entry/exit so
  concurrent test runs against the same DB do not collide.

## Notable decisions

### Why both a Store interface and a Service

Mirrors the inbounds / nodes / bootstrap pattern. The Store is
the persistence boundary; the Service owns validation +
business rules. Tests can swap the Store for the MemoryStore
without touching the Service, and the production code wires
the Service with the PgStore. The interface is narrow on
purpose: 9 methods, each one corresponds to a single
SQL query or a single in-memory map operation.

### Why a separate `users` package (not in `internal/auth`)

The `internal/auth/` package is for **panel admins** — the
operators who run the panel, log in via the JWT flow, and
manage end-users via the admin UI. The `internal/users/`
package is for **end-users** — the people who pay for access
and use the sing-box config. They have nothing in common at
the data-model level. The auth User has Username / Email /
PasswordHash / Role / Scopes; the end-user User has Username /
Email / Status / PlanID / TrafficLimit / DeviceLimit /
SubToken. The two packages must not be confused.

### Why a 64-hex-char sub_token

32 random bytes from `crypto/rand` (Linux: getrandom(2)),
encoded as 64 hex characters. Matches the existing
`internal/bootstrap/secrets.go` convention (32-byte bearer
secrets). 128 bits is well above the NIST SP 800-63B
"Remembered Secret" entropy floor; rotation is the
mitigation for an on-path attacker who already has the
token.

### Why a 24h prev-token grace window

The default in `RotateSubToken` matches the migration 0011
"previous token keeps working for 24h after rotation" pattern
used by 3X-UI / X-UI. The end-user has time to re-import the
new URL on every device before the old URL 404s. The grace
window is configurable per call (the operator's cabinet UI
might want a longer window for trusted users).

### Why Service creates the sub_token (not the operator)

The sub_token is the secret credential the subscription
package looks the user up by. If the operator supplied it
(via the CreateInput), they'd need a way to generate a fresh
32-byte secret — a footgun. The Service generates it from
`crypto/rand` and the operator never sees it (the API
response includes it exactly once, in the Create response;
subsequent reads do NOT include it).

## Lint clean

`golangci-lint v2` with `backend/.golangci.yml -tags=integration`
reports 0 issues for `./internal/users/...`. The fixes for the
v2-only lints follow the same pattern as the v0.4.0-a/b/c PRs:

- `gofmt` (alignment of struct field tags) — fixed by `go fmt`
- `gocritic typeUnparen` — `(Store)` simplified to `Store`

No `gosec` warnings; no `revive var-naming` issues (the
`byToken` / `byPrevToken` initialisms are spelled lowercase
on purpose — the package's exported `ByX` accessors would
trigger the rule, but the unexported indexes are correct as-is
per the Go community convention for unexported identifiers).

## Test results

Local:

- `go test ./internal/users/...` — all 22 unit tests pass
  (5 test groups, 1.0s)
- `go test -tags=integration ./internal/users/...` — all 22
  unit tests + the integration test pass (when
  INTEGRATION_DATABASE_URL is set; the integration test
  is a no-op otherwise, per the build tag convention)
- `go test ./...` — all 17 packages pass (no regressions
  in inbounds, nodes, cores, auth, etc.)
- `golangci-lint run --config .golangci.yml -tags=integration
  ./internal/users/...` — 0 issues

CI: the existing `backend` job already runs
`golangci-lint v2` and `go test -tags=integration`. No CI
configuration change needed.

## What this PR does NOT do

- No REST handlers (d.2)
- No router wiring (d.2)
- No admin middleware integration (d.2)
- No CoreConfig / singbox.RenderConfig user injection (d.3)
- No event emission / BatchedApplier wiring (d.4)
- No FlushFn real work (d.5)
- No end-to-end smoke test (d.6)
- No Telegram / Cabinet integration (out of scope; Phase 1.1)

## Refs

- ARCHITECTURE.md §7.5 (Apply pipeline)
- v0.4.0-mvp-batched (BatchedApplier + real Apply transport)
  #92, #93, #94
- Migration 0001_initial.sql (users table) + 0011
  (sub_token rotation)
- `internal/auth/` (panel admins — pre-existing, separate
  concern)
- `internal/inbounds/` (template for Store / Service / pg
  store pattern)
