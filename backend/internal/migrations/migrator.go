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

	"github.com/jackc/pgx/v5/pgxpool"
)

// Up applies every `*.sql` file under `dir` in lexical order, each
// inside its own pgx transaction. Only the Up half of each
// goose-style file is applied — see UpBodyOf for the slicing
// rules.
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

	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if err := applyOne(ctx, pool, name, string(raw)); err != nil {
			return err
		}
	}
	return nil
}

// applyOne runs every Up-only statement of `raw` inside a single
// pgx transaction. The Tx wrapper provides the atomicity that
// the file-level BEGIN; ... COMMIT; would have provided in a tool
// that honoured multi-statement transactions. If a statement
// fails the Tx is rolled back, the file is left in its pre-state,
// and we return an error that includes the failing statement's
// first line for triage.
func applyOne(ctx context.Context, pool *pgxpool.Pool, name, raw string) error {
	body := UpBodyOf(raw)
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
	const up = "-- +migrate Up"
	upIdx := strings.Index(raw, up)
	if upIdx < 0 {
		return raw
	}
	const down = "-- +migrate Down"
	after := raw[upIdx+len(up):]
	downIdx := strings.Index(after, down)
	if downIdx < 0 {
		return raw[upIdx:]
	}
	return raw[upIdx : upIdx+len(up)+downIdx]
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
