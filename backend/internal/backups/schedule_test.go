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
		"*/2 0 * * *", // step syntax not supported
		"0 0 1-5 * *", // range syntax not supported
	}
	for _, expr := range bad {
		t.Run(expr, func(t *testing.T) {
			if _, err := ParseCron(expr); err == nil {
				t.Fatalf("ParseCron(%q): expected error, got nil", expr)
			}
		})
	}
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
