// SPDX-License-Identifier: AGPL-3.0-or-later

package migrations

import (
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
