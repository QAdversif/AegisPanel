// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration

// Integration test for the migrator's `down` path. Run with:
//
//	INTEGRATION_DATABASE_URL=postgres://aegis:aegis@localhost:5432/aegis_it \
//	  go test -tags=integration ./internal/migrations/...
//
// v0.8.32.2 (#306) regression guard. Pre-fix,
// `migrate down 0013_nodes_agent_bearer.sql` did not
// delete the journal row, so a `down → up` sequence
// permanently skipped the migration. The fix wraps
// the rollback DDL and the journal DELETE in a
// single transaction; the test exercises the full
// up → down → up cycle against a live postgres
// container and asserts the second `up` re-applies
// the migration (i.e. the journal row was removed by
// `down`).
//
// The test uses a tempdir with a single migration
// (`0010_x.sql`) so the cycle is bounded to one file;
// the journal starts empty, the first `up` records
// the row, `down` removes it, and the second `up`
// re-applies. Without the fix, the second `up` is a
// no-op (Up skips anything in the applied-map) and
// the schema never gets the table; the test would
// then fail at the second `appliedMigrations` check.

package migrations

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool opens a pgxpool to the integration test
// database. The migrations package cannot import
// testutil (it would cycle through `testutil.db.go`,
// which calls `migrations.Up` itself), so the
// integration test owns its pool. The connection
// string is the same one testutil.MustNewPool uses —
// `INTEGRATION_DATABASE_URL` with a `postgres://aegis:aegis@localhost:5432/aegis_it?sslmode=disable`
// default — to keep the two paths consistent.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("INTEGRATION_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://aegis:aegis@localhost:5432/aegis_it?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pgxpool.New: %v (is the integration postgres up? INTEGRATION_DATABASE_URL=%q)", err, dsn)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

func TestUpDownUp_CycleClearsJournalRow_306(t *testing.T) {
	dir := t.TempDir()
	const name = "0010_test.sql"
	body := "-- +migrate Up\n" +
		"CREATE TABLE migrator_cycle_test (id INT PRIMARY KEY);\n" +
		"-- +migrate Down\n" +
		"DROP TABLE migrator_cycle_test;\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	// First up: applies the migration, records the
	// row. Verify both the schema and the journal.
	if err := Up(ctx, pool, dir); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	applied, err := appliedMigrations(ctx, pool)
	if err != nil {
		t.Fatalf("appliedMigrations after first up: %v", err)
	}
	if _, ok := applied[name]; !ok {
		t.Fatalf("first Up did not record the journal row for %s; got applied=%v", name, applied)
	}
	if _, err := pool.Exec(ctx, "SELECT count(*) FROM migrator_cycle_test"); err != nil {
		t.Fatalf("migrator_cycle_test table missing after first Up: %v", err)
	}

	// Down: rolls back the DDL AND (v0.8.32.2 fix)
	// removes the journal row in the same transaction.
	if err := Down(ctx, pool, dir, name); err != nil {
		t.Fatalf("Down: %v", err)
	}
	applied, err = appliedMigrations(ctx, pool)
	if err != nil {
		t.Fatalf("appliedMigrations after down: %v", err)
	}
	if _, ok := applied[name]; ok {
		t.Fatalf("Down did not remove the journal row for %s; got applied=%v (this is the #306 bug)", name, applied)
	}
	if _, err := pool.Exec(ctx, "SELECT count(*) FROM migrator_cycle_test"); err == nil {
		t.Fatal("migrator_cycle_test table still present after Down")
	}

	// Second up: the journal is empty (Down cleared it),
	// so the migration re-applies. Verify the schema
	// is back and the journal has the row again.
	if err := Up(ctx, pool, dir); err != nil {
		t.Fatalf("second Up: %v (this is the #306 bug — Up skipped the migration because the journal still held the row)", err)
	}
	applied, err = appliedMigrations(ctx, pool)
	if err != nil {
		t.Fatalf("appliedMigrations after second up: %v", err)
	}
	if _, ok := applied[name]; !ok {
		t.Fatalf("second Up did not record the journal row; got applied=%v", applied)
	}
	if _, err := pool.Exec(ctx, "SELECT count(*) FROM migrator_cycle_test"); err != nil {
		t.Fatalf("migrator_cycle_test table missing after second Up: %v (this is the #306 bug — the migration was skipped)", err)
	}
}
