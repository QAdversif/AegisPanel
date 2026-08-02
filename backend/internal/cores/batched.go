// SPDX-License-Identifier: AGPL-3.0-or-later
//
// BatchedApplier — coalesce per-user state-change deltas
// over a configurable time window before asking the core
// to render+apply. See ARCHITECTURE.md §7.5 for the design
// rationale (the Batched Apply primary strategy for cores
// without `DYNAMIC_USERS`, which today means sing-box on
// v1.0).
//
// # Why this lives in `internal/cores`, not in the sing-box
// package
//
// The applier is **core-agnostic**. It does not know about
// sing-box, Xray, or any specific core — it only knows
// about deltas (Kind, UserID, Payload) and a FlushFn
// callback. The actual render+apply logic lives in a
// FlushFn the caller wires up; the sing-box provider's
// FlushFn re-renders the full config from the current DB
// state and calls Apply. When the v2.0 Xray provider lands
// with `DYNAMIC_USERS`, its FlushFn will route through
// `HandlerService.AddUser/RemoveUser` instead.
//
// # Cancel/replace semantics
//
// Per §7.5: "если в окне пришла дельта AddUser + RemoveUser
// для одного юзера — обе дельты дропаются (no-op)". The
// absorb() method implements this. SetLimit deltas are
// treated independently: they always update the user's
// pending quota even if a paired AddUser is later cancelled
// by a RemoveUser (the user ends up in "remove" state with
// no quota to set).
//
// # Lifecycle
//
// One BatchedApplier per node. The panel's main() builds
// a map[node_id]*BatchedApplier at boot, spawns a Run()
// goroutine for each, and hands the applier to the
// user-management layer. On shutdown, the parent context
// is cancelled; the Run() loop drains and returns. Pending
// deltas on shutdown are lost (logged, not flushed) — the
// next panel restart picks up the same state from the DB.

package cores

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DeltaKind discriminates the three kinds of per-user state
// change the applier can collect. The FlushFn receives a
// []Delta and re-renders the full config from the current
// state — the Kind is informational.
type DeltaKind string

const (
	// DeltaAddUser requests the user be present in the
	// rendered config.
	DeltaAddUser DeltaKind = "add_user"
	// DeltaRemoveUser requests the user be absent.
	DeltaRemoveUser DeltaKind = "remove_user"
	// DeltaSetLimit updates the user's per-node quota.
	// Payload is provider-specific; for sing-box, it
	// carries `{"bytes": <int64>}`.
	DeltaSetLimit DeltaKind = "set_limit"
)

// Delta is a single per-user state-change request.
type Delta struct {
	Kind     DeltaKind
	UserID   uuid.UUID
	Payload  json.RawMessage
	Enqueued time.Time
}

// FlushFn is the callback invoked at the end of every
// coalescing window (or when maxQueue is reached). The
// applier does not know how to render or apply — that is
// the caller's job. The applier guarantees FlushFn is
// called at most once per window, with the set of deltas
// enqueued during the window (after cancel/replace).
//
// On error, the applier logs and continues; a transient
// flush failure must not block the next window.
type FlushFn func(ctx context.Context, deltas []Delta) error

// BatchedApplier coalesces per-user state-change deltas
// over a configurable time window.
type BatchedApplier struct {
	window   time.Duration
	maxQueue int
	queue    chan Delta
	flushFn  FlushFn
	clock    func() time.Time

	mu      sync.Mutex
	pending map[uuid.UUID]Delta
}

// NewBatchedApplier returns a ready-to-use BatchedApplier.
// window must be > 0 (default 20s); maxQueue must be > 0
// (default 1000); flushFn must be non-nil. The zero value
// is not usable — every panel process needs an explicit
// constructor.
func NewBatchedApplier(window time.Duration, maxQueue int, flushFn FlushFn) *BatchedApplier {
	if window <= 0 {
		window = 20 * time.Second
	}
	if maxQueue <= 0 {
		maxQueue = 1000
	}
	if flushFn == nil {
		panic("cores: NewBatchedApplier: flushFn is nil")
	}
	return &BatchedApplier{
		window:   window,
		maxQueue: maxQueue,
		queue:    make(chan Delta, maxQueue),
		flushFn:  flushFn,
		clock:    time.Now,
		pending:  make(map[uuid.UUID]Delta),
	}
}

// SetClock swaps the time source. Tests use this to make
// ticker behaviour deterministic; production code does not.
func (b *BatchedApplier) SetClock(now func() time.Time) { b.clock = now }

// Window returns the configured coalescing window. Useful
// for log lines and metric labels.
func (b *BatchedApplier) Window() time.Duration { return b.window }

// Enqueue hands d to the applier's run loop. Blocks if the
// queue is full (rare; maxQueue defaults to 1000 and the
// flush window is 20s, so a sustained 50-deltas/sec
// user-management rate is well under the cap).
func (b *BatchedApplier) Enqueue(d Delta) {
	if d.Enqueued.IsZero() {
		d.Enqueued = b.clock()
	}
	b.queue <- d
}

// QueueLen returns the current depth of the
// applier's input channel. Useful for tests that
// drive the fan-out filter without starting the
// Run loop, and for a future "enqueue pressure"
// metric. Not used on the hot path.
func (b *BatchedApplier) QueueLen() int {
	return len(b.queue)
}

// Run drives the coalescing loop. Returns when ctx is
// cancelled, after draining any deltas enqueued just before
// cancellation. The returned error is ctx.Err().
//
// Spawn it in a goroutine: `go b.Run(ctx)`.
func (b *BatchedApplier) Run(ctx context.Context) error {
	ticker := time.NewTicker(b.window)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			b.drain()
			return ctx.Err()
		case d := <-b.queue:
			b.absorb(d)
			if b.pendingLen() >= b.maxQueue {
				b.flush(ctx)
			}
		case <-ticker.C:
			b.flush(ctx)
		}
	}
}

// absorb merges d into b.pending per the §7.5 cancel/replace
// rules:
//
//   - AddUser + RemoveUser for the same UserID within the
//     window cancel out (both dropped, no-op).
//   - SetLimit always updates the pending state (the user
//     is assumed to be in the config; quota is just the
//     number).
//   - Same kind + same user: last-write-wins.
func (b *BatchedApplier) absorb(d Delta) {
	b.mu.Lock()
	defer b.mu.Unlock()
	existing, ok := b.pending[d.UserID]
	if !ok {
		b.pending[d.UserID] = d
		return
	}
	// Cancel/replace: AddUser + RemoveUser = no-op.
	if (existing.Kind == DeltaAddUser && d.Kind == DeltaRemoveUser) ||
		(existing.Kind == DeltaRemoveUser && d.Kind == DeltaAddUser) {
		delete(b.pending, d.UserID)
		return
	}
	// Default: latest wins.
	b.pending[d.UserID] = d
}

// flush takes a snapshot of pending and hands it to flushFn.
// Errors are logged, not returned — the next window must
// still happen.
func (b *BatchedApplier) flush(ctx context.Context) {
	b.mu.Lock()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return
	}
	snapshot := make([]Delta, 0, len(b.pending))
	for _, d := range b.pending {
		snapshot = append(snapshot, d)
	}
	b.pending = make(map[uuid.UUID]Delta)
	b.mu.Unlock()

	if err := b.flushFn(ctx, snapshot); err != nil {
		log.Printf("batched apply: flush failed (deltas=%d): %v", len(snapshot), err)
	}
}

// drain reads every remaining delta off the queue and
// absorbs it. Best-effort — does not invoke the FlushFn.
// The caller (Run) drops the snapshot; the next panel
// restart will rebuild the state from the DB.
func (b *BatchedApplier) drain() {
	for {
		select {
		case d := <-b.queue:
			b.absorb(d)
		default:
			return
		}
	}
}

func (b *BatchedApplier) pendingLen() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

// Pending returns a copy of the current pending set, for
// tests and metrics. Not used on the hot path.
func (b *BatchedApplier) Pending() []Delta {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Delta, 0, len(b.pending))
	for _, d := range b.pending {
		out = append(out, d)
	}
	return out
}
