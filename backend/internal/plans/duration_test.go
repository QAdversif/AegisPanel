// SPDX-License-Identifier: AGPL-3.0-or-later

package plans

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestDurationMicrosecondsRoundTrip covers the
// encode path: time.Duration -> int64 microseconds
// -> Postgres INTERVAL -> pgtype.Interval ->
// time.Duration. The local helper only owns the
// two ends (encode to microseconds, decode from
// pgtype.Interval); the middle is Postgres itself.
//
// The round-trip properties the package relies on:
//
//   - sub-microsecond precision is lost (Postgres
//     INTERVAL is microsecond-precision);
//   - whole milliseconds, seconds, minutes, hours,
//     days survive a round-trip exactly;
//   - 30-day "month" units round-trip back as 30
//     days (the documented month-as-30-days
//     behaviour).
func TestDurationMicrosecondsRoundTrip(t *testing.T) {
	cases := []time.Duration{
		0,
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
		micros := durationToMicroseconds(d)
		// The Go side never sees the int64 microseconds
		// as an INTERVAL — Postgres does. The
		// round-trip is "we hand microseconds to
		// Postgres, Postgres stores the value as
		// Days/Microseconds components, pgx scans it
		// back as a pgtype.Interval, we call
		// intervalToDuration". Simulate that locally:
		const microsPerDay = int64(24 * 3600 * 1_000_000) // 86_400_000_000
		iv := pgtype.Interval{
			Days:         int32(micros / microsPerDay),
			Microseconds: micros % microsPerDay,
			Valid:        true,
		}
		got := intervalToDuration(iv)
		// The original Duration is exact; the
		// reconstructed Duration may lose sub-microsecond
		// nanoseconds. Truncate both sides to
		// microseconds and compare.
		want := (d / time.Microsecond) * time.Microsecond
		if got != want {
			t.Errorf("Duration %s -> micros %d -> Interval %+v -> Duration %s; want %s",
				d, micros, iv, got, want)
		}
	}
}

// TestIntervalMonthsMapTo30Days covers the
// "months decode as 30 days" policy. An
// interval with N months round-trips to N*30
// days, not to N*28/29/30/31.
func TestIntervalMonthsMapTo30Days(t *testing.T) {
	iv := pgtype.Interval{Months: 1, Valid: true}
	got := intervalToDuration(iv)
	want := 30 * 24 * time.Hour
	if got != want {
		t.Errorf("1 month = %s, want %s", got, want)
	}
	iv = pgtype.Interval{Months: 3, Days: 5, Valid: true}
	got = intervalToDuration(iv)
	want = 3*30*24*time.Hour + 5*24*time.Hour
	if got != want {
		t.Errorf("3 months + 5 days = %s, want %s", got, want)
	}
}
