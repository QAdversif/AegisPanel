// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestLimiter returns a Limiter with a controllable
// clock. The clock starts at t0 and advances by
// `step` on every call to now().
func newTestLimiter(t *testing.T, rate, burst float64, idle time.Duration) (*Limiter, func(time.Duration)) {
	t.Helper()
	var nowNs atomic.Int64
	nowNs.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	l := New(rate, burst, idle)
	l.SetClock(func() time.Time {
		return time.Unix(0, nowNs.Load())
	})
	step := func(d time.Duration) {
		nowNs.Add(d.Nanoseconds())
	}
	return l, step
}

func TestLimiter_FirstBurstAllowed(t *testing.T) {
	l, _ := newTestLimiter(t, 1, 5, 0)
	// A brand-new key gets the full burst budget on
	// first contact.
	for i := 0; i < 5; i++ {
		ok, _ := l.Allow("alice")
		if !ok {
			t.Fatalf("request %d should be allowed (burst=5)", i+1)
		}
	}
	// The 6th call within the same second is rejected.
	ok, retry := l.Allow("alice")
	if ok {
		t.Fatal("6th request should be rejected (burst=5, rps=1)")
	}
	if retry <= 0 {
		t.Fatalf("retryAfter = %v, want > 0", retry)
	}
	// One second later we get one more token.
	l.SetClock(func() time.Time { return time.Unix(0, time.Date(2026, 1, 1, 0, 0, 6, 0, time.UTC).UnixNano()) })
	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("request after 1s should be allowed (rps=1)")
	}
}

func TestLimiter_DisabledAllowsAll(t *testing.T) {
	l, _ := newTestLimiter(t, 0, 0, 0) // rps=0 = disabled
	for i := 0; i < 100; i++ {
		if ok, _ := l.Allow("alice"); !ok {
			t.Fatalf("disabled limiter must allow all; request %d was rejected", i+1)
		}
	}
}

func TestLimiter_NilReceiverIsSafe(t *testing.T) {
	var l *Limiter
	ok, retry := l.Allow("alice")
	if !ok {
		t.Fatal("nil receiver must default to allow")
	}
	if retry != 0 {
		t.Fatalf("nil receiver retry = %v, want 0", retry)
	}
}

func TestLimiter_KeysAreIsolated(t *testing.T) {
	l, _ := newTestLimiter(t, 1, 2, 0)
	// Two keys, each gets its own bucket.
	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("alice request 1 should be allowed")
	}
	if ok, _ := l.Allow("bob"); !ok {
		t.Fatal("bob request 1 should be allowed")
	}
	// alice drains her bucket (1.5 left). bob is
	// untouched.
	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("alice request 2 should be allowed (burst=2)")
	}
	// Both keys have now used 2 tokens worth of
	// capacity. The next request for either must be
	// rejected. (alice is at 0.5 tokens; bob is at
	// 1.5; the third request from either drains
	// the bucket to <1.)
	if ok, _ := l.Allow("alice"); ok {
		t.Fatal("alice request 3 should be rejected")
	}
	if ok, _ := l.Allow("bob"); !ok {
		t.Fatal("bob request 2 should be allowed (1.5 tokens left)")
	}
	if ok, _ := l.Allow("bob"); ok {
		t.Fatal("bob request 3 should be rejected (only 0.5 tokens left)")
	}
}

func TestLimiter_RetryAfterIsAccurate(t *testing.T) {
	l, clock := newTestLimiter(t, 2, 2, 0)
	// Drain the bucket.
	l.Allow("alice")
	l.Allow("alice")
	ok, retry := l.Allow("alice")
	if ok {
		t.Fatal("expected 3rd request to be rejected")
	}
	// rps=2 -> one token every 500ms. The 3rd
	// request was denied with an empty bucket, so
	// the next token is half a second away.
	if retry < 400*time.Millisecond || retry > 600*time.Millisecond {
		t.Fatalf("retryAfter = %v, want ~500ms", retry)
	}
	// Advance the clock by 500ms; the next call must
	// succeed.
	clock(500 * time.Millisecond)
	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("expected request after 500ms to be allowed")
	}
}

func TestLimiter_IdleRefillResetsBucket(t *testing.T) {
	l, clock := newTestLimiter(t, 1, 1, 100*time.Millisecond)
	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("first request should be allowed")
	}
	if ok, _ := l.Allow("alice"); ok {
		t.Fatal("second request within 100ms should be rejected")
	}
	// Sleep longer than `idle`; the bucket should
	// be fully refilled.
	clock(200 * time.Millisecond)
	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("request after idle-refill should be allowed")
	}
}

func TestLimiter_MaxKeysEvictsOldest(t *testing.T) {
	l, clock := newTestLimiter(t, 1, 1, 0)
	l.SetMaxKeys(2)
	// Insert two keys; both are recent.
	l.Allow("alice")
	clock(time.Millisecond)
	l.Allow("bob")
	clock(time.Millisecond)
	if got := l.Size(); got != 2 {
		t.Fatalf("Size = %d, want 2", got)
	}
	// Insert a third key: should evict the oldest
	// (alice, who last saw traffic 2ms ago).
	clock(time.Millisecond)
	l.Allow("charlie")
	if got := l.Size(); got != 2 {
		t.Fatalf("Size after eviction = %d, want 2 (max=2)", got)
	}
	// alice's bucket is gone. A request under her
	// key starts fresh.
	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("alice's first request after eviction should be allowed")
	}
}

func TestLimiter_ConcurrentSafe(t *testing.T) {
	l := New(100, 100, 0) // 100 RPS, burst 100
	const goroutines = 50
	const callsPerGoroutine = 200
	var wg sync.WaitGroup
	var allowed atomic.Int64
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < callsPerGoroutine; j++ {
				if ok, _ := l.Allow("shared"); ok {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	// 50 goroutines * 200 = 10000 calls. With
	// rps=100, the limit is 100/sec; the test runs
	// in microseconds so the vast majority of the
	// 10000 calls must be rejected (the initial
	// burst=100 is the only "free" budget).
	got := allowed.Load()
	if got < 80 || got > 200 {
		t.Fatalf("allowed = %d, want roughly 100 (initial burst) for a fast run", got)
	}
}
