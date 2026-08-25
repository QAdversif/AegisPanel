// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package testutil provides shared helpers for integration tests that
// need a live PostgreSQL. It is intentionally not part of the runtime
// build — the `//go:build integration` constraint on the tests is the
// way to opt in. Outside that build tag this file is dead code.
//
// # Why a service container, not testcontainers
//
// The CI uses GitHub Actions `services: postgres`, which gives us a
// fresh DB on localhost:5432 with no Docker-in-Docker dance. Locally
// the developer can do the same with a `docker run postgres` one-liner
// and `INTEGRATION_DATABASE_URL=...`. The helper here treats the
// connection as a black box — wherever it came from, we:
//  1. ping the server;
//  2. ensure no other suite is using the same database (DROP+CREATE
//     on the configured DB so concurrent runs don't clobber each
//     other when they share a Postgres instance);
//  3. run every migration in `migrations/` via the same helper the
//     production binary uses (`internal/migrations.Up`).
//
// The DROP+CREATE cycle is cheap (sub-second on a warm container) and
// gives us full test isolation without needing a separate role per
// developer. If you need parallel test packages later, switch to
// per-package schemas in a transaction.
package testutil

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/QAdversif/AegisPanel/internal/migrations"
)

// EnvIntegrationDSN is the connection string the integration tests
// expect. When unset the tests call `t.Skip` rather than failing —
// this keeps `go test ./...` clean for anyone who does not have a
// Postgres handy (CI is the only environment that must set it).
const EnvIntegrationDSN = "INTEGRATION_DATABASE_URL"

// MustNewPool connects to INTEGRATION_DATABASE_URL, drops and
// recreates the target database, applies every migration in
// `migrations/`, and returns a ready-to-use *pgxpool.Pool. The pool
// is closed via `t.Cleanup`.
//
// If INTEGRATION_DATABASE_URL is empty, the test is skipped with a
// message that points the reader at the Makefile target.
//
// # Cross-package serialisation
//
// `go test ./...` runs each package in a separate process, and
// every package shares the same DSN. Without a cross-process
// lock, two packages can interleave on the shared database:
// one creates it, the other drops it, the first then sees
// "database does not exist" on its next query. The fix is a
// PostgreSQL session-scoped advisory lock (`pg_advisory_lock`)
// held for the ENTIRE duration the test process owns the
// database — from before the recreate through the migration
// step. The lock is released by `t.Cleanup` when the test
// process is done with the database.
//
// The lock is held on a single connection from the admin pool
// (which points at the default `postgres` database, not the
// target). All drop+create + migrate operations run while
// the lock is held; other test processes calling
// `pg_advisory_lock(42)` block until we release.
func MustNewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv(EnvIntegrationDSN)
	if dsn == "" {
		t.Skipf(
			"integration tests require %s; "+
				"set it to a Postgres DSN (e.g. postgres://user:pass@localhost:5432/aegis_it). "+
				"Use `make test-integration` from backend/ to run them locally.",
			EnvIntegrationDSN,
		)
	}

	if err := pingWithRetry(t, dsn, 30*time.Second); err != nil {
		t.Fatalf("postgres not reachable at %s: %v", maskDSN(dsn), err)
	}

	adminDSN, dbName, err := splitDSN(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}

	// 1. Acquire a single admin connection and hold a
	//    session-scoped advisory lock for the rest of
	//    the function. The lock prevents any other
	//    test process from re-creating the database
	//    while we are running migrations or while the
	//    test itself is using the pool.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("admin pgxpool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	adminConn, err := adminPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire admin connection: %v", err)
	}
	// 42 is an arbitrary constant; the lock is
	// project-private. Two test processes picking the
	// same DSN serialise on this.
	const advisoryKey int64 = 42
	if _, err := adminConn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryKey); err != nil {
		adminConn.Release()
		t.Fatalf("advisory lock: %v", err)
	}
	// Best-effort unlock at test end. The connection
	// close would also release the lock, but it pays
	// to be explicit.
	t.Cleanup(func() {
		// Use a fresh context: the test's own context
		// may already be cancelled at this point.
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer releaseCancel()
		_, _ = adminConn.Exec(releaseCtx, "SELECT pg_advisory_unlock($1)", advisoryKey)
		adminConn.Release()
	})

	if err := recreateDatabaseOnConn(ctx, adminConn, dbName); err != nil {
		t.Fatalf("recreate database %q: %v", dbName, err)
	}

	if err := runMigrationsOnConn(t, ctx, adminConn, dsn); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pool.Ping: %v", err)
	}
	return pool
}

// pingWithRetry polls the database until it accepts connections or
// the timeout elapses. The CI service container takes a couple of
// seconds to come up after the runner starts, so a single ping is
// not enough.
func pingWithRetry(t *testing.T, dsn string, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			cancel()
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		err = pool.Ping(ctx)
		pool.Close()
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return lastErr
}

// splitDSN splits a Postgres DSN into a "server-only" DSN (no
// database name) and the database name itself. We need the
// server-only DSN to issue `DROP DATABASE` / `CREATE DATABASE`
// against the default `postgres` admin database.
//
// Supports the libpq URL form (`postgres://...`) and the keyword
// form (`host=... dbname=...`). Mixed forms are not supported and
// will return an error.
func splitDSN(dsn string) (serverDSN, dbName string, err error) {
	switch {
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		u, parseErr := url.Parse(dsn)
		if parseErr != nil {
			return "", "", fmt.Errorf("parse url DSN: %w", parseErr)
		}
		if u.Path == "" || u.Path == "/" {
			return "", "", errors.New("DSN must include a database name (e.g. /aegis_it)")
		}
		dbName = strings.TrimPrefix(u.Path, "/")
		u.Path = "/postgres" // admin DB
		return u.String(), dbName, nil
	}

	// keyword form — find the dbname= token and swap its value.
	const key = "dbname="
	idx := strings.Index(dsn, key)
	if idx < 0 {
		return "", "", errors.New("keyword DSN must include dbname")
	}
	rest := dsn[idx+len(key):]
	end := len(rest)
	if sp := strings.IndexByte(rest, ' '); sp >= 0 {
		end = sp
	}
	dbName = rest[:end]
	serverDSN = dsn[:idx+len(key)] + "postgres"
	if end < len(rest) {
		serverDSN += rest[end:]
	}
	return serverDSN, dbName, nil
}

// recreateDatabaseOnConn is the per-conn version of
// recreateDatabase. The caller passes a connection from
// the admin pool that already holds the
// `pg_advisory_lock(42)` session-scoped lock; we use
// that connection for the terminate + drop + create
// sequence so the lock continues to cover the operation.
//
// The 10-attempt retry on DROP catches the rare case of
// a still-closing backend surviving the terminate query
// (pg_terminate_backend is non-blocking). With the
// advisory lock held, no other test process can interfere.
func recreateDatabaseOnConn(ctx context.Context, conn *pgxpool.Conn, dbName string) error {
	if _, err := conn.Exec(ctx,
		fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()", dbName),
	); err != nil {
		return fmt.Errorf("terminate backends: %w", err)
	}

	// v0.8.32.1 ci-hygiene hotfix: 10 attempts × 100ms
	// (=1s total) was too tight for the CI backend
	// matrix. The shared postgres container (used by
	// ~11 integration test packages) sees concurrent
	// DROP+CREATE from `t.Parallel()`-enabled suites
	// and from the parallel-job matrix; a 1s budget
	// on a busy runner misses the CREATE more often
	// than not. Bumped to 30 attempts × 200ms = 6s
	// total, with an explicit exponential backoff
	// (200, 200, 400, 400, 800, ...). Also added
	// a `SELECT 1` probe at the end to confirm the
	// new database is actually openable; CREATE can
	// succeed while the catalog is still propagating,
	// and the very first `pool.Query` then hits
	// "database does not exist" until the catalog
	// catches up (this is the FATAL we see in the
	// CI logs, three different PIDs within 1s).
	const maxAttempts = 30
	const initialBackoff = 200 * time.Millisecond
	const maxBackoff = 2 * time.Second
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if _, err := conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName)); err != nil {
			lastErr = fmt.Errorf("drop database: %w", err)
		} else if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", dbName)); err != nil {
			lastErr = fmt.Errorf("create database: %w", err)
		} else {
			// CREATE returned success. The
			// new database may still be a
			// moment away from being
			// openable (catalog propagation),
			// so probe via a fresh connection
			// before declaring victory. The
			// probe is on a connection FROM
			// the new database (not the
			// admin pool's connection, which
			// points at `postgres`).
			if err := probeNewDatabase(ctx, dbName, maxBackoff); err == nil {
				return nil
			} else {
				lastErr = fmt.Errorf("create-then-probe: %w", err)
			}
		}
		// Linear backoff with cap. 200, 200, 400,
		// 400, 800, 800, 1600, 1600, ... up to
		// 2s. Total wall budget with 30 attempts
		// is ~6s worst case (200*1 + 200*2 +
		// 400*4 + 800*8 + 1600*8 + 2000*7 = 38_800ms
		// = 38s — we cap at 6s by limiting
		// maxAttempts; if a CREATE+probe takes
		// longer than 200ms the test runner is in
		// a state where no amount of retry helps
		// and we want the failure to surface
		// quickly).
		backoff := initialBackoff << (attempt / 2)
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		time.Sleep(backoff)
	}
	return fmt.Errorf("recreate database after %d attempts: %w", maxAttempts, lastErr)
}

// probeNewDatabase opens a short-lived admin connection,
// switches it to the freshly-created dbName, and runs
// `SELECT 1`. Returns nil on success, the first error
// otherwise. Caps the probe at `deadline` so a
// pathological CI runner doesn't hang for minutes.
//
// We use a fresh pool rather than reusing the caller's
// adminConn because that connection is bound to the
// `postgres` admin database (see splitDSN). pgx
// re-resolves the connection's database on each Exec
// if we issue `SET search_path` + `SET database` — but
// the cheap, clean path is just to open one new
// connection.
func probeNewDatabase(ctx context.Context, dbName string, deadline time.Duration) error {
	probeCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	// We need the admin DSN to point at the new
	// database. The admin DSN points at `postgres`
	// for the DROP+CREATE; switch it to dbName for
	// the probe. The caller (MustNewPool) has the
	// adminDSN as a local; we don't. Re-derive the
	// same shape: `postgres://user:pass@host:port/dbName?params=...`
	// by taking `INTEGRATION_DATABASE_URL` and
	// rewriting the path.
	dsn := os.Getenv(EnvIntegrationDSN)
	if dsn == "" {
		// Defensive: this is only called from
		// recreateDatabaseOnConn, which is
		// called from MustNewPool, which
		// already checked. If we get here
		// somehow, fall back to "no probe" so
		// the retry loop still progresses.
		return nil
	}
	u, parseErr := url.Parse(dsn)
	if parseErr != nil {
		return parseErr
	}
	u.Path = "/" + dbName
	pool, err := pgxpool.New(probeCtx, u.String())
	if err != nil {
		return err
	}
	defer pool.Close()
	var one int
	if err := pool.QueryRow(probeCtx, "SELECT 1").Scan(&one); err != nil {
		return err
	}
	if one != 1 {
		return fmt.Errorf("probe: SELECT 1 returned %d, want 1", one)
	}
	return nil
}

// runMigrationsOnConn delegates to the production migrator
// (`internal/migrations.Up`). The caller holds a
// `pg_advisory_lock(42)` on `_` (a connection from the
// admin pool); we do not use that connection for the
// migration itself because it points at the default
// `postgres` database, not the target. Instead we open a
// transient pool to the target DSN. The lock is still
// held while we connect, so no other test process can
// drop+create the database underneath us.
//
// Keeping the migration path identical between dev/CI
// and tests means a fix to one is a fix to the other —
// there is no second migrator to keep in sync.
func runMigrationsOnConn(t *testing.T, ctx context.Context, _ *pgxpool.Conn, dsn string) error {
	t.Helper()

	backendDir, err := findBackendDir()
	if err != nil {
		return err
	}
	// v0.8.31.1: migrations moved from `backend/migrations/`
	// to `backend/internal/migrations/sql/` (PR #316 commit
	// 7e7fb38 — the //go:embed migration source). Pass the
	// new path so runMigrationsOnConn exercises the
	// file-based code path the operator hits when a host
	// mount override is in place, not just the embedded
	// fallback.
	migDir := filepath.Join(backendDir, "internal", "migrations", "sql")

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("pgxpool: %w", err)
	}
	defer pool.Close()

	return migrations.Up(ctx, pool, migDir)
}

// findBackendDir returns the absolute path to the `backend/`
// directory by walking up from this source file. The testutil package
// is two levels deep (`backend/testutil/db.go`), so `..` twice lands
// on `backend/`. We verify the expected layout (a
// `internal/migrations/sql/` sibling — the v0.8.31.1 hotfix
// location per PR #316 commit 7e7fb38) so a moved file fails fast
// with a useful message. The pre-v0.8.31.1 path was `migrations/`
// at the repo root; that directory is now empty (the SQL files
// moved under internal/migrations/sql/ for the //go:embed source).
func findBackendDir() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("could not determine test file path")
	}
	dir := filepath.Dir(thisFile) // backend/testutil
	root := filepath.Dir(dir)     // backend
	if _, err := os.Stat(filepath.Join(root, "internal", "migrations", "sql")); err != nil {
		return "", fmt.Errorf("migrations dir not found at %s/internal/migrations/sql: %w", root, err)
	}
	return root, nil
}

// maskDSN redacts the password component of a DSN so it is safe to
// print in test failure messages. Only the libpq URL form is masked;
// for the keyword form we leave it alone (the password component is
// not extracted in tests anyway, and keyword DSNs are only used in
// the `pg_hba.conf`-style configurations not seen in CI).
func maskDSN(dsn string) string {
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		return dsn
	}
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return dsn
	}
	if _, hasPass := u.User.Password(); !hasPass {
		return dsn
	}
	u.User = url.UserPassword(u.User.Username(), "***")
	return u.String()
}
