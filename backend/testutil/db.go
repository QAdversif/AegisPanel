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

	const maxAttempts = 10
	const backoff = 100 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if _, err := conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName)); err != nil {
			lastErr = fmt.Errorf("drop database: %w", err)
			time.Sleep(backoff)
			continue
		}
		if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", dbName)); err != nil {
			lastErr = fmt.Errorf("create database: %w", err)
			time.Sleep(backoff)
			continue
		}
		return nil
	}
	return fmt.Errorf("recreate database after %d attempts: %w", maxAttempts, lastErr)
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
	migDir := filepath.Join(backendDir, "migrations")

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
// on `backend/`. We verify the expected layout (a `migrations/`
// sibling) so a moved file fails fast with a useful message.
func findBackendDir() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("could not determine test file path")
	}
	dir := filepath.Dir(thisFile) // backend/testutil
	root := filepath.Dir(dir)     // backend
	if _, err := os.Stat(filepath.Join(root, "migrations")); err != nil {
		return "", fmt.Errorf("migrations dir not found at %s/migrations: %w", root, err)
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
