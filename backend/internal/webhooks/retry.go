// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Exponential-backoff retry schedule for the
// outgoing-webhook dispatcher. The schedule is a
// fixed list of inter-attempt intervals (1s, 5s,
// 25s, 2m15s, 11m15s) that grows by a factor of
// five. After the 5th retry fails, the delivery
// is moved to the DLQ.
//
// # Why a fixed schedule
//
// The doc.go contract is "1s, 5s, 25s, 2m15s,
// 11m15s with a hard ceiling of 24h". A
// formula-driven backoff would change the schedule
// silently if someone tweaked the factor. The fixed
// list makes the contract explicit and lets
// receiver-side debugging match "5th retry" to a
// known wall-clock interval.
//
// # Wall-clock budget
//
// Sum of the five intervals: 1 + 5 + 25 + 135 +
// 675 = 841 seconds (~14 minutes). The 24h ceiling
// is far above this and is the documented upper
// bound for a future "more aggressive retry" knob.
// v0.7.0 does not expose the knob; v0.7.x will
// add it behind an AEGIS_WEBHOOKS_MAX_RETRY_SEC
// config.

package webhooks

import "time"

// retryIntervals is the inter-attempt delay
// schedule. retryIntervals[0] is the wait between
// attempt 1 and attempt 2; retryIntervals[4] is the
// wait between attempt 5 and attempt 6. After
// attempt 6 fails, the delivery goes to the DLQ.
//
// The schedule is exported as a function (not a
// var) so tests cannot mutate it by accident.
func retryIntervals() []time.Duration {
	return []time.Duration{
		1 * time.Second,
		5 * time.Second,
		25 * time.Second,
		2*time.Minute + 15*time.Second,
		11*time.Minute + 15*time.Second,
	}
}

// MaxAttempts is the total number of POST attempts
// the dispatcher makes for a single delivery
// (1 initial + 5 retries = 6). After the 6th
// attempt fails, the delivery is moved to the DLQ.
const MaxAttempts = 6

// NextAttemptDelay returns the wait between the
// given attempt number (1-indexed) and the next
// one. Returns 0 if there is no next attempt
// (i.e. the given attempt is the last one and the
// caller should move to DLQ on the next failure).
//
// # Panics
//
// The function does not panic on any input; an
// attempt <= 0 or > MaxAttempts returns 0
// defensively so a buggy caller cannot block the
// dispatcher on a non-existent interval.
func NextAttemptDelay(attempt int) time.Duration {
	if attempt < 1 || attempt >= MaxAttempts {
		return 0
	}
	intervals := retryIntervals()
	// attempt=1 -> intervals[0] (1s)
	// attempt=5 -> intervals[4] (11m15s)
	// attempt=6 -> 0 (no more retries)
	return intervals[attempt-1]
}

// TotalRetryBudget returns the sum of all the
// intervals. Useful for log messages and for the
// admin UI ("this endpoint has been retrying for
// 14 minutes").
func TotalRetryBudget() time.Duration {
	intervals := retryIntervals()
	var total time.Duration
	for _, d := range intervals {
		total += d
	}
	return total
}
