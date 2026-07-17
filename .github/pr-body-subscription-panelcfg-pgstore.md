# feat(backend): SubscriptionPgStore + PanelcfgPgStore (v0.1.0 backend)

## Summary

Adds the two remaining `*PgStore` implementations needed to land the
`v0.1.0-mvp-render` milestone. With this PR, the panel can run with
**every persistence layer on Postgres** (auth, hosts, inbounds, nodes,
subscription, panelcfg). MemoryStore remains the default for dev;
`AEGIS_*_BACKEND=pg` env switch activates the new PgStores.

## Changes

### New stores

- `backend/internal/panelcfg/pg_store.go` — `PanelcfgPgStore` (1 table:
  `panel_path_config`, migration 0010). Implements all 4 Store methods:
  `GetActive`, `GetByID`, `SetActive`, `Reset`. SetActive and Reset are
  atomic (single transaction: deactivate-all + insert-new). Grace
  window handled via `clock_timestamp() + $2::INTERVAL`.
- `backend/internal/subscription/pg_store.go` — `SubscriptionPgStore`
  (5 tables: `users`, `plans`, `host_pools`, `host_pool_members`,
  `plan_pool`, migrations 0001 + 0011). Implements all 7 Store methods:
  `GetUserBySubToken`, `GetUserByPrevSubToken`, `GetUserByID`,
  `UpdateSubToken`, `ListPoolsForUser`, `ListPoolsAll`,
  `ListPoolMembers`. UpdateSubToken is a single statement
  (atomic at SQL level): moves primary to prev, sets
  `sub_token_prev_expires_at`, bumps `sub_token_rotated_at` and
  `updated_at` to `clock_timestamp()`.

### Integration tests

- `backend/internal/panelcfg/pg_store_integration_test.go` — 11 tests
  under `//go:build integration`:
  - GetActive default row seeded
  - GetActive when no active row → ErrNotFound
  - GetByID sentinel and unknown
  - SetActive inserts new active row
  - SetActive rejects invalid path (empty, too short, invalid char)
  - SetActive grace window: old row gets `expires_at = now + grace`
  - SetActive no grace: old row gets `expires_at = NULL`
  - SetActive "at most one active row" invariant after N rotations
  - SetActive rejects duplicate path (UNIQUE constraint)
  - Reset reactivates default row
  - Reset "only one active" after alternating SetActive/Reset
- `backend/internal/subscription/pg_store_integration_test.go` — 13
  tests under `//go:build integration`:
  - GetUserBySubToken found / unknown
  - GetUserByPrevSubToken found / unknown
  - GetUserByID found / unknown
  - UpdateSubToken moves primary to prev (full assertion: new
    primary, old primary, prev with grace, rotated_at bumped,
    updated_at bumped)
  - UpdateSubToken not found
  - UpdateSubToken drops earlier primary (old primary no longer
    resolves via the primary lookup)
  - ListPoolsForUser no plan → empty
  - ListPoolsForUser plan not linked → empty
  - ListPoolsForUser plan linked → returns sorted pools, unlinked
    pool excluded
  - ListPoolsAll empty DB → empty (non-nil) slice
  - ListPoolsAll all seeded pools returned sorted
  - ListPoolMembers empty pool → empty slice
  - ListPoolMembers all members returned sorted by host_id, weights
    round-trip
  - ListPoolMembers other-pool members do not leak

### Config

- `backend/internal/config/config.go` — adds `SubscriptionBackend`
  (`AEGIS_SUBSCRIPTION_BACKEND`) and `PanelcfgBackend`
  (`AEGIS_PANELCFG_BACKEND`) env switches. Default `memory` to match
  the existing `AEGIS_*_BACKEND` pattern.

### Wiring

- `backend/cmd/aegis/main.go` — adds the two new env switches to the
  `needsPg` OR, picks the store implementation per service, logs
  the choice.

## Migrations

**No new migrations.** The schema is already complete:
- migration 0001: `users`, `plans`, `host_pools`,
  `host_pool_members`, `plan_pool`
- migration 0010: `panel_path_config`
- migration 0011: `users.sub_token_prev`,
  `users.sub_token_prev_expires_at` + partial UNIQUE index

## Schema mapping notes

- `users` is fully modelled by the Go `User` struct except for 4
  fields: `external_id`, `last_reset_at`, `telegram_id`, `email`.
  The Store reads them but discards them; a future model change
  adds the corresponding Go fields without a Store change.
- `users.plan_id` has NO FK constraint in migration 0001 (the
  relationship is documented but the FK was deferred). The Store
  treats the column as a free-floating UUID. This matches the
  MemoryStore behaviour.
- `users.hosts_allowlist` / `hosts_blocklist` are JSONB arrays of
  UUIDs. NULL round-trips to a nil slice; `'[]'::JSONB` round-trips
  to a non-nil empty slice.
- `plans.duration` is `INTERVAL` in SQL, `time.Duration` in Go. pgx
  v5 handles the conversion natively.
- `host_pools.strategy` and `users.status` have CHECK constraints;
  the Store does not enforce them (the Service layer does, on the
  way in).

## Atomicity / concurrency

- `PanelcfgPgStore.SetActive` and `Reset`: explicit transaction,
  `deferred Rollback` so a panic rolls back. Two statements
  (deactivate + insert) commit atomically.
- `SubscriptionPgStore.UpdateSubToken`: single UPDATE statement,
  atomic at SQL level. The partial UNIQUE index on
  `users.sub_token_prev` (migration 0011) surfaces a
  double-rotation-with-collision as a 23505 SQLSTATE, mapped to
  `ErrNotFound` (the operator-facing error for "rotation impossible
  on this state").
- All reads: pgxpool acquires a connection per call. No shared state
  in the store; safe for concurrent use.

## Memory parity

The PgStore is a behaviour-equivalent drop-in for the MemoryStore:

| Method | MemoryStore | PgStore |
| --- | --- | --- |
| `GetUserBySubToken` | O(1) map lookup | UNIQUE index hit |
| `GetUserByPrevSubToken` | O(1) map lookup | partial UNIQUE index hit |
| `GetUserByID` | O(1) map lookup | PRIMARY KEY hit |
| `UpdateSubToken` | in-memory mutation + index update | single UPDATE |
| `ListPoolsForUser` | dev shortcut (any pool with members) | actual `plan_pool` join |
| `ListPoolsAll` | every pool | every pool |
| `ListPoolMembers` | in-memory slice | `host_pool_members` query |
| `GetActive` (panelcfg) | in-memory map | index hit on `is_active` |
| `GetByID` (panelcfg) | in-memory map | PRIMARY KEY hit |
| `SetActive` (panelcfg) | in-memory mutation | transaction |
| `Reset` (panelcfg) | in-memory mutation | transaction (upsert) |

**Known semantic difference:** `ListPoolsForUser`. The MemoryStore
has a documented Phase 0 shortcut ("every pool that has at least one
member is considered attached to every plan"). The PgStore uses
the actual `plan_pool` join — the production-correct path. The
shortcut is dev-only; the existing Service tests that depend on it
use the MemoryStore.

## CI

- `make test-integration` (or `INTEGRATION_DATABASE_URL=... go
  test -tags=integration ./...`) runs all 24 new tests against the
  service-container Postgres.
- The CI docs/frontend/trivy/govulncheck/containers jobs are
  unchanged.
- The CI backend job runs `golangci-lint` + `sqlfluff` (v2) + unit
  tests. No SQL changes in this PR, so `sqlfluff` is a no-op
  (the existing migrations still pass).

## Out of scope (deferred)

- The `ListPoolsForUser` shortcut in the MemoryStore is NOT
  removed. It is a dev convenience; tests still use it. A
  follow-up PR can either:
  - (a) Update the MemoryStore to use a `planPool` map (matching
    the real schema), or
  - (b) Drop the shortcut entirely and add per-test `plan_pool`
    seeding.
  Either way it is a MemoryStore-only change.
- A `*PgStore`-backed `With*` dev seed path is NOT added. The
  integration tests seed via raw SQL; the dev path continues to
  use the MemoryStore's `With*` helpers. The `cmd/aegis/main.go`
  dev seed (if any) is unaffected.

## Next steps (per ADR-0003 / §21)

- **#51 PanelcfgPgStore** — done in this PR.
- **#52** Node Profile validator (per ADR-0002) — next.
- **#53+** Frontend: shadcn-vue init + components + DataTable +
  CRUD pages (per ADR-0004), in 5 sub-PRs.

## Files changed

- `backend/cmd/aegis/main.go` (+33 / -7)
- `backend/internal/config/config.go` (+18)
- `backend/internal/panelcfg/pg_store.go` (NEW, +280)
- `backend/internal/panelcfg/pg_store_integration_test.go` (NEW, +322)
- `backend/internal/subscription/pg_store.go` (NEW, +387)
- `backend/internal/subscription/pg_store_integration_test.go` (NEW, +530)

## References

- ADR-0003 (v9): MVP v1.0 ships on sing-box; CoreProvider
  abstraction preserves second-core option.
- ADR-0004: shadcn-vue UI stack.
- ARCHITECTURE.md §21 Phase 1 (MVP-0.x).
- Migration 0001 (users / plans / host_pools / host_pool_members
  / plan_pool).
- Migration 0010 (panel_path_config).
- Migration 0011 (sub_token_prev + partial UNIQUE index).
