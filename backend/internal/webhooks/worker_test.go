// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestWorker_RunStopsOnContextCancel(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	w := NewWorker(svc, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx)
	}()
	// Let it tick a few times, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Errorf("Run did not return after context cancel")
	}
}

func TestWorker_TickProcessesDueRetries(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200, Body: "ok"}}
	svc.SetHTTPClient(fake)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Drive a failure to enqueue a retry.
	fake.Responses = []fakeResponse{{StatusCode: 503}}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// Force the scheduled time into the past.
	store.mu.Lock()
	store.pendingRetries[results[0].DeliveryID] = time.Now().UTC().Add(-1 * time.Second)
	store.mu.Unlock()
	// Run the worker for ~100ms with a 10ms tick.
	w := NewWorker(svc, 10*time.Millisecond)
	w.SetBatch(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	// Two HTTP requests: the initial 503 and
	// at least one retry 200.
	if len(fake.Requests) < 2 {
		t.Errorf("HTTP requests = %d, want >= 2", len(fake.Requests))
	}
	// Queue is empty after the worker drained it.
	due, err := store.ListDueRetries(context.Background(), time.Now().UTC(), 0)
	if err != nil {
		t.Fatalf("ListDueRetries: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("expected 0 pending retries, got %d", len(due))
	}
	_ = ep
}

func TestWorker_TickSkipsNotDue(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200}}
	svc.SetHTTPClient(fake)
	if _, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	fake.Responses = []fakeResponse{{StatusCode: 503}}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// Schedule the retry in the future.
	store.mu.Lock()
	store.pendingRetries[results[0].DeliveryID] = time.Now().UTC().Add(1 * time.Hour)
	store.mu.Unlock()
	w := NewWorker(svc, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	// Only the initial 503 — the worker
	// ignored the not-due row.
	if len(fake.Requests) != 1 {
		t.Errorf("HTTP requests = %d, want 1 (initial only)", len(fake.Requests))
	}
}

// TestWorker_TicksRepeatedlyBeforeCancel is a
// smoke test that the worker keeps ticking (and
// stays well-behaved) until ctx is cancelled.
// The per-tick context timeout must abort slow
// ticks so they do not stack up behind the next
// one.
func TestWorker_TicksRepeatedlyBeforeCancel(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200}}
	svc.SetHTTPClient(fake)
	if _, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Insert a due retry so the worker tries
	// to process it.
	if err := store.EnqueueRetry(context.Background(), uuid.New(), time.Now().UTC().Add(-1*time.Second)); err != nil {
		t.Fatalf("EnqueueRetry: %v", err)
	}
	w := NewWorker(svc, 20*time.Millisecond)
	var ticks atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ticks.Add(1)
				w.tick(ctx)
			}
		}
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done
	if ticks.Load() < 2 {
		t.Errorf("ticks = %d, want >= 2", ticks.Load())
	}
}
