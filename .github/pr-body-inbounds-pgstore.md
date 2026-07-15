## Summary

Adds the PostgreSQL-backed Store implementation for the `inbounds` package, closing out the Phase 1 store work (auth in PR 24, hosts in PR 36, nodes in PR 37, **inbounds here**). The Phase 0 MemoryStore remains the default; the new pgx-backed implementation is opt-in via `AEGIS_INBOUNDS_BACKEND=pg`. With this PR the panel's full set of CRUD services can be backed by a single Postgres instance in production.

## What's in the box

- `internal/inbounds/pg_store.go` — pgx-backed `Store` with `Create` / `GetByID` / `GetByNodeAndName` / `GetByNodeAndPort` / `ListByNode` / `ListByProtocol` / `Update` / `Delete`. The two JSONB columns (`tags`, `params`) are marshaled via a small inline `mustMarshal` / `mustMarshalOrNil` pair so the typed-nil-vs-nullable distinction is preserved through the round-trip.
- `internal/inbounds/pg_store_integration_test.go` — 18 integration tests under the `integration` build tag. Cover round-trip, `(node_id, name)` and `(node_id, listen_port)` UNIQUE rejection, `nil` params round-trip, empty-tags round-trip, unknown-protocol CHECK rejection, all four get paths (incl. `NotFound`), `ListByNode` sorted by `listen_port`, `ListByProtocol` filter, `Update` field replacement plus name- and port-collision paths, and `Delete` (incl. `NotFound`).
- `internal/config/config.go` — adds `InboundsBackend` (env var `AEGIS_INBOUNDS_BACKEND`, default `memory`).
- `cmd/aegis/main.go` — wires the env switch into the boot sequence. When `=pg`, a fresh `pgxpool` is opened (migrations are applied at the top of `main`, so the `inbounds` table already exists by the time the store is constructed).

## Schema choices

- `tags` is a non-nullable `JSONB` array, `params` is a nullable `JSONB` object. The two columns are marshaled differently so the typed-nil vs SQL `NULL` distinction survives a round-trip: a `*Balancer` field set to `nil` on the Go side becomes SQL `NULL` in the column (not `null` JSON), and reads back as a `nil` map on the Go side. A `*Balancer` set to a real value is JSON-marshaled as the object and unmarshaled back into a non-nil map.
- The `protocol` and `listen_port` CHECK constraints in migration 0003 match the Go model allow-list and port range 1..65535; the integration test for `wireguard` (an unknown protocol) confirms the DB rejects it even if the Service is bypassed.
- The `(node_id, name)` and `(node_id, listen_port)` UNIQUE constraints are mapped to `ErrDuplicate` for both Create and Update paths, with a context message that names the offending node, name, and port.
- No new migrations are added — the schema in migration 0003 is already what `PgStore` needs.

## Concurrency

- `pgxpool` handles connection pooling.
- `Create` and `Update` are single-statement operations (no children to atomically insert), so no explicit transaction is needed.
- `Delete` is a single statement; the `ON DELETE CASCADE` on `inbounds.node_id` removes inbounds as a side effect of node deletion (the node store owns that path).

## JSONB + reflect notes

The `mustMarshalOrNil` helper does a `reflect.ValueOf(v).Kind()` check for the standard nilable kinds (Pointer, Interface, Slice, Map, Chan, Func) and returns SQL `NULL` when the value is a typed nil. The kind list mirrors what `encoding/json` itself treats as nil; the inline-check analyzer (`go vet -vettool=inline`) accepts `reflect.Pointer` (the canonical Go 1.26+ name) and would flag `reflect.Ptr` as a `//go:fix inline` candidate. The file uses `reflect.Pointer` throughout.

## Test strategy

The `pg_store_integration_test.go` file follows the same `//go:build integration` convention as `auth/pg_store_integration_test.go`, `hosts/pg_store_integration_test.go`, and `nodes/pg_store_integration_test.go`. CI runs the tagged suite against a service-container Postgres; local `go test ./...` stays fast and dependency-free.

The testutil advisory-lock + cross-package serialisation fixes from PR 36 are reused as-is (they live in `backend/testutil/db.go` and the integration tests get them for free). The cross-package `seedNode` helper inside this file uses `state='new'` per the Go model lifecycle (migration 0006 aligned `nodes_state_check` with the Go model allow-list).

## Follow-up

Phase 1 stores are done. The natural next step is a shared `pgxpool.Pool` refactor — today each `AEGIS_*_BACKEND=pg` block opens its own pool in `main.go`; a single `db.Open(cfg)` factory would simplify boot and make connection-limit tuning central. Subscription work, the agent protocol, and the sing-box apply path remain the larger Phase 2 items.

## Compatibility

- The MemoryStore is untouched — `AEGIS_INBOUNDS_BACKEND=memory` is still the default, and dev / unit tests continue to use it.
- The `Store` interface and the `Service` layer are unchanged. Handlers do not need to be edited.

## Checklist

- [x] `go vet ./...` clean
- [x] `go vet -tags=integration ./...` clean
- [x] `golangci-lint` clean for new files
- [x] MemoryStore unit tests still pass
- [x] Integration tests follow the existing pattern
- [x] No new migrations (schema already correct)
- [x] Env-driven backend switch mirrors `AEGIS_AUTH_BACKEND` / `AEGIS_HOSTS_BACKEND` / `AEGIS_NODES_BACKEND`
