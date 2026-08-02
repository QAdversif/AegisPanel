// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Tests for the v0.7.x BatchedApplier fan-out filter
// in `enqueueUserDelta`. The fan-out is narrowed by
// the user's `HostsAllowlist` / `HostsBlocklist` —
// when both are empty, every applier is enqueued
// (v0.5.0 behaviour); when either is non-empty,
// the fan-out is the subset of appliers whose
// node UUID is in the allowlist and not in the
// blocklist.
//
// The tests inspect `BatchedApplier.QueueLen()`
// (the depth of the applier's input channel) —
// we never start the Run loop, so the FlushFn
// never fires and the delta sits in the channel
// buffer. QueueLen is exported specifically for
// this kind of test (and a future enqueue-pressure
// metric); the Run loop is tested separately in
// the cores package.

package users

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/cores"
)

// newFanoutSvc constructs a Service with the given
// node IDs registered as BatchedAppliers (small
// Window; the FlushFn is a no-op since we never
// start the Run loop). Returns the service and
// the per-node applier map for QueueLen assertions.
func newFanoutSvc(t *testing.T, nodeIDs []uuid.UUID) (*Service, map[uuid.UUID]*cores.BatchedApplier) {
	t.Helper()
	aps := make(map[uuid.UUID]*cores.BatchedApplier, len(nodeIDs))
	for _, id := range nodeIDs {
		aps[id] = cores.NewBatchedApplier(
			20*time.Millisecond,
			100,
			func(ctx context.Context, _ []cores.Delta) error { return nil },
		)
	}
	return NewService(NewMemoryStore(nil)).WithBatchApplier(aps), aps
}

// TestEnqueueUserDelta_EmptyAllowlist_AllAppliers covers
// the v0.5.0-compatible path: a user with no
// allowlist / blocklist fans out to every
// registered applier (default allow).
func TestEnqueueUserDelta_EmptyAllowlist_AllAppliers(t *testing.T) {
	t.Parallel()
	node1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	node2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	node3 := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	svc, aps := newFanoutSvc(t, []uuid.UUID{node1, node2, node3})
	u := &User{ID: uuid.New(), Username: "u"}
	delta := cores.Delta{Kind: cores.DeltaAddUser, UserID: u.ID}
	svc.enqueueUserDelta(delta, u)
	if got := aps[node1].QueueLen(); got != 1 {
		t.Errorf("node1 QueueLen = %d, want 1 (default allow)", got)
	}
	if got := aps[node2].QueueLen(); got != 1 {
		t.Errorf("node2 QueueLen = %d, want 1 (default allow)", got)
	}
	if got := aps[node3].QueueLen(); got != 1 {
		t.Errorf("node3 QueueLen = %d, want 1 (default allow)", got)
	}
}

// TestEnqueueUserDelta_Allowlist_NarrowsToAllowedNodes
// is the Phase 2 headline test: a user with a
// non-empty allowlist only fans out to appliers
// whose node ID is in the allowlist.
func TestEnqueueUserDelta_Allowlist_NarrowsToAllowedNodes(t *testing.T) {
	t.Parallel()
	allowed := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	other1 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	other2 := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	svc, aps := newFanoutSvc(t, []uuid.UUID{allowed, other1, other2})
	u := &User{
		ID:             uuid.New(),
		Username:       "u",
		HostsAllowlist: []uuid.UUID{allowed},
	}
	delta := cores.Delta{Kind: cores.DeltaAddUser, UserID: u.ID}
	svc.enqueueUserDelta(delta, u)

	if got := aps[allowed].QueueLen(); got != 1 {
		t.Errorf("allowed QueueLen = %d, want 1", got)
	}
	if got := aps[other1].QueueLen(); got != 0 {
		t.Errorf("non-allowed #1 QueueLen = %d, want 0", got)
	}
	if got := aps[other2].QueueLen(); got != 0 {
		t.Errorf("non-allowed #2 QueueLen = %d, want 0", got)
	}
}

// TestEnqueueUserDelta_Blocklist_SkipsBlockedNodes
// covers the inverse filter: a user with a
// blocklist fans out to every applier EXCEPT
// the blocked ones.
func TestEnqueueUserDelta_Blocklist_SkipsBlockedNodes(t *testing.T) {
	t.Parallel()
	blocked := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	other1 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	other2 := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	svc, aps := newFanoutSvc(t, []uuid.UUID{blocked, other1, other2})
	u := &User{
		ID:             uuid.New(),
		Username:       "u",
		HostsBlocklist: []uuid.UUID{blocked},
	}
	delta := cores.Delta{Kind: cores.DeltaAddUser, UserID: u.ID}
	svc.enqueueUserDelta(delta, u)

	if got := aps[blocked].QueueLen(); got != 0 {
		t.Errorf("blocked QueueLen = %d, want 0", got)
	}
	if got := aps[other1].QueueLen(); got != 1 {
		t.Errorf("non-blocked #1 QueueLen = %d, want 1", got)
	}
	if got := aps[other2].QueueLen(); got != 1 {
		t.Errorf("non-blocked #2 QueueLen = %d, want 1", got)
	}
}

// TestEnqueueUserDelta_AllowAndBlock_BlockWins pins
// the precedence rule: a node that is in BOTH the
// allowlist and the blocklist is skipped (the
// blocklist wins, as documented in the helper's
// contract).
func TestEnqueueUserDelta_AllowAndBlock_BlockWins(t *testing.T) {
	t.Parallel()
	both := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	allowed := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	svc, aps := newFanoutSvc(t, []uuid.UUID{both, allowed})
	u := &User{
		ID:             uuid.New(),
		Username:       "u",
		HostsAllowlist: []uuid.UUID{both, allowed},
		HostsBlocklist: []uuid.UUID{both},
	}
	delta := cores.Delta{Kind: cores.DeltaAddUser, UserID: u.ID}
	svc.enqueueUserDelta(delta, u)

	if got := aps[both].QueueLen(); got != 0 {
		t.Errorf("both-allow-and-block QueueLen = %d, want 0 (blocklist wins)", got)
	}
	if got := aps[allowed].QueueLen(); got != 1 {
		t.Errorf("allowed-only QueueLen = %d, want 1", got)
	}
}

// TestEnqueueUserDelta_NilUser_FansOutToAll is the
// defensive path: a nil user (which the four
// call sites never pass, but be defensive) falls
// back to the v0.5.0 fan-out-to-all behaviour so
// a caller bug does not silently drop deltas.
func TestEnqueueUserDelta_NilUser_FansOutToAll(t *testing.T) {
	t.Parallel()
	node1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	node2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	svc, aps := newFanoutSvc(t, []uuid.UUID{node1, node2})
	delta := cores.Delta{Kind: cores.DeltaAddUser, UserID: uuid.New()}
	svc.enqueueUserDelta(delta, nil)
	if got := aps[node1].QueueLen(); got != 1 {
		t.Errorf("node1 QueueLen = %d, want 1 (nil user → default allow)", got)
	}
	if got := aps[node2].QueueLen(); got != 1 {
		t.Errorf("node2 QueueLen = %d, want 1 (nil user → default allow)", got)
	}
}
