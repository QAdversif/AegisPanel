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
// # Source resolution (v0.8.31.1 hotfix)
//
// The panel binary now `//go:embed`s the full set of SQL files
// (see `embeddedMigrations` below). The host-mounted dir
// passed as `dir` is treated as an operator override (the
// hot-fix path): if it exists and is non-empty, the migrator
// reads from it; otherwise it falls back to the embedded set.
// A host mount that is non-empty but missing any embedded
// migration fails loud — a partial override is almost always
// an install-contract bug (operator scp'd some but not all
// files after a release bump), not an intent, and silently
// falling back would let the panel boot with a partial
// schema and crash on the first query against a missing
// column (the v0.8.30/31 chicken-and-egg we hit on prod 2026-08-25).
//
// The pure helpers (UpBodyOf, StripSQLLineComments, SplitSQL) are
// exported so the integration test helper in `backend/testutil`
// can re-use them. The Up entry point is what `cmd/aegis/main.go`
// calls on boot.
package migrations

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// embeddedMigrations is the v0.8.31.1 hotfix canonical migration
// set, baked into the panel binary at compile time via
// //go:embed. The host-mounted dir at `dir` is an optional
// operator override (hot-fix path); if it's empty or absent,
// the migrator reads from this FS. The hotfix surfaces in two
// ways: (1) the v0.8.30/31 mTLS migration files (0023_agentca
// + 0024_add_nodes_agent_transport) are part of this embed,
// so a fresh v0.8.30+ install now applies them automatically
// without the operator having to scp them into the host
// mount; (2) the host-mount-as-override semantic is gated
// by a fail-loud completeness check, so a partial override
// can't silently leave the schema out of sync with the
// binary's expectation.
//
//go:embed all:sql/*.sql
var embeddedMigrations embed.FS

// Up applies every `*.sql` file in the resolved source in
// lexical order, each inside its own pgx transaction. Only
// the Up half of each goose-style file is applied — see
// UpBodyOf for the slicing rules.
//
// # Source resolution
//
//  1. If `dir` is non-empty and the directory exists with at
//     least one `*.sql` file, treat it as the operator
//     override path. Every embedded migration must also be
//     present in the override — a partial override is an
//     error, not a fallback (see package doc).
//  2. Otherwise (dir empty, missing, or has no `.sql` files),
//     read from the embedded FS (`sql/*.sql`).
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
	readFile, names, err := resolveSource(dir)
	if err != nil {
		return err
	}

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
		raw, err := readFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if err := applyOne(ctx, pool, name, raw); err != nil {
			return err
		}
		if err := recordMigration(ctx, pool, name); err != nil {
			return err
		}
	}
	return nil
}

// resolveSource picks the migration source for Up: the
// host-mounted dir if present and complete, otherwise the
// embedded FS. The fail-loud completeness check refuses a
// partial override (a host mount that has SOME but not all
// migrations present in the embed) — silently falling back
// would let the panel boot with a partial schema and crash
// on the first query against a missing column, the same
// failure mode that the v0.8.30/31 mTLS chicken-and-egg hit
// on prod 2026-08-25.
//
// The returned readFile closure reads a single file by name
// from whichever source was selected. The returned names
// list is the canonical migration set in lexical order.
func resolveSource(dir string) (readFile func(name string) (string, error), names []string, err error) {
	// Host-mounted dir (operator override path). The dir
	// may not exist (e.g. operator removed the volume
	// from the compose) — that's a clean signal to use
	// the embed, not an error. A present-but-unreadable
	// dir is a real error and we surface it.
	var dirEntries []os.DirEntry
	if dir != "" {
		var readErr error
		dirEntries, readErr = os.ReadDir(dir)
		if readErr != nil && !os.IsNotExist(readErr) {
			return nil, nil, fmt.Errorf("read migrations dir %q: %w", dir, readErr)
		}
	}

	// Try the host dir first: if it has any .sql files,
	// it's the operator's override intent. We then
	// validate the override is COMPLETE (every embedded
	// migration is present in the override) — a partial
	// override fails loud.
	if len(dirEntries) > 0 {
		var hostNames []string
		for _, e := range dirEntries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
				hostNames = append(hostNames, e.Name())
			}
		}
		if len(hostNames) > 0 {
			embeddedNames, err := readEmbeddedNames()
			if err != nil {
				return nil, nil, fmt.Errorf("read embedded migrations for validation: %w", err)
			}
			if missing := setDiff(embeddedNames, hostNames); len(missing) > 0 {
				return nil, nil, fmt.Errorf(
					"migrations dir %q is incomplete: missing %v "+
						"(the panel binary ships these via embed.FS; "+
						"see docs/operator-install.md \u00a7migrations \u2014 "+
						"either remove the host mount to use embedded, or "+
						"copy the missing files into the mount)",
					dir, missing,
				)
			}
			// Override accepted. Use the host dir's
			// files in lexical order.
			readFile := func(name string) (string, error) {
				data, rerr := os.ReadFile(filepath.Join(dir, name))
				if rerr != nil {
					return "", rerr
				}
				return string(data), nil
			}
			sort.Strings(hostNames)
			return readFile, hostNames, nil
		}
	}

	// No host mount (or empty): use the embedded FS.
	embeddedNames, err := readEmbeddedNames()
	if err != nil {
		return nil, nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	readFile = func(name string) (string, error) {
		data, rerr := embeddedMigrations.ReadFile("sql/" + name)
		if rerr != nil {
			return "", rerr
		}
		return string(data), nil
	}
	return readFile, embeddedNames, nil
}

// readEmbeddedNames lists the .sql files in the embedded
// sql/ directory in lexical order. The result is the
// canonical migration set the panel binary was built with.
func readEmbeddedNames() ([]string, error) {
	entries, err := embeddedMigrations.ReadDir("sql")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// setDiff returns the elements of `a` that are not present
// in `b`. The result is in the same (unsorted) order as `a`.
// Used by resolveSource to fail loud on a partial override.
func setDiff(a, b []string) []string {
	setB := make(map[string]struct{}, len(b))
	for _, n := range b {
		setB[n] = struct{}{}
	}
	var missing []string
	for _, n := range a {
		if _, ok := setB[n]; !ok {
			missing = append(missing, n)
		}
	}
	return missing
}

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

// upMarkerLine / downMarkerLine are line-anchored regexes
// that match the canonical goose `-- +migrate Up` /
// `-- +migrate Down` markers. They are line-anchored (the
// `(?m)^[ \t]*--[ \t]*\+migrate Up[ \t]*$` form) on purpose:
// a bare `strings.Index` for the literal `-- +migrate Up`
// substring would happily match a back-quoted reference
// inside a docstring (e.g. `-- this file has a `-- +migrate
// Up` section`), which would then make UpBodyOf return a
// slice of just the docstring comments and silently drop
// the real SQL that follows the actual marker. The
// 2026-08-25 v0.8.32.1 production-incident lesson: keep
// marker detection to whole lines so docstring prose can
// never shadow the real marker.
//
// The leading/trailing character classes are `[ \t]*`
// (space + tab only), NOT `\s*`: Go's `\s` includes `\n`,
// and a greedy `\s*$` would happily eat the line's trailing
// newline and the next-line content, defeating the
// line-anchored check. `[ \t]*` keeps the match to
// horizontal whitespace on the SAME line as the marker.
// The marker text itself is fixed (we do not accept
// `-- +migrate UpNoSpace` or other variants).
var (
	upMarkerLine   = regexp.MustCompile(`(?m)^[ \t]*--[ \t]*\+migrate Up[ \t]*$`)
	downMarkerLine = regexp.MustCompile(`(?m)^[ \t]*--[ \t]*\+migrate Down[ \t]*$`)
)

// upDownBodyOf is the shared implementation. `up` is true for
// the leading half (Up marker onward, stop at Down marker or
// EOF) and false for the trailing half (Down marker onward).
// Extracted so the two public helpers stay in lockstep — if the
// marker logic ever changes (e.g. to honour
// `-- +migrate StatementBegin` for multi-statement files), the
// change is made in one place.
//
// Markers must be on their own line (see upMarkerLine /
// downMarkerLine for the precise regex). A marker that
// appears inside a comment — e.g. a docstring explaining
// the file layout — is intentionally NOT a marker, so
// authors can write `-- this file ships both an Up and a
// Down section` without confusing the parser.
func upDownBodyOf(raw string, up bool) string {
	upLoc := upMarkerLine.FindStringIndex(raw)
	downLoc := downMarkerLine.FindStringIndex(raw)

	if up {
		if upLoc == nil {
			return raw
		}
		// Stop at the Down marker if present and after
		// the Up marker, otherwise return to end of
		// file. The slice keeps the Up marker line
		// itself so error messages that surface a
		// failing statement also surface a useful
		// line of context.
		//
		// gocritic weakCond: FindStringIndex returns
		// a nil []int when the regex does not match.
		// The `len(downLoc) == 0` form is the
		// explicit-empty-slice form and silences the
		// "nil check may not be enough, check for len"
		// warning.
		if len(downLoc) == 0 || downLoc[0] < upLoc[0] {
			return raw[upLoc[0]:]
		}
		return raw[upLoc[0]:downLoc[0]]
	}

	// Down slice.
	if len(downLoc) == 0 {
		return ""
	}
	return raw[downLoc[0]:]
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
