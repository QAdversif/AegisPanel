// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"testing"
	"time"
)

func TestNextAttemptDelay_Schedule(t *testing.T) {
	t.Parallel()
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 5 * time.Second},
		{3, 25 * time.Second},
		{4, 2*time.Minute + 15*time.Second},
		{5, 11*time.Minute + 15*time.Second},
		{6, 0}, // no more retries
	}
	for _, tt := range tests {
		got := NextAttemptDelay(tt.attempt)
		if got != tt.want {
			t.Errorf("NextAttemptDelay(%d) = %s, want %s", tt.attempt, got, tt.want)
		}
	}
}

func TestNextAttemptDelay_OutOfRange(t *testing.T) {
	t.Parallel()
	// attempt <= 0 or > MaxAttempts returns 0
	// (defensive; a buggy caller should not
	// block on a non-existent interval).
	if d := NextAttemptDelay(0); d != 0 {
		t.Errorf("NextAttemptDelay(0) = %s, want 0", d)
	}
	if d := NextAttemptDelay(-1); d != 0 {
		t.Errorf("NextAttemptDelay(-1) = %s, want 0", d)
	}
	if d := NextAttemptDelay(MaxAttempts + 1); d != 0 {
		t.Errorf("NextAttemptDelay(MaxAttempts+1) = %s, want 0", d)
	}
}

func TestTotalRetryBudget(t *testing.T) {
	t.Parallel()
	// 1 + 5 + 25 + 135 + 675 = 841 seconds.
	want := 841 * time.Second
	if got := TotalRetryBudget(); got != want {
		t.Errorf("TotalRetryBudget() = %s, want %s", got, want)
	}
}

func TestMaxAttempts_ConsistentWithSchedule(t *testing.T) {
	t.Parallel()
	// MaxAttempts must equal len(schedule) + 1
	// (1 initial attempt + N retries).
	intervals := retryIntervals()
	if MaxAttempts != len(intervals)+1 {
		t.Errorf("MaxAttempts = %d, want %d (one initial + %d retries)",
			MaxAttempts, len(intervals)+1, len(intervals))
	}
}
