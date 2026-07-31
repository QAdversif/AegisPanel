// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Worker is the v0.7.x background goroutine that
// fires retries on the schedule the dispatcher set
// when the original attempt failed. v0.7.0 records
// every POST attempt in `webhook_deliveries` and,
// on a non-final failure, returns
// `DeliveryStatusRetry` — but the next attempt has
// to be triggered out-of-band. v0.7.x adds this
// worker to remove the manual `POST /webhooks/
// deliveries/{id}/retry` step on the happy path.
//
// # How it works
//
// On every tick (default 5s) the worker calls
// `Service.ProcessDueRetries`, which:
//
//  1. Reads up to `batch` (default 100) rows
//     from `webhook_pending_retries` whose
//     `next_attempt_at <= now`, ordered by
//     `next_attempt_at` asc.
//  2. For each id, calls `Service.RetryDelivery`
//     which fires the next attempt and removes
//     the OLD retry row. The new attempt's
//     `deliverSync` (or `recordFailure`)
//     re-enqueues itself on its own failure.
//
// # Graceful shutdown
//
// `Run` returns when `ctx` is cancelled. The
// caller (cmd/aegis/main.go) cancels the context
// on SIGINT/SIGTERM via signal.NotifyContext.
// A single tick in flight is allowed to finish
// before Run returns (the per-tick context is
// derived from the outer ctx but bounded to the
// interval so a slow tick never stacks up).
//
// # Single-replica design
//
// v0.7.x targets a single-replica panel. Two
// replicas of the worker would race on the
// `webhook_pending_retries` table; the design
// assumes a future v0.8+ deployment will pin the
// worker to a leader (etcd / pg advisory lock).
// The store methods are atomic enough to survive
// a stray double-fire (DequeueRetry is idempotent
// and the new attempt's RetryDelivery is also
// idempotent on the same logical delivery) but
// the operator would see two HTTP requests per
// retry. Acceptable for v0.7.x; out of scope.

package webhooks

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// Worker is the background retry scheduler. The
// zero value is not usable; construct via
// NewWorker.
type Worker struct {
	svc      *Service
	interval time.Duration
	batch    int
	now      func() time.Time
}

// NewWorker wires a Worker. `interval` is the tick
// period (5s in production — the smallest retry
// interval is 1s, so 5s gives the worker enough
// resolution without being noisy). `batch` is the
// per-tick cap (0 means DefaultWorkerBatch = 100).
func NewWorker(svc *Service, interval time.Duration) *Worker {
	return &Worker{
		svc:      svc,
		interval: interval,
		batch:    DefaultWorkerBatch,
		now:      time.Now,
	}
}

// SetBatch overrides the per-tick cap. The
// interval stays fixed. Test-only.
func (w *Worker) SetBatch(n int) {
	w.batch = n
}

// SetClock swaps the time source. Test-only.
func (w *Worker) SetClock(now func() time.Time) {
	w.now = now
}

// Run blocks until ctx is cancelled. On every
// tick the worker calls ProcessDueRetries with
// the current time and `w.batch`. The function
// returns nil on clean cancellation; an
// unrecoverable error returns it to the caller
// (main.go logs and exits).
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

// tick fires one batch of due retries. The per-
// tick context is bounded to `w.interval` so a
// slow Store / HTTP backend never stacks ticks.
//
// A returned error is logged at WARN and the
// worker keeps running — the next tick will
// retry the same rows.
func (w *Worker) tick(ctx context.Context) {
	// Per-tick context bounded to the interval.
	// A slow tick must not stack up behind the
	// next one (which would extend the
	// worker's effective poll rate).
	tickCtx, cancel := context.WithTimeout(ctx, w.interval)
	defer cancel()
	fired, err := w.svc.ProcessDueRetries(tickCtx, w.now(), w.batch)
	if err != nil {
		log.Warn().Err(err).Msg("webhooks: retry worker tick: list/process failed")
		return
	}
	if fired > 0 {
		log.Info().
			Int("fired", fired).
			Dur("interval", w.interval).
			Msg("webhooks: retry worker tick")
	}
}
