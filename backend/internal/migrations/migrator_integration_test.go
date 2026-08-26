// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration

// Integration test for the migrator's down-side
// journal semantics. Run with:
//
//	INTEGRATION_DATABASE_URL=postgres://aegis:aegis@localhost:5432/aegis_it \
//	  go test -tags=integration ./internal/migrations/...
//
// v0.8.32.2 (#306) regression guard. The fix wraps
// the rollback DDL and the journal DELETE in a
// single transaction so a `down → up` cycle
// round-trips the schema and the journal
// atomically.
//
// The test does not call `Up` / `Down` directly
// because `Up` requires a complete migrations dir
// (the override-completeness check from PR #318 /
// v0.8.32.1 fail-loud on a partial override), and a
// tempdir with a single stub migration would not
// satisfy that check. Instead the test exercises
// the migrator's primitive `applyBody` +
// `recordMigrationInTx` + `unrecordMigrationInTx`
// pipeline directly against a live postgres, which
// is the surface #306 changes (atomicity of the
// journal update with the schema DDL).
//
// The test cleans up the schema_migrations table
// and the migrator_cycle_test table in a deferred
// `t.Cleanup` so a previous failure does not leak
// into the next run.

package migrations

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool opens a pgxpool to the integration test
// database. The migrations package cannot import
// testutil (it would cycle through `testutil.db.go`,
// which calls `migrations.Up` itself), so the
// integration test owns its pool. The DSN matches
// `testutil.MustNewPool`'s default so the two paths
// are consistent.
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
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	// Bootstrap the schema_migrations table once. The
	// helper is idempotent (CREATE TABLE IF NOT
	// EXISTS), so re-runs across the test suite are
	// safe.
	if err := ensureSchemaMigrationsTable(ctx, pool); err != nil {
		t.Fatalf("ensureSchemaMigrationsTable: %v", err)
	}

	const name = "0010_test_migrator_cycle.sql"
	upBody := "CREATE TABLE migrator_cycle_test (id INT PRIMARY KEY);"
	downBody := "DROP TABLE migrator_cycle_test;"

	// Clean up: any leftover row from a prior failed
	// run breaks the "starts at zero" assumption of
	// the test.
	if _, err := pool.Exec(ctx,
		`DELETE FROM schema_migrations WHERE name = $1`, name,
	); err != nil {
		t.Fatalf("cleanup DELETE: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS migrator_cycle_test`); err != nil {
		t.Fatalf("cleanup DROP: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort; don't fail the test if
		// cleanup races with another test's
		// connection.
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM schema_migrations WHERE name = $1`, name)
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS migrator_cycle_test`)
	})

	// --- First up: apply the Up body, record the row
	// in the SAME transaction. Mirrors what `Up`
	// does on the post-fix code path (applyBody +
	// recordMigrationInTx + Commit).
	tx, err := applyBody(ctx, pool, name, upBody)
	if err != nil {
		t.Fatalf("first applyBody: %v", err)
	}
	if err := recordMigrationInTx(ctx, tx, name); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("first recordMigrationInTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	// Assert: schema has the table, journal has the row.
	if _, err := pool.Exec(ctx, "SELECT count(*) FROM migrator_cycle_test"); err != nil {
		t.Fatalf("migrator_cycle_test table missing after first up: %v", err)
	}
	applied, err := appliedMigrations(ctx, pool)
	if err != nil {
		t.Fatalf("appliedMigrations after first up: %v", err)
	}
	if _, ok := applied[name]; !ok {
		t.Fatalf("first up did not record the journal row for %s; got applied=%v", name, applied)
	}

	// --- Down: apply the rollback DDL, remove the
	// journal row in the SAME transaction. Mirrors
	// what `Down` does on the post-fix code path.
	tx, err = applyBody(ctx, pool, name, downBody)
	if err != nil {
		t.Fatalf("down applyBody: %v", err)
	}
	if err := unrecordMigrationInTx(ctx, tx, name); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("down unrecordMigrationInTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("down commit: %v", err)
	}
	// Assert: schema is rolled back, journal no
	// longer has the row. The journal-empty check is
	// the load-bearing one for #306: pre-fix,
	// `unrecordMigrationInTx` did not exist and
	// `Down` never deleted the row, so the next
	// `Up` would have permanently skipped the
	// migration.
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

	// --- Second up: with the journal empty, the
	// migration re-applies. This is the recovery
	// path that was broken pre-fix — the journal
	// row was left in place, so `Up` skipped the
	// migration and the schema was never re-applied.
	tx, err = applyBody(ctx, pool, name, upBody)
	if err != nil {
		t.Fatalf("second applyBody: %v (this is the #306 bug — Up skipped the migration because the journal still held the row)", err)
	}
	if err := recordMigrationInTx(ctx, tx, name); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("second recordMigrationInTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("second commit: %v", err)
	}
	// Assert: schema is back, journal has the row
	// again.
	if _, err := pool.Exec(ctx, "SELECT count(*) FROM migrator_cycle_test"); err != nil {
		t.Fatalf("migrator_cycle_test table missing after second up: %v (this is the #306 bug — the migration was skipped)", err)
	}
	applied, err = appliedMigrations(ctx, pool)
	if err != nil {
		t.Fatalf("appliedMigrations after second up: %v", err)
	}
	if _, ok := applied[name]; !ok {
		t.Fatalf("second up did not record the journal row; got applied=%v", applied)
	}
}
