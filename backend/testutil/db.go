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
//   1. ping the server;
//   2. ensure no other suite is using the same database (DROP+CREATE
//      on the configured DB so concurrent runs don't clobber each
//      other when they share a Postgres instance);
//   3. run every goose migration from `migrations/`.
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
)

// EnvIntegrationDSN is the connection string the integration tests
// expect. When unset the tests call `t.Skip` rather than failing —
// this keeps `go test ./...` clean for anyone who does not have a
// Postgres handy (CI is the only environment that must set it).
const EnvIntegrationDSN = "INTEGRATION_DATABASE_URL"

// MustNewPool connects to INTEGRATION_DATABASE_URL, drops and
// recreates the target database, applies every goose migration in
// `migrations/`, and returns a ready-to-use *pgxpool.Pool. The pool
// is closed via `t.Cleanup`.
//
// If INTEGRATION_DATABASE_URL is empty, the test is skipped with a
// message that points the reader at the Makefile target.
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

	if err := recreateDatabase(t, adminDSN, dbName); err != nil {
		t.Fatalf("recreate database %q: %v", dbName, err)
	}

	if err := runMigrations(t, dsn); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

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
		return "", "", errors.New("keyword DSN must include dbname=...")
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

// recreateDatabase force-recreates the target database. We terminate
// any existing connections first so the DROP doesn't block on a
// lingering session.
func recreateDatabase(t *testing.T, adminDSN, dbName string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx,
		fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()", dbName),
	); err != nil {
		return fmt.Errorf("terminate backends: %w", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName)); err != nil {
		return fmt.Errorf("drop database: %w", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", dbName)); err != nil {
		return fmt.Errorf("create database: %w", err)
	}
	return nil
}

// runMigrations applies every `.sql` file in the project-root
// `migrations/` directory in lexical order, each in its own
// transaction.
//
// We deliberately bypass goose here. The production migrator
// (`cmd/aegis/main.go`) uses `goose.UpContext`, but goose v3.27.2's
// default parser rejects files that begin with an explicit
// `BEGIN;` transaction wrapper. The integration suite only needs
// the schema in place — not goose's version-tracking table.
//
// pgx's Exec helpers do not transparently apply a multi-statement
// string that contains its own `BEGIN;` / `COMMIT;` block: in
// extended-query mode each statement is sent separately, so the
// first `BEGIN` opens a server-side transaction that is left
// dangling when pgx moves on. The simplest portable answer is to
// split the file into individual SQL statements on `;` (none of
// the project's migration files embed a `;` inside a string
// literal, so a naïve split is safe here) and run each one
// inside a single `pgx.Tx`. Comments and empty lines are skipped
// so we don't waste round-trips on `-- +migrate Up` markers.
//
// If a future migration embeds `;` inside a string literal or
// uses a feature that needs goose-only tooling (e.g. `goose fix`,
// Go migrators, multi-version downgrades) we'll need to revisit
// this helper and align the production migrator's behaviour.
func runMigrations(t *testing.T, dsn string) error {
	t.Helper()

	backendDir, err := findBackendDir()
	if err != nil {
		return err
	}

	migDir := filepath.Join(backendDir, "migrations")
	entries, err := os.ReadDir(migDir)
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("pgxpool: %w", err)
	}
	defer pool.Close()

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(migDir, e.Name()))
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Name(), err)
		}
		if err := applyMigration(ctx, pool, e.Name(), string(raw)); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs every statement in `raw` inside a single
// `pgx.Tx`. See runMigrations for the broader rationale.
//
// Each goose-style migration file interleaves an "Up" body and a
// "Down" body, separated by `-- +migrate Up` and `-- +migrate Down`
// markers. We only want the Up half — running both means a fresh
// database would be created and immediately dropped, which is
// what bit us on the first attempt at this helper. `upBodyOf`
// returns the slice between the two markers (or the whole file
// if no Down marker is present, which is the safe default).
func applyMigration(ctx context.Context, pool *pgxpool.Pool, name, raw string) error {
	body, err := upBodyOf(raw)
	if err != nil {
		return err
	}
	cleaned := stripSQLLineComments(body)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx for %s: %w", name, err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	for _, stmt := range splitSQL(cleaned) {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		if _, err := tx.Exec(ctx, trimmed); err != nil {
			return fmt.Errorf("apply %s (stmt: %s...): %w", name, truncate(trimmed, 60), err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", name, err)
	}
	return nil
}

// upBodyOf extracts the Up body of a goose-style migration file.
// The file may look like:
//
//	BEGIN;
//	-- +migrate Up
//	CREATE TABLE ...;
//	-- +migrate Down
//	DROP TABLE ...;
//	COMMIT;
//
// We return the slice from the first `-- +migrate Up` marker to
// the first `-- +migrate Down` marker (or to end of file if no
// Down marker is present). `BEGIN;` / `COMMIT;` outside the
// marked region are kept; `tx.Begin` / `tx.Commit` in
// applyMigration will own the transaction boundary, so the
// file-level BEGIN/COMMIT are ignored as separate statements
// (the regex-tolerant pgx will not flip a "BEGIN" inside an
// already-open Tx into a savepoint, but the syntactic noise is
// harmless either way).
func upBodyOf(raw string) (string, error) {
	upIdx := strings.Index(raw, "-- +migrate Up")
	if upIdx < 0 {
		// No Up marker — assume the whole file is the Up body.
		// This matches goose's own behaviour for migrations that
		// ship without explicit Up/Down split.
		return raw, nil
	}
	// Walk forward from upIdx to find the first "-- +migrate Down"
	// marker at line start. If absent, return the rest of the file.
	after := raw[upIdx+len("-- +migrate Up"):]
	downIdx := strings.Index(after, "-- +migrate Down")
	if downIdx < 0 {
		return raw[upIdx:], nil
	}
	return raw[upIdx : upIdx+len("-- +migrate Up")+downIdx], nil
}

// stripSQLLineComments removes any `-- ...` line from the input.
// It does NOT touch `--` that appears inside a string literal —
// none of Aegis' migrations do that today, and if we ever do, the
// right fix is a proper SQL tokeniser, not a regex.
func stripSQLLineComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, line := range strings.Split(s, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}

// splitSQL is a naive `;`-delimited splitter. Aegis migration files
// do not embed `;` inside string literals, so this is safe; if
// that ever changes we'll need a tokeniser that respects quotes
// and dollar-quoted blocks.
func splitSQL(raw string) []string { return strings.Split(raw, ";") }

// truncate is a tiny helper for error messages — keeps the failing
// statement to one readable line.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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
	root := filepath.Dir(dir)      // backend
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
