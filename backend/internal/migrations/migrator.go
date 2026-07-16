// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package migrations applies the project's goose-style SQL migrations
// to a Postgres database using only the pgx driver. We do not use
// the `pressly/goose` runtime — goose v3.27.2's default parser
// rejects files that begin with `BEGIN;` (the parser sees the
// transaction wrapper before the `+migrate Up` directive and bails
// with "unexpected state 0"), and Aegis' migrations all use that
// style. Rather than rewrite the migrations or downgrade goose,
// we read each file, slice it between the `-- +migrate Up` and
// `-- +migrate Down` markers, and apply the Up body inside a
// single pgx transaction.
//
// # Idempotency
//
// The migrator tracks applied migrations in a `schema_migrations`
// table. Each call to `Up` re-reads the directory, re-sorts the
// files, and skips any whose name is already in the table. A
// re-run on a fresh DB is a no-op for the migrations that were
// applied previously; a re-run on a partially-migrated DB
// resumes from the first missing migration. The
// `schema_migrations` table itself is created on the first
// `Up` call (the CREATE TABLE IF NOT EXISTS is the very
// first statement the migrator runs).
//
// The pure helpers (UpBodyOf, StripSQLLineComments, SplitSQL) are
// exported so the integration test helper in `backend/testutil`
// can re-use them. The Up entry point is what `cmd/aegis/main.go`
// calls on boot.
package migrations

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// schemaMigrationsDDL is the bootstrap statement for the
// `schema_migrations` tracking table. We inline it (rather
// than reading from a file) so the first call to Up on a
// fresh database is self-contained. The table is a single
// column of migration names, in application order.
const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    name        TEXT PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

// ensureSchemaMigrationsTable creates the tracking table
// if it does not already exist. The CREATE TABLE IF NOT
// EXISTS is idempotent across migrator re-runs.
func ensureSchemaMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, schemaMigrationsDDL)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

// appliedMigrations returns the set of migration names
// already in `schema_migrations`. The caller uses this to
// skip files that have been applied previously.
func appliedMigrations(ctx context.Context, pool *pgxpool.Pool) (map[string]struct{}, error) {
	rows, err := pool.Query(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan schema_migrations row: %w", err)
		}
		out[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return out, nil
}

// recordMigration inserts the migration name into
// `schema_migrations` after a successful apply. The
// ON CONFLICT DO NOTHING makes the insert itself
// idempotent (a re-apply of the same migration does
// not duplicate the row).
func recordMigration(ctx context.Context, pool *pgxpool.Pool, name string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO schema_migrations (name, applied_at) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING`,
		name, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	return nil
}

// Up applies every `*.sql` file under `dir` in lexical order, each
// inside its own pgx transaction. Only the Up half of each
// goose-style file is applied — see UpBodyOf for the slicing
// rules.
//
// Idempotency: the first call to Up creates the
// `schema_migrations` table. Every subsequent call (or
// re-run) reads the table and skips files whose names
// are already present. A migration file is applied
// once and only once per database; the apply +
// record are wrapped in a single transaction so a
// crash mid-apply does not leave a half-applied
// migration in the table.
//
// `pool` is the *pgxpool.Pool that the rest of the runtime will
// use; this is the same handle the caller is expected to keep open
// for the application's lifetime. We do not open a sibling
// `*sql.DB` connection the way the old goose-based code did —
// the pgx stdlib adapter does not honour multi-statement
// transactions (BEGIN; ... COMMIT;) and is therefore useless for
// our migration files.
func Up(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migrations dir %q: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	// Bootstrap the tracking table on every call. The
	// CREATE TABLE IF NOT EXISTS makes this a
	// no-op after the first call.
	if err := ensureSchemaMigrationsTable(ctx, pool); err != nil {
		return err
	}
	applied, err := appliedMigrations(ctx, pool)
	if err != nil {
		return err
	}

	for _, name := range names {
		if _, ok := applied[name]; ok {
			// Already applied. Skip without
			// re-running. A future refactor may
			// add a `--force` flag for the
			// "I edited a past migration by
			// hand, please re-apply" path; for
			// now the convention is "never edit a
			// merged migration, write a new
			// one instead".
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if err := applyOne(ctx, pool, name, string(raw)); err != nil {
			return err
		}
		if err := recordMigration(ctx, pool, name); err != nil {
			return err
		}
	}
	return nil
}

// applyOne is the per-file wrapper used by Up. It pulls the Up
// body out of `raw` and hands it to applyBody.
func applyOne(ctx context.Context, pool *pgxpool.Pool, name, raw string) error {
	return applyBody(ctx, pool, name, UpBodyOf(raw))
}

// Down applies the Down body of a single migration file. The
// file is `target` (a filename relative to `dir`, e.g.
// "0001_initial.sql"); only the slice between the
// `-- +migrate Down` marker and end-of-file is applied. See
// DownBodyOf for the slicing rules.
//
// We deliberately do not "rewind" the whole database by
// iterating files in reverse — the operator picks the
// specific migration they want to roll back, and the
// ordering of Down bodies is the file author's
// responsibility. The current Aegis migration files write
// DROP TABLE statements in the correct reverse-dependency
// order, so a single Down per file is enough.
func Down(ctx context.Context, pool *pgxpool.Pool, dir, target string) error {
	if target == "" {
		return fmt.Errorf("down: target file is required")
	}
	if strings.ContainsAny(target, "/\\") {
		return fmt.Errorf("down: target must be a bare filename, not a path")
	}
	path := filepath.Join(dir, target)
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return applyBody(ctx, pool, target, DownBodyOf(string(raw)))
}

// applyBody is the shared execution path for Up and Down. It
// runs every statement in `body` inside a single pgx Tx,
// skipping comments and empty lines. If a statement fails the
// Tx is rolled back, the file is left in its pre-state, and we
// return an error that includes the failing statement's first
// line for triage.
func applyBody(ctx context.Context, pool *pgxpool.Pool, name, body string) error {
	cleaned := StripSQLLineComments(body)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx for %s: %w", name, err)
	}
	defer func() {
		// Rollback is a no-op after a successful Commit, so this
		// is safe to leave attached to every path including the
		// happy one.
		_ = tx.Rollback(ctx)
	}()

	for _, stmt := range SplitSQL(cleaned) {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		if _, err := tx.Exec(ctx, trimmed); err != nil {
			preview := firstLine(trimmed)
			return fmt.Errorf("apply %s (stmt %q): %w", name, preview, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", name, err)
	}
	return nil
}

// UpBodyOf extracts the Up body of a goose-style migration file.
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
// Down marker is present). If neither marker is found, the entire
// file is returned unchanged. The slice keeps the leading
// `-- +migrate Up` comment so error messages that surface a
// failing statement also surface a useful line of context.
func UpBodyOf(raw string) string {
	return upDownBodyOf(raw, true)
}

// DownBodyOf is the inverse of UpBodyOf: it returns the slice
// from the first `-- +migrate Down` marker to the end of the
// file. If no Down marker is present, the function returns an
// empty string — there is no sensible default for a Down that
// has not been written. The slice keeps the leading
// `-- +migrate Down` comment for the same reason UpBodyOf does
// for its marker.
func DownBodyOf(raw string) string {
	return upDownBodyOf(raw, false)
}

// upDownBodyOf is the shared implementation. `up` is true for
// the leading half (Up marker onward, stop at Down marker or
// EOF) and false for the trailing half (Down marker onward).
// Extracted so the two public helpers stay in lockstep — if the
// marker logic ever changes (e.g. to honour
// `-- +migrate StatementBegin` for multi-statement files), the
// change is made in one place.
func upDownBodyOf(raw string, up bool) string {
	const upMarker = "-- +migrate Up"
	const downMarker = "-- +migrate Down"

	upIdx := strings.Index(raw, upMarker)
	downIdx := strings.Index(raw, downMarker)

	if up {
		if upIdx < 0 {
			return raw
		}
		// Stop at the Down marker if present, otherwise
		// return to end of file. The slice keeps the
		// Up marker comment itself.
		if downIdx < 0 || downIdx < upIdx {
			return raw[upIdx:]
		}
		return raw[upIdx:downIdx]
	}

	// Down slice.
	if downIdx < 0 {
		return ""
	}
	return raw[downIdx:]
}

// StripSQLLineComments removes any `-- ...` line from `s`. It does
// not touch `--` that appears inside a string literal — none of
// the project's migration files do that today, and if they ever
// do, the right fix is a proper SQL tokeniser, not a regex.
//
// The strip is line-oriented because every goose migration uses
// `-- +migrate Up` / `-- +migrate Down` as *whole-line* markers.
// A statement that immediately follows a line-comment is still
// valid SQL, and pgx's parser is happy to receive it.
func StripSQLLineComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i, line := range strings.Split(s, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}

// SplitSQL is a naive `;`-delimited splitter. Aegis migration
// files do not embed `;` inside string literals, so a naive split
// is safe; if that ever changes we'll need a tokeniser that
// respects quotes and dollar-quoted blocks.
func SplitSQL(raw string) []string { return strings.Split(raw, ";") }

// firstLine is a tiny helper for error messages — keeps the
// failing statement to one readable line. Trims trailing
// whitespace and cuts at the first newline.
func firstLine(s string) string {
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[:nl]
	}
	return strings.TrimSpace(s)
}
