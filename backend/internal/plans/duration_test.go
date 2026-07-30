// SPDX-License-Identifier: AGPL-3.0-or-later

package plans

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestDurationIntervalRoundTrip covers the
// day-precision encode / decode path. The
// Service stores Duration as a time.Duration
// (nanoseconds); the pgx layer encodes / decodes
// this as a pgtype.Interval. The round-trip
// properties the package relies on:
//
//   - whole days survive a round-trip exactly;
//   - sub-day remainder (hours, minutes, seconds,
//     microseconds) survives down to microsecond
//     precision;
//   - 30-day "month" units round-trip back as 30
//     days (the documented month-as-30-days
//     behaviour).
func TestDurationIntervalRoundTrip(t *testing.T) {
	cases := []time.Duration{
		0,
		1 * time.Nanosecond,
		1 * time.Microsecond,
		1 * time.Millisecond,
		1 * time.Second,
		1 * time.Minute,
		1 * time.Hour,
		24 * time.Hour,
		36 * time.Hour, // 1 day + 12h
		30 * 24 * time.Hour,
		90 * 24 * time.Hour,
		365 * 24 * time.Hour,
	}
	for _, d := range cases {
		iv := durationToInterval(d)
		got := intervalToDuration(iv)
		// Sub-microsecond precision is lost; the
		// encode path drops sub-microsecond
		// nanoseconds. Round to microsecond and
		// compare.
		want := (d / time.Microsecond) * time.Microsecond
		if got != want {
			t.Errorf("Duration %s -> Interval %+v -> Duration %s; want %s",
				d, iv, got, want)
		}
	}
}

// TestIntervalMonthsMapTo30Days covers the
// "months decode as 30 days" policy. An
// interval with N months round-trips to N*30
// days, not to N*28/29/30/31.
func TestIntervalMonthsMapTo30Days(t *testing.T) {
	iv := pgtype.Interval{Months: 1}
	got := intervalToDuration(iv)
	want := 30 * 24 * time.Hour
	if got != want {
		t.Errorf("1 month = %s, want %s", got, want)
	}
	iv = pgtype.Interval{Months: 3, Days: 5}
	got = intervalToDuration(iv)
	want = 3*30*24*time.Hour + 5*24*time.Hour
	if got != want {
		t.Errorf("3 months + 5 days = %s, want %s", got, want)
	}
}
