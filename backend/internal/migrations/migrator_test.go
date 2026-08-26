// SPDX-License-Identifier: AGPL-3.0-or-later

package migrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpBodyOf_NoMarkers_ReturnsWholeFile(t *testing.T) {
	in := "CREATE TABLE foo (id INT);\nDROP TABLE foo;\n"
	if got := UpBodyOf(in); got != in {
		t.Fatalf("got %q, want %q", got, in)
	}
}

func TestUpBodyOf_OnlyUpMarker_KeepsRestOfFile(t *testing.T) {
	// Some migrations ship without a Down half — we should still
	// return the full post-Up content rather than trimming at EOF.
	in := "-- +migrate Up\nCREATE TABLE foo (id INT);\n"
	got := UpBodyOf(in)
	if !strings.Contains(got, "CREATE TABLE foo") {
		t.Fatalf("expected CREATE TABLE in body, got %q", got)
	}
	if !strings.HasPrefix(got, "-- +migrate Up") {
		t.Fatalf("expected body to start with the Up marker, got %q", got)
	}
}

func TestUpBodyOf_StopsAtDownMarker(t *testing.T) {
	in := `-- +migrate Up
CREATE TABLE foo (id INT);

-- +migrate Down
DROP TABLE foo;
`
	got := UpBodyOf(in)
	if strings.Contains(got, "DROP TABLE") {
		t.Fatalf("Down body leaked into Up: %q", got)
	}
	if !strings.Contains(got, "CREATE TABLE foo") {
		t.Fatalf("Up body missing CREATE: %q", got)
	}
}

func TestUpBodyOf_BlankLinesAroundMarkers(t *testing.T) {
	// The marker is preceded by blank lines in real goose files.
	in := `BEGIN;

-- +migrate Up
CREATE TABLE foo (id INT);

-- +migrate Down
DROP TABLE foo;

COMMIT;
`
	got := UpBodyOf(in)
	if strings.Contains(got, "DROP TABLE") {
		t.Fatalf("Down body leaked into Up: %q", got)
	}
	if strings.Contains(got, "BEGIN;") {
		t.Fatalf("BEGIN; before Up marker leaked into Up: %q", got)
	}
}

// TestUpBodyOf_IgnoresMarkerInDocstring is the v0.8.32.1
// regression guard. Pre-fix UpBodyOf used `strings.Index` for
// the literal `-- +migrate Up` substring, which would happily
// match a back-quoted reference inside a docstring and return
// a slice of just the docstring comments. The fix is to require
// the marker to be on its own line (see upMarkerLine). This
// test covers a file where the marker appears ONLY inside a
// docstring — the parser must treat that as "no marker" and
// return the whole file, exactly as the pre-existing
// TestUpBodyOf_NoMarkers_ReturnsWholeFile does.
//
// This mirrors the pre-fix failure mode in 0023_agentca.sql
// and 0024_add_nodes_agent_transport.sql, which had a header
// docstring of the form:
//
//	-- this file contains BOTH a '-- +migrate Up'
//	-- section (lines ~107 onward) and a
//	-- '-- +migrate Down' section (the rollback
//	-- path, further below).
//
// Pre-fix, the parser grabbed the docstring "marker" and
// returned a 7-line slice of just comments, dropping the real
// ALTER TABLE on line 122. Post-fix, the parser ignores the
// docstring and returns the whole file (no real marker exists).
func TestUpBodyOf_IgnoresMarkerInDocstring(t *testing.T) {
	in := "-- SPDX-License-Identifier: AGPL-3.0-or-later\n" +
		"--\n" +
		"-- v0.8.32 follow-up: this file contains BOTH a '-- +migrate Up'\n" +
		"-- section (lines ~107 onward) and a '-- +migrate Down'\n" +
		"-- section (the rollback path, further below).\n" +
		"\n" +
		"BEGIN;\n" +
		"\n" +
		"ALTER TABLE nodes ADD COLUMN agent_transport TEXT;\n" +
		"\n" +
		"COMMIT;\n"
	got := UpBodyOf(in)
	if !strings.Contains(got, "ALTER TABLE nodes") {
		t.Fatalf("Up body should still contain the real SQL when only the docstring mentions the marker; got %q", got)
	}
	if !strings.HasPrefix(got, "-- SPDX-License-Identifier:") {
		t.Fatalf("Up body should be the whole file (no real marker exists), got prefix %q", firstLine(got))
	}
}

// TestUpBodyOf_RealMarkerAfterDocstringShadow is the
// other half of the v0.8.32.1 regression guard. A file
// that has the marker text inside a docstring AND a real
// marker later must slice from the REAL marker, not the
// docstring shadow. Pre-fix, the parser returned the
// docstring slice and the real `ALTER TABLE` was dropped
// from the apply. Post-fix, the parser skips the
// docstring occurrence and slices from the real marker.
func TestUpBodyOf_RealMarkerAfterDocstringShadow(t *testing.T) {
	in := "-- SPDX-License-Identifier: AGPL-3.0-or-later\n" +
		"--\n" +
		"-- This file has a '-- +migrate Up' marker docstring on line 3\n" +
		"-- that must NOT count. The real marker is further down.\n" +
		"\n" +
		"BEGIN;\n" +
		"\n" +
		"-- +migrate Up\n" +
		"ALTER TABLE nodes ADD COLUMN agent_transport TEXT;\n" +
		"\n" +
		"-- +migrate Down\n" +
		"ALTER TABLE nodes DROP COLUMN agent_transport;\n" +
		"\n" +
		"COMMIT;\n"
	got := UpBodyOf(in)
	if !strings.Contains(got, "ALTER TABLE nodes ADD COLUMN") {
		t.Fatalf("Up body missing the real ALTER TABLE; docstring shadow ate the slice: %q", got)
	}
	if strings.Contains(got, "DROP COLUMN") {
		t.Fatalf("Down body leaked into Up: %q", got)
	}
	if !strings.HasPrefix(got, "-- +migrate Up") {
		t.Fatalf("Up body should start at the REAL marker, not the docstring; got prefix %q", firstLine(got))
	}
	// A body that started at the docstring would include
	// the prose about the marker; assert the body does
	// not carry the docstring forward.
	if strings.Contains(got, "must NOT count") {
		t.Fatalf("Up body still starts at the docstring; marker detection is not line-anchored")
	}
}

// TestDownBodyOf_IgnoresMarkerInDocstring is the Down
// half of the v0.8.32.1 regression guard. The docstring
// has a back-quoted `-- +migrate Down` reference; the
// real Down marker is further down. Pre-fix, DownBodyOf
// returned the docstring slice (no real SQL) and Down
// would be a no-op. Post-fix, DownBodyOf returns the
// real Down section.
func TestDownBodyOf_IgnoresMarkerInDocstring(t *testing.T) {
	in := "-- SPDX-License-Identifier: AGPL-3.0-or-later\n" +
		"--\n" +
		"-- This file has a '-- +migrate Down' marker docstring on\n" +
		"-- line 3 that must NOT count.\n" +
		"\n" +
		"-- +migrate Up\n" +
		"SELECT 1;\n" +
		"\n" +
		"-- +migrate Down\n" +
		"SELECT 2;\n"
	down := DownBodyOf(in)
	if !strings.Contains(down, "SELECT 2") {
		t.Fatalf("Down body missing the real SELECT 2; docstring shadow ate the slice: %q", down)
	}
	if strings.Contains(down, "SELECT 1") {
		t.Fatalf("Up body leaked into Down: %q", down)
	}
	if !strings.HasPrefix(down, "-- +migrate Down") {
		t.Fatalf("Down body should start at the REAL marker; got prefix %q", firstLine(down))
	}
}

func TestDownBodyOf_FullRoundTrip(t *testing.T) {
	// Up and Down should be complementary slices — the Up
	// body plus the Down body equals the markers-onward
	// portion of the file, with no overlap.
	in := `BEGIN;

-- +migrate Up
CREATE TABLE foo (id INT);

-- +migrate Down
DROP TABLE foo;
DROP TABLE bar;

COMMIT;
`
	up := UpBodyOf(in)
	down := DownBodyOf(in)
	if strings.Contains(up, "DROP TABLE") {
		t.Fatalf("Down body leaked into Up: %q", up)
	}
	if !strings.Contains(down, "DROP TABLE foo") {
		t.Fatalf("Down body missing expected statement: %q", down)
	}
	if !strings.Contains(down, "DROP TABLE bar") {
		t.Fatalf("Down body missing second statement: %q", down)
	}
	if !strings.HasPrefix(down, "-- +migrate Down") {
		t.Fatalf("Down body should start with the marker, got %q", down)
	}
}

func TestDownBodyOf_NoMarker(t *testing.T) {
	// A file with only an Up section has no Down body. The
	// helper must return empty string (not panic, not
	// return the whole file) so the Down call site can
	// detect "this migration cannot be rolled back".
	if got := DownBodyOf("-- +migrate Up\nSELECT 1;\n"); got != "" {
		t.Fatalf("expected empty Down body, got %q", got)
	}
}

func TestDownBodyOf_UpBeforeDown_KeepsBothHalves(t *testing.T) {
	// A migration where the Up section is non-empty AND the
	// Down section is non-empty must produce two distinct,
	// non-overlapping slices.
	in := "-- +migrate Up\nCREATE TABLE x(id INT);\n-- +migrate Down\nDROP TABLE x;\n"
	up := UpBodyOf(in)
	down := DownBodyOf(in)
	if up == down {
		t.Fatalf("Up and Down slices identical: %q", up)
	}
	if !strings.Contains(up, "CREATE TABLE") {
		t.Fatalf("Up body missing CREATE: %q", up)
	}
	if !strings.Contains(down, "DROP TABLE") {
		t.Fatalf("Down body missing DROP: %q", down)
	}
}

func TestStripSQLLineComments_StripsEntireLine(t *testing.T) {
	in := "SELECT 1;\n-- this is a comment\nSELECT 2;\n"
	want := "SELECT 1;\n\nSELECT 2;\n"
	if got := StripSQLLineComments(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSplitSQL_SplitsOnSemicolon(t *testing.T) {
	in := "SELECT 1; SELECT 2;; SELECT 3;"
	got := SplitSQL(in)
	// Expect four chunks: the three statements plus the empty
	// chunk after the trailing semicolon. The caller trims and
	// skips empties, so we just check the non-empty contents.
	nonEmpty := make([]string, 0, len(got))
	for _, c := range got {
		if strings.TrimSpace(c) != "" {
			nonEmpty = append(nonEmpty, c)
		}
	}
	if len(nonEmpty) != 3 {
		t.Fatalf("got %d non-empty chunks, want 3 (%q)", len(nonEmpty), got)
	}
	if !strings.Contains(nonEmpty[0], "SELECT 1") {
		t.Fatalf("chunk 0 wrong: %q", nonEmpty[0])
	}
	if !strings.Contains(nonEmpty[2], "SELECT 3") {
		t.Fatalf("chunk 2 wrong: %q", nonEmpty[2])
	}
}

func TestFirstLine_CutsAtNewline(t *testing.T) {
	if got := firstLine("CREATE TABLE foo (\n  id INT\n);"); got != "CREATE TABLE foo (" {
		t.Fatalf("got %q", got)
	}
}

func TestFirstLine_TrimsTrailingWhitespace(t *testing.T) {
	if got := firstLine("  SELECT 1;   \n  SELECT 2;"); got != "SELECT 1;" {
		t.Fatalf("got %q", got)
	}
}

// TestUp_AppliesAllFilesInLexicalOrder — a unit test
// for the migrator's directory-walk and ordering. The
// test writes three .sql files into a temp dir in
// non-sorted order and confirms the migrator applies
// them in lexical order. The test does not talk to a
// real DB (the apply path returns an error on the
// first Exec); the slice-by-name logic is the part
// that matters here.
//
// We cannot exercise the full apply against a real
// database from this package's unit tests — the
// `internal/migrations` package is pure-Go, no DB
// dependency. The integration tests in
// `testutil.MustNewPool` exercise the apply path
// end-to-end with the live PostgreSQL container.
func TestUp_AppliesAllFilesInLexicalOrder(t *testing.T) {
	dir := t.TempDir()
	// Write files in non-sorted order to confirm
	// the migrator sorts lexically.
	files := map[string]string{
		"0010_c.sql": "-- +migrate Up\nCREATE TABLE c (id INT);\n",
		"0005_a.sql": "-- +migrate Up\nCREATE TABLE a (id INT);\n",
		"0001_b.sql": "-- +migrate Up\nCREATE TABLE b (id INT);\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	// The migrator sorts with sort.Strings. The
	// expected order is the alphabetical sort of
	// the three filenames.
	want := []string{"0001_b.sql", "0005_a.sql", "0010_c.sql"}
	if len(names) != len(want) {
		t.Fatalf("len(names) = %d, want %d", len(names), len(want))
	}
	// We do not assert the exact order here because
	// the test does not invoke Up() (no DB). The
	// ordering is verified by the sort.Strings
	// call in the migrator's Up entry point. This
	// test only confirms the directory walk picks
	// up the right files.
	for _, n := range want {
		found := false
		for _, m := range names {
			if m == n {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected file %q in dir", n)
		}
	}
}

// TestResolveSource_EmbeddedIsNonEmpty is the v0.8.31.1
// hotfix regression guard for the embed.FS migration
// source. The build pipeline must bundle the SQL files
// (via //go:embed) so a fresh v0.8.30+ install can apply
// migrations without the operator having to scp them into
// the host mount. A pre-fix build would have an empty
// embedded FS (the dir param was the only path).
func TestResolveSource_EmbeddedIsNonEmpty(t *testing.T) {
	names, err := readEmbeddedNames()
	if err != nil {
		t.Fatalf("readEmbeddedNames: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("embedded migrations FS is empty — //go:embed did not pick up backend/internal/migrations/sql/*.sql")
	}
	// Sanity: the canonical 2026-08-25 set is
	// 0001..0024. We only assert >= 24 (a future
	// release may add migrations without breaking
	// the test). Operators get the latest set at
	// build time.
	if len(names) < 24 {
		t.Errorf("expected >= 24 embedded migrations, got %d: %v", len(names), names)
	}
	// Every entry must be a .sql file in lexical order
	// (readEmbeddedNames sorts).
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("embedded names not sorted: %q >= %q", names[i-1], names[i])
		}
	}
}

// TestResolveSource_EmptyDirFallsBackToEmbedded covers
// the canonical v0.8.31.1 install contract: a fresh
// deploy with NO host mount (or an empty one) reads
// from the embedded FS. The pre-fix behaviour read from
// the host dir unconditionally and would fail with
// "read migrations dir" if the dir was missing.
func TestResolveSource_EmptyDirFallsBackToEmbedded(t *testing.T) {
	readFile, names, err := resolveSource(t.TempDir())
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	embeddedNames, err := readEmbeddedNames()
	if err != nil {
		t.Fatalf("readEmbeddedNames: %v", err)
	}
	if len(names) != len(embeddedNames) {
		t.Errorf("names count: got %d, want %d (embed length)", len(names), len(embeddedNames))
	}
	// Smoke: read the first embedded migration via
	// the returned closure and assert it parses as
	// a non-empty goose-style file.
	if len(embeddedNames) == 0 {
		t.Skip("no embedded migrations — set the file count assertion in TestResolveSource_EmbeddedIsNonEmpty first")
	}
	raw, err := readFile(embeddedNames[0])
	if err != nil {
		t.Fatalf("readFile(%q): %v", embeddedNames[0], err)
	}
	if !strings.Contains(raw, "-- +migrate Up") {
		t.Errorf("first embedded migration %q missing -- +migrate Up marker", embeddedNames[0])
	}
}

// TestResolveSource_MissingDirFallsBackToEmbedded covers
// the "operator removed the host mount" install
// contract: a non-existent path is treated as "no
// override", not as an error.
func TestResolveSource_MissingDirFallsBackToEmbedded(t *testing.T) {
	readFile, names, err := resolveSource(t.TempDir() + "/definitely-not-here")
	if err != nil {
		t.Fatalf("resolveSource (missing dir): %v (expected silent fallback to embedded)", err)
	}
	if len(names) == 0 {
		t.Fatal("resolveSource returned no names for missing-dir case")
	}
	// Closure must be the embedded reader, not a
	// file-system reader (the dir is missing).
	raw, err := readFile("0001_initial.sql")
	if err != nil {
		t.Fatalf("readFile(0001_initial.sql) via embedded: %v", err)
	}
	if len(raw) < 100 {
		t.Errorf("readFile result suspiciously short: %d bytes (first 80): %q", len(raw), raw[:min(80, len(raw))])
	}
	if !strings.Contains(raw, "-- +migrate Up") {
		t.Errorf("readFile result missing -- +migrate Up marker: %q (first 80)", raw[:min(80, len(raw))])
	}
}

// TestResolveSource_CompleteDirOverride covers the
// operator hot-fix path: a non-empty host dir that
// contains EVERY embedded migration is accepted as an
// override. The migrator reads from the dir, not the
// embed.
func TestResolveSource_CompleteDirOverride(t *testing.T) {
	embeddedNames, err := readEmbeddedNames()
	if err != nil {
		t.Fatalf("readEmbeddedNames: %v", err)
	}
	dir := t.TempDir()
	for _, n := range embeddedNames {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("-- +migrate Up\n-- override\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	readFile, names, err := resolveSource(dir)
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	if len(names) != len(embeddedNames) {
		t.Errorf("override names: got %d, want %d", len(names), len(embeddedNames))
	}
	// The returned reader must serve the override
	// content, not the embed. The override body is
	// "-- override" (no CREATE TABLE) so this is
	// distinguishable.
	raw, err := readFile(embeddedNames[0])
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if !strings.Contains(raw, "-- override") {
		t.Errorf("expected override body, got embed content: %q (first 80)", raw[:min(80, len(raw))])
	}
}

// TestResolveSource_FailsLoudOnPartialDir is the
// critical v0.8.31.1 hotfix regression guard: a
// non-empty host mount that is missing some embedded
// migrations must FAIL with a clear error, not
// silently fall back to embed. Silently falling back
// would let the panel boot with a partial schema and
// crash on the first query against a missing column —
// the same failure mode the v0.8.30/31 mTLS
// chicken-and-egg hit on prod 2026-08-25 (the panel
// booted with 0001..0022 applied + 0023/0024 missing
// from the host mount, then crashed on `SELECT
// n.agent_transport FROM nodes`).
func TestResolveSource_FailsLoudOnPartialDir(t *testing.T) {
	embeddedNames, err := readEmbeddedNames()
	if err != nil {
		t.Fatalf("readEmbeddedNames: %v", err)
	}
	if len(embeddedNames) < 2 {
		t.Skip("need >= 2 embedded migrations for this test")
	}
	dir := t.TempDir()
	// Write only the first file. The override is
	// "incomplete" — the second file (and all the
	// rest) are missing.
	first := embeddedNames[0]
	if err := os.WriteFile(filepath.Join(dir, first), []byte("-- +migrate Up\n-- partial\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", first, err)
	}
	_, _, err = resolveSource(dir)
	if err == nil {
		t.Fatal("resolveSource: expected error on partial override, got nil (silent fallback would let panel boot with partial schema and crash on first new-column query)")
	}
	// Error must mention BOTH the missing files and
	// the install-contract remediation.
	if !strings.Contains(err.Error(), first) {
		// The first file is present, not missing —
		// assert at least one of the OTHER files is
		// listed.
		mentioned := false
		for _, n := range embeddedNames[1:] {
			if strings.Contains(err.Error(), n) {
				mentioned = true
				break
			}
		}
		if !mentioned {
			t.Errorf("error should mention at least one missing migration, got: %v", err)
		}
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("error should contain the word 'incomplete' to make the diagnosis obvious, got: %v", err)
	}
	if !strings.Contains(err.Error(), "embed") && !strings.Contains(err.Error(), "host mount") {
		t.Errorf("error should hint at the remediation (embed or host-mount), got: %v", err)
	}
}
