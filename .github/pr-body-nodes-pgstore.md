## Summary

Adds the PostgreSQL-backed Store implementation for the `nodes` package, mirroring the same pattern as `auth.PgStore` (PR 24) and `hosts.PgStore` (PR 36). The Phase 0 MemoryStore remains the default; the new pgx-backed implementation is opt-in via `AEGIS_NODES_BACKEND=pg`.

## What's in the box

- `internal/nodes/pg_store.go` — pgx-backed `Store` with Create / GetByID / GetByName / List / Update / Delete. Tag round-trip is via a single `LEFT JOIN node_tags` query, grouped by node id in Go. Create and Update run in a transaction; Update uses the canonical "delete children + insert" pattern for the tag set.
- `internal/nodes/pg_store_integration_test.go` — integration tests under the `integration` build tag. Covers round-trip, duplicate-name rejection, tag replacement, rename collision, cascade-delete, NotFound, and the empty-tag case.
- `migrations/0005_nodes_capacity_hint.sql` — adds a `capacity_hint TEXT NOT NULL DEFAULT ''` column to `nodes`. The Phase 0 Go model carries this field; the schema was missing it.
- `internal/config/config.go` — adds `NodesBackend` (env var `AEGIS_NODES_BACKEND`, default `memory`).
- `cmd/aegis/main.go` — wires the env switch into the boot sequence. When `=pg`, the same pool the auth service opens is reused (migrations already applied at the top of `main`).

## Schema choices

- Tags live in the existing `node_tags` table (migration 0001), not a JSONB column on `nodes`. The relational design was already there; the PgStore just respects it.
- `nodes.name` is `UNIQUE` per migration 0001. The PgStore maps the `23505` SQLSTATE to `ErrDuplicate` for both Create and Update paths.
- The other "DB-only" columns on `nodes` (ssh_port, ssh_user, ssh_key_id, core_kind, core_version, agent_version, inbound_set_id, last_heartbeat_at, last_config_revision, drain, health) stay at their migration 0001 defaults. The Phase 1 slim PgStore intentionally does not touch them — the agent protocol and apply path that will populate them land in a later PR.

## Migration ordering

0005 is a pure `ALTER TABLE … ADD COLUMN` with a default. The Up body is one statement, the Down body mirrors it. No data backfill needed; existing rows get `capacity_hint = ''` automatically.

## Concurrency

- `pgxpool` handles connection pooling.
- Each `Create` and `Update` runs in a transaction so a host row + N tag rows are atomic; a panic in any of the inserts rolls back the whole batch.
- `Delete` is a single statement; the `ON DELETE CASCADE` on `node_tags.node_id` removes the tag rows as a side effect.

## Test strategy

The `pg_store_integration_test.go` file follows the same `//go:build integration` convention as `auth/pg_store_integration_test.go` and `hosts/pg_store_integration_test.go`. CI runs the tagged suite against a service-container Postgres; local `go test ./...` stays fast and dependency-free.

The testutil advisory-lock + cross-package serialisation fixes from PR 36 are reused as-is (they live in `backend/testutil/db.go` and the integration tests get them for free).

## Follow-up

`inbounds.PgStore` (the last Phase 1 store). Same pattern: one `nodes.inbounds` JOIN, `UNIQUE (node_id, name)` + `UNIQUE (node_id, listen_port)` already in migration 0003, JSONB columns for `tags` and `params`.

## Compatibility

- The MemoryStore is untouched — `AEGIS_NODES_BACKEND=memory` is still the default, and dev / unit tests continue to use it.
- The `Store` interface and the `Service` layer are unchanged. Handlers do not need to be edited.

## Checklist

- [x] `go vet ./...` clean
- [x] `golangci-lint` clean for new files
- [x] MemoryStore unit tests still pass
- [x] Integration tests follow the existing pattern
- [x] Migration has a `Down` body
- [x] Env-driven backend switch mirrors `AEGIS_AUTH_BACKEND` / `AEGIS_HOSTS_BACKEND`
