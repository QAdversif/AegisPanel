// SPDX-License-Identifier: AGPL-3.0-or-later

package cores

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// flushRecorder is a test helper that records every flush
// the BatchedApplier made, and the result the FlushFn
// returned. The optional failFirst / alwaysFail knobs let
// tests simulate FlushFn errors.
type flushRecorder struct {
	mu         sync.Mutex
	calls      [][]Delta
	failFirst  int32 // fail the first N calls with errFlushFailed
	alwaysFail bool  // fail every call
}

var errFlushFailed = errors.New("simulated flush failure")

func (r *flushRecorder) flush(ctx context.Context, deltas []Delta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]Delta{}, deltas...))
	// Simulate failure if configured.
	if r.alwaysFail {
		return errFlushFailed
	}
	if atomic.LoadInt32(&r.failFirst) > 0 {
		atomic.AddInt32(&r.failFirst, -1)
		return errFlushFailed
	}
	return nil
}

func (r *flushRecorder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// helper to build a Delta with minimal fields.
func mkDelta(kind DeltaKind, userID uuid.UUID) Delta {
	return Delta{
		Kind:     kind,
		UserID:   userID,
		Payload:  json.RawMessage(`{}`),
		Enqueued: time.Now(),
	}
}

// TestBatchedApplier_CoalescesDeltasInWindow verifies that
// N deltas enqueued inside the same window collapse into a
// single flush call.
func TestBatchedApplier_CoalescesDeltasInWindow(t *testing.T) {
	rec := &flushRecorder{}
	window := 100 * time.Millisecond
	app := NewBatchedApplier(window, 100, rec.flush)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = app.Run(ctx) }()

	// Push 5 deltas for 5 different users inside the
	// window. None of them should trigger an immediate
	// flush (maxQueue is 100).
	for i := 0; i < 5; i++ {
		app.Enqueue(mkDelta(DeltaAddUser, uuid.New()))
	}
	// No flush yet — wait briefly to let the channel drain
	// into the pending set.
	time.Sleep(window / 2)
	if got := rec.callCount(); got != 0 {
		t.Fatalf("callCount after enqueue = %d, want 0 (still inside window)", got)
	}
	// Wait for the next ticker tick. The window is 100ms,
	// so we wait up to 2x.
	deadline := time.Now().Add(2 * window)
	for time.Now().Before(deadline) && rec.callCount() == 0 {
		time.Sleep(window / 10)
	}
	if got := rec.callCount(); got != 1 {
		t.Fatalf("callCount after window = %d, want 1", got)
	}
	if got := len(rec.calls[0]); got != 5 {
		t.Fatalf("flush size = %d, want 5", got)
	}
}

// TestBatchedApplier_CancelReplace verifies the §7.5
// AddUser + RemoveUser same user = no-op semantics.
func TestBatchedApplier_CancelReplace(t *testing.T) {
	rec := &flushRecorder{}
	window := 50 * time.Millisecond
	app := NewBatchedApplier(window, 100, rec.flush)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = app.Run(ctx) }()

	u1 := uuid.New()
	u2 := uuid.New()

	// u1: AddUser then RemoveUser → both cancel out, no
	// pending entry.
	app.Enqueue(mkDelta(DeltaAddUser, u1))
	app.Enqueue(mkDelta(DeltaRemoveUser, u1))

	// u2: AddUser then AddUser (same kind) → 1 pending
	// entry, last-write-wins (still AddUser).
	app.Enqueue(mkDelta(DeltaAddUser, u2))
	app.Enqueue(mkDelta(DeltaAddUser, u2))

	// u3 (added later for control): just AddUser → 1
	// pending entry.
	u3 := uuid.New()
	app.Enqueue(mkDelta(DeltaAddUser, u3))

	// Let the run loop absorb everything.
	time.Sleep(window / 2)
	pending := app.Pending()
	if len(pending) != 2 {
		t.Fatalf("pending size after cancel/replace = %d, want 2 (u1 cancelled, u2 collapsed, u3 added)", len(pending))
	}

	// Wait for the next tick.
	deadline := time.Now().Add(2 * window)
	for time.Now().Before(deadline) && rec.callCount() == 0 {
		time.Sleep(window / 10)
	}
	if got := rec.callCount(); got != 1 {
		t.Fatalf("callCount = %d, want 1", got)
	}
	if got := len(rec.calls[0]); got != 2 {
		t.Fatalf("flush size = %d, want 2", got)
	}
}

// TestBatchedApplier_MaxQueueTriggersImmediateFlush verifies
// that exceeding maxQueue fires the FlushFn right away
// (without waiting for the next ticker tick).
func TestBatchedApplier_MaxQueueTriggersImmediateFlush(t *testing.T) {
	rec := &flushRecorder{}
	window := 1 * time.Second // long enough that the flush MUST come from maxQueue
	app := NewBatchedApplier(window, 10, rec.flush)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = app.Run(ctx) }()

	// Push 10 deltas — that hits maxQueue=10 and should
	// trigger an immediate flush.
	for i := 0; i < 10; i++ {
		app.Enqueue(mkDelta(DeltaAddUser, uuid.New()))
	}

	// Poll for the flush; should be near-instant since the
	// run loop checks pendingLen on every absorb.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && rec.callCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := rec.callCount(); got != 1 {
		t.Fatalf("callCount after maxQueue hit = %d, want 1 (immediate flush)", got)
	}
}

// TestBatchedApplier_FlushErrorDoesNotCrashLoop verifies
// that a FlushFn returning an error is logged but the run
// loop continues to the next window.
func TestBatchedApplier_FlushErrorDoesNotCrashLoop(t *testing.T) {
	rec := &flushRecorder{failFirst: 1} // first call fails
	window := 50 * time.Millisecond
	app := NewBatchedApplier(window, 100, rec.flush)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = app.Run(ctx) }()

	app.Enqueue(mkDelta(DeltaAddUser, uuid.New()))
	time.Sleep(window / 2)

	// First tick → FlushFn returns errFlushFailed → logged.
	// Loop must continue.
	deadline := time.Now().Add(2 * window)
	for time.Now().Before(deadline) && rec.callCount() < 1 {
		time.Sleep(window / 10)
	}
	// Push another delta so the second tick has something
	// to flush; the second flushFn call must succeed.
	time.Sleep(window)
	app.Enqueue(mkDelta(DeltaAddUser, uuid.New()))
	deadline = time.Now().Add(3 * window)
	for time.Now().Before(deadline) && rec.callCount() < 2 {
		time.Sleep(window / 10)
	}
	if got := rec.callCount(); got < 2 {
		t.Fatalf("callCount after error recovery = %d, want >= 2 (loop crashed?)", got)
	}
}

// TestBatchedApplier_GracefulShutdownDrains verifies that
// cancelling the parent context returns ctx.Err() and that
// no flush is invoked during shutdown (drain is best-effort
// and does not flush).
func TestBatchedApplier_GracefulShutdownDrains(t *testing.T) {
	rec := &flushRecorder{}
	window := 1 * time.Hour // long enough that no tick fires during the test
	app := NewBatchedApplier(window, 100, rec.flush)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	app.Enqueue(mkDelta(DeltaAddUser, uuid.New()))
	// Give the run loop a moment to absorb the delta.
	time.Sleep(50 * time.Millisecond)
	if got := rec.callCount(); got != 0 {
		t.Fatalf("callCount before cancel = %d, want 0 (window is 1h)", got)
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx.Cancel")
	}
	if got := rec.callCount(); got != 0 {
		t.Fatalf("callCount after cancel = %d, want 0 (drain does not flush)", got)
	}
}
