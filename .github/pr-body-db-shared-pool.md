## Summary

Adds a single `internal/db` package that owns the PostgreSQL connection pool, and rewires `cmd/aegis/main.go` to use it. Before this PR the same `pgxpool.New(ctx, cfg.PostgresDSN)` block was copy-pasted four times — once per `AEGIS_*_BACKEND=pg` switch (auth, hosts, nodes, inbounds) — and a fifth time in the `runMigrate` subcommand. All five blocks used the same DSN, the same defaults, the same error handling. After the refactor, every consumer gets a single shared pool (lazy-opened only when at least one service asks for `pg`), and there is exactly one place that knows how to talk to PostgreSQL.

This is the "shared pool" follow-up that closed out Phase 1 stores (PRs 24, 36, 37, 38) — see the follow-up note in PR 38's body.

## What's in the box

- `internal/db/db.go` — a single exported function `Open(ctx, dsn) (*pgxpool.Pool, error)`. Parses the DSN via `pgxpool.ParseConfig` (so operator-supplied `pool_max_conns`, `pool_min_conns`, `pool_max_conn_lifetime`, `pool_health_check_period`, etc. are honoured as-is), opens the pool with `NewWithConfig`, then pings the server. On any error the partially-opened pool is closed before returning, so the caller never has to clean up after a failure.
- `internal/db/db_test.go` — two unit tests. `TestOpen_BadDSN` rejects an unparseable DSN with a wrapped error. `TestOpen_UnreachableHost` proves the post-`New` `Ping` catches a syntactically valid but unreachable DSN before the caller thinks the pool is ready (port 1 is reserved and refuses fast).
- `cmd/aegis/main.go` — `pgxpool.New` is gone from this file. One `db.Open` call (only when `needsPg` is true, computed from the four `*Backend` env vars), one `defer pool.Close()`, one `migrations.Up`. Each `case "pg":` switch arm now reads `xxxStore = xxx.NewPgStore(pool)` against the shared pool; the four near-identical error/log blocks are gone. The `runMigrate` subcommand also routes through `db.Open`.

## Design notes

- **Lazy open.** `needsPg` is `OR` of the four `AEGIS_*_BACKEND` flags. When everything is `memory` (the Phase 0 default for dev / unit tests), no pool is opened, no connection is held, and `AEGIS_POSTGRES_DSN` is still required by the config validator but never touched. The lazy path was a deliberate choice over "always open" — opening a pool on every dev run wastes a Postgres connection for no benefit.
- **DSN-driven pool config.** pgxpool already applies reasonable defaults (max 4 conns, 1h lifetime, 30m idle, 1m health check period). The operator can override any of these via the standard DSN `pool_*` parameters. We do not apply our own defaults on top — overriding them silently would surprise the operator and `ParseConfig` already does the right thing.
- **Ping after `NewWithConfig`.** A misconfigured DSN (wrong host, wrong password, wrong dbname) would otherwise hand the caller a "ready" pool that only fails on the first query. By pinging once during `Open` we move that failure to boot time, where it belongs. The `TestOpen_UnreachableHost` test guards against a future regression that removes this check.
- **No `internal/config` import inside `internal/db`.** `db.Open` takes a `dsn string`, not a `*config.Config`. This keeps the package importable from `runMigrate` (which reads the DSN from `os.Getenv` directly so a migrations run can recover a broken install without a valid full config) and from any future caller that does not have a `Config` handy.
- **No new migrations.** This PR is a pure refactor; the schema is unchanged.

## Test strategy

- `internal/db/db_test.go` runs as a plain unit test (no `//go:build integration` tag) so it is part of `go test ./...`. The two cases are chosen to fail without needing a live Postgres.
- The per-store integration tests (`internal/{auth,hosts,nodes,inbounds}/pg_store_integration_test.go`) already exercise the full `db.Open` → `migrations.Up` → store lifecycle end-to-end through `testutil.MustNewPool`. They continue to pass.
- The `runMigrate` subcommand is exercised manually in the merge-deployment environment; an automated integration test for it is not in scope here.

## Compatibility

- Boot semantics are unchanged: a service configured for `pg` still gets a `*pgxpool.Pool`; a service configured for `memory` still gets a `MemoryStore`. The order of `log.Info` lines is slightly different — the new code logs "db: opened pool" (implicit via the per-store `using pgx-backed store` line) before any per-store message; previously the per-store log was a `log.Fatal` path that didn't have a success log. The structured fields are the same.
- The `AEGIS_POSTGRES_DSN` env var is unchanged. The `pool_*` DSN parameters are unchanged.
- The `testutil.MustNewPool` helper is unchanged. It still does its own `pgxpool.New` + `pg_advisory_lock` + drop+create dance for test isolation, and does not use `db.Open`. A future PR could rewire it to share `db.ParseConfig` (one line) but a `db` failure should not break the integration suite today.

## Follow-up

- Configurable pool sizing via env vars (today the operator must encode `pool_max_conns` etc. in the DSN — fine for prod, awkward for dev).
- A `db.HealthCheck(ctx)` helper that pings on demand and feeds into `/healthz`.
- Pool metrics (active conns, idle conns, wait count) on the existing obs package.
- Re-routing `testutil.MustNewPool` through `db.ParseConfig` once we trust the abstraction.

## Checklist

- [x] `go vet -tags=integration ./...` clean
- [x] `go vet -tags=integration -vettool=inline ./...` clean
- [x] `gofmt -l` clean for new files
- [x] `go test ./...` passes (including new `internal/db` tests)
- [x] `go build -tags=integration ./...` clean
- [x] No new migrations
- [x] No new env vars
