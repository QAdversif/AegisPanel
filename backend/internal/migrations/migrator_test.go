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
