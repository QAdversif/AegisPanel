// SPDX-License-Identifier: AGPL-3.0-or-later

package backups

import (
	"testing"
	"time"
)

func TestParseCron_Valid(t *testing.T) {
	cases := []struct {
		expr  string
		match time.Time
		no    time.Time
	}{
		{
			"0 2 * * *",
			time.Date(2026, 3, 15, 2, 0, 0, 0, time.UTC),
			time.Date(2026, 3, 15, 3, 0, 0, 0, time.UTC),
		},
		{
			"30 14 * * 1",
			time.Date(2026, 3, 16, 14, 30, 0, 0, time.UTC), // Monday
			time.Date(2026, 3, 17, 14, 30, 0, 0, time.UTC), // Tuesday
		},
		{
			"0 0 1 1 *",
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			c, err := ParseCron(tc.expr)
			if err != nil {
				t.Fatalf("ParseCron(%q): %v", tc.expr, err)
			}
			if !c.matches(tc.match) {
				t.Errorf("%q did not match %s", tc.expr, tc.match)
			}
			if c.matches(tc.no) {
				t.Errorf("%q unexpectedly matched %s", tc.expr, tc.no)
			}
		})
	}
}

func TestParseCron_Invalid(t *testing.T) {
	bad := []string{
		"",
		"* * * *",     // 4 fields
		"* * * * * *", // 6 fields
		"60 0 * * *",  // minute out of range
		"0 24 * * *",  // hour out of range
		"0 0 32 * *",  // dom out of range
		"0 0 0 13 *",  // month out of range
		"abc 0 * * *", // non-integer
	}
	for _, expr := range bad {
		t.Run(expr, func(t *testing.T) {
			if _, err := ParseCron(expr); err == nil {
				t.Fatalf("ParseCron(%q): expected error, got nil", expr)
			}
		})
	}
}

// TestParseCron_Step verifies the Vixie `*/N` syntax
// across the three field types where it is most
// common: minute, hour, and day-of-month.
func TestParseCron_Step(t *testing.T) {
	t.Run("*/15 minute", func(t *testing.T) {
		got, err := parseCronField("*/15", 0, 59)
		if err != nil {
			t.Fatalf("parseCronField: %v", err)
		}
		want := []int{0, 15, 30, 45}
		if !slicesEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("*/6 hour", func(t *testing.T) {
		got, err := parseCronField("*/6", 0, 23)
		if err != nil {
			t.Fatalf("parseCronField: %v", err)
		}
		want := []int{0, 6, 12, 18}
		if !slicesEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("*/2 dom", func(t *testing.T) {
		// 1..31 stepping by 2 → 16 elements:
		// 1, 3, 5, 7, 9, 11, 13, 15, 17, 19, 21, 23,
		// 25, 27, 29, 31
		got, err := parseCronField("*/2", 1, 31)
		if err != nil {
			t.Fatalf("parseCronField: %v", err)
		}
		if len(got) != 16 {
			t.Fatalf("got %d elements (%v), want 16", len(got), got)
		}
		if got[0] != 1 {
			t.Errorf("first element = %d, want 1", got[0])
		}
		if got[len(got)-1] != 31 {
			t.Errorf("last element = %d, want 31", got[len(got)-1])
		}
	})
}

// TestParseCron_Range verifies the `N-M` range
// syntax across the three field types where it is
// most common: hour (business hours), dom (week
// ranges), and a 24-hour wrap.
func TestParseCron_Range(t *testing.T) {
	t.Run("1-5", func(t *testing.T) {
		got, err := parseCronField("1-5", 1, 12)
		if err != nil {
			t.Fatalf("parseCronField: %v", err)
		}
		want := []int{1, 2, 3, 4, 5}
		if !slicesEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("0-23", func(t *testing.T) {
		// Full hour range — must produce 24 elements.
		got, err := parseCronField("0-23", 0, 23)
		if err != nil {
			t.Fatalf("parseCronField: %v", err)
		}
		if len(got) != 24 {
			t.Fatalf("got %d elements, want 24", len(got))
		}
		if got[0] != 0 || got[23] != 23 {
			t.Errorf("got [%d, ..., %d], want [0, ..., 23]", got[0], got[23])
		}
	})
	t.Run("9-17", func(t *testing.T) {
		got, err := parseCronField("9-17", 0, 23)
		if err != nil {
			t.Fatalf("parseCronField: %v", err)
		}
		want := []int{9, 10, 11, 12, 13, 14, 15, 16, 17}
		if !slicesEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// TestParseCron_List verifies the comma-separated
// `N,M,K` list syntax.
func TestParseCron_List(t *testing.T) {
	t.Run("0,15,30,45", func(t *testing.T) {
		got, err := parseCronField("0,15,30,45", 0, 59)
		if err != nil {
			t.Fatalf("parseCronField: %v", err)
		}
		want := []int{0, 15, 30, 45}
		if !slicesEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("1,3,5", func(t *testing.T) {
		got, err := parseCronField("1,3,5", 1, 12)
		if err != nil {
			t.Fatalf("parseCronField: %v", err)
		}
		want := []int{1, 3, 5}
		if !slicesEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("0,30", func(t *testing.T) {
		got, err := parseCronField("0,30", 0, 59)
		if err != nil {
			t.Fatalf("parseCronField: %v", err)
		}
		want := []int{0, 30}
		if !slicesEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// TestParseCron_RangeWithStep verifies the
// `N-M/S` range-with-step syntax. The result
// must be the range [N, M] walked at step S,
// inclusive of the boundary.
func TestParseCron_RangeWithStep(t *testing.T) {
	t.Run("1-31/2", func(t *testing.T) {
		// Odd days of the month: 1, 3, 5, ..., 31
		// = 16 elements.
		got, err := parseCronField("1-31/2", 1, 31)
		if err != nil {
			t.Fatalf("parseCronField: %v", err)
		}
		if len(got) != 16 {
			t.Fatalf("got %d elements (%v), want 16", len(got), got)
		}
		if got[0] != 1 {
			t.Errorf("first element = %d, want 1", got[0])
		}
		if got[len(got)-1] != 31 {
			t.Errorf("last element = %d, want 31", got[len(got)-1])
		}
	})
	t.Run("0-59/15", func(t *testing.T) {
		// Identical to */15.
		got, err := parseCronField("0-59/15", 0, 59)
		if err != nil {
			t.Fatalf("parseCronField: %v", err)
		}
		want := []int{0, 15, 30, 45}
		if !slicesEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// TestParseCron_InvalidSyntax verifies that
// malformed step / range / list expressions are
// rejected at boot with a clear error.
func TestParseCron_InvalidSyntax(t *testing.T) {
	bad := []string{
		"*/",    // empty step
		"*/0",   // zero step
		"1-",    // empty range end
		"-5",    // empty range start
		"1-5-9", // triple-dash (parse error)
		"1,2,",  // trailing comma
		"*/abc", // non-numeric step
		"60",    // out of range (minute 0-59)
		"1-60",  // range end out of range
	}
	for _, expr := range bad {
		t.Run(expr, func(t *testing.T) {
			_, err := ParseCron("0 " + expr + " * * *")
			if err == nil {
				t.Errorf("expected error for %q, got nil", expr)
			}
		})
	}
}

// slicesEqual is a small helper used by the
// ParseCron tests; kept here (not a top-level
// util) so the tests stay self-contained.
func slicesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNewBackupIDFormat(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	id := newBackupID(now)
	if len(id) < 10 || id[:4] != "bck_" {
		t.Fatalf("id = %q, want prefix bck_ and length >= 10", id)
	}
	// Two calls produce different IDs (random tail).
	other := newBackupID(now)
	if id == other {
		t.Fatalf("expected different IDs, got %q twice", id)
	}
}
