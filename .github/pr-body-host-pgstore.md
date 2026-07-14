# feat(backend): HostPgStore (pgx-backed host persistence)

Adds the PostgreSQL-backed `Store` implementation for the
`hosts` package. Closes the v3 host model realisation on
the persistence side: the `hosts` and `host_endpoints`
tables (added in PR #35) are now backed by a real
`Store`.

## What it does

- **`pg_store.go`** — `PgStore` implements the
  `Store` interface. JSONB columns (`status_filter`,
  `tags`, `address`, `sni`, `host`, nullable `balancer`)
  are marshaled inline. The nullable `host_endpoints.port`
  is bound as a `*int` for pgx.
- **Read path** — every read uses a single
  `LEFT JOIN host_endpoints` query and groups by
  `host_id` in Go. 100 hosts × 3 endpoints = 1 query, not
  N+1.
- **Write path** — `Create` and `Update` run inside a
  pgx transaction. `Update` uses the canonical
  "DELETE children + INSERT" pattern for the endpoint
  set so the persisted state is exactly what the
  service validated (no partial updates).
- **`Delete`** is a single `DELETE FROM hosts`. The
  `host_endpoints` rows are removed by the
  `ON DELETE CASCADE` foreign key added in
  migration 0004.
- **Backend switch** — `AEGIS_HOSTS_BACKEND=pg|memory`
  in `internal/config`. `main.go` opens a separate
  `pgxpool` for the hosts store (the auth service
  already has its own; the shared pool is a future
  refactor).
- **Integration tests** —
  `pg_store_integration_test.go` (`//go:build integration`)
  with the same CI pattern as `auth.PgStore`. Each test
  seeds two nodes + two inbounds + a single host and
  exercises one method. Tests skip (not fail) when
  `INTEGRATION_DATABASE_URL` is unset.

## Why a single JOIN

The naive approach is 1 query for the host + 1 query
per host for the endpoints (N+1). For the dev panel
(~100 hosts) this is fine, but the JOIN is not much
more code and is the obvious read pattern. The single
query returns up to one row per endpoint; the Go side
groups them by `host_id`. A host with no endpoints
yields one row with `NULL` endpoint columns; the
scanner detects the NULL and skips the endpoint
append.

## JSONB columns

Five columns are JSONB. Each is marshaled with
`encoding/json` at the write site and unmarshaled into
the Go struct field at the read site:

- `hosts.status_filter` ↔ `[]UserStatus`
- `hosts.tags` ↔ `[]string`
- `hosts.balancer` (nullable) ↔ `*Balancer`
- `host_endpoints.address` ↔ `[]string`
- `host_endpoints.sni` ↔ `[]string`
- `host_endpoints.host` ↔ `[]string`

`host_endpoints.port` is the only nullable scalar; it
binds as `*int`, which pgx v5 maps to `NULL` when nil.
`host_endpoints.path` is `TEXT` (required, never NULL).

## Files

| File | Purpose |
|---|---|
| `internal/hosts/pg_store.go` | `PgStore`, the JOIN query, the scan helper, JSONB marshaling |
| `internal/hosts/pg_store_integration_test.go` | `//go:build integration`; CI runs with `services: postgres` |
| `internal/config/config.go` | new `AEGIS_HOSTS_BACKEND` env var |
| `cmd/aegis/main.go` | switch on `cfg.HostsBackend`, open pool, log the choice |

## Out of scope (next PRs)

- **`NodesPgStore`** and **`InboundsPgStore`** — same
  pattern. The MemoryStore on those is fine for dev;
  the pgx-backed Store lands with the broader Phase 1
  pg migration.
- **Shared `pgxpool.Pool`** — today each Service opens
  its own pool. A `db.Open(cfg)` factory that returns a
  shared pool is a 30-line refactor; deferred to the
  Phase 1 pg milestone so all three Stores land in a
  single PR.
- **Connection pool tuning** (`max_conns`,
  `min_conns`, etc.) — also a Phase 1 pg concern.

## Testing

```
go test ./internal/hosts/...
# ok  github.com/QAdversif/AegisPanel/internal/hosts

INTEGRATION_DATABASE_URL=postgres://... \
  go test -tags=integration ./internal/hosts/...
# ok  github.com/QAdversif/AegisPanel/internal/hosts
```

The MemoryStore tests still pass unchanged. The
integration tests are gated by the `integration` build
tag and skip cleanly when `INTEGRATION_DATABASE_URL`
is empty (matching the `auth.PgStore` pattern).
