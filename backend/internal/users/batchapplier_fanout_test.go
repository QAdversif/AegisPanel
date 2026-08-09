// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Tests for the v0.7.x + v0.8.x BatchedApplier fan-out
// filter in `enqueueUserDelta`. The fan-out is narrowed
// by the user's `HostsAllowlist` / `HostsBlocklist`:
//
//   - v0.5.0 baseline: when both are empty, every
//     applier is enqueued (default allow).
//   - v0.7.x (pre-architecture fix): when either is
//     non-empty, the field's UUIDs were treated as
//     node IDs directly. This is the misimplementation
//     the v0.8.x work fixes.
//   - v0.8.x (current): the field stores HOST IDs
//     (per the architecture; the field is named
//     `HostsAllowlist`, not `NodesAllowlist`).
//     `enqueueUserDelta` expands each host to its
//     node IDs via `s.hosts.NodesForHost` and
//     unions them, then matches against the
//     BatchedApplier map (which is still keyed by
//     node UUID). Fail-closed: a nil `s.hosts` with
//     a non-empty field yields an empty fan-out
//     (logged warning).
//
// The tests inspect `BatchedApplier.QueueLen()` (the
// depth of the applier's input channel) — we never
// start the Run loop, so the FlushFn never fires and
// the delta sits in the channel buffer. QueueLen is
// exported specifically for this kind of test (and
// a future enqueue-pressure metric); the Run loop
// is tested separately in the cores package.

package users

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/cores"
)

// stubHosts is the v0.8.x test double for the
// host→node lookup. The test pre-seeds a host→node
// map at construction time; `NodesForHost` reads
// it. A host id that is not in the map returns
// (nil, ErrNotFound) — the same contract as
// `*hosts.Service.NodesForHost` (which wraps the
// store's ErrNotFound). The fail-closed behaviour
// in `enqueueUserDelta` is what makes "not in the
// map" equivalent to "no nodes".
type stubHosts struct {
	// hosts→nodes. The host id is the key; the
	// value is the set of node ids the host
	// references (via its Endpoints). Multiple
	// hosts may reference the same node (the
	// union in the test setup is a *multiset*; the
	// service flattens it to a set).
	hostToNodes map[uuid.UUID][]uuid.UUID
	// errFn is an optional override for failure
	// injection. nil = no errors. The single
	// argument is the host id, so the test can
	// inject errors for specific hosts.
	errFn func(uuid.UUID) error
}

func (s *stubHosts) NodesForHost(_ context.Context, hostID uuid.UUID) ([]uuid.UUID, error) {
	if s.errFn != nil {
		if err := s.errFn(hostID); err != nil {
			return nil, err
		}
	}
	nodes, ok := s.hostToNodes[hostID]
	if !ok {
		// Match the real service's wrap of
		// `hosts.ErrNotFound`. The enqueueUserDelta
		// code treats any error as "no nodes for
		// that host" (fail-closed); the test
		// doesn't inspect the sentinel.
		return nil, errors.New("stubHosts: host not found")
	}
	// Return a copy so the caller can't mutate
	// the stub's internal slice.
	out := make([]uuid.UUID, len(nodes))
	copy(out, nodes)
	return out, nil
}

// newFanoutSvc constructs a Service with the given
// node IDs registered as BatchedAppliers (small
// Window; the FlushFn is a no-op since we never
// start the Run loop). Returns the service and the
// per-node applier map for QueueLen assertions.
// `hosts` is the v0.8.x lookup; nil leaves the
// Service's `s.hosts` unset (the fail-closed
// empty-fan-out path).
func newFanoutSvc(t *testing.T, nodeIDs []uuid.UUID, hosts HostNodesLookup) (*Service, map[uuid.UUID]*cores.BatchedApplier) {
	t.Helper()
	aps := make(map[uuid.UUID]*cores.BatchedApplier, len(nodeIDs))
	for _, id := range nodeIDs {
		aps[id] = cores.NewBatchedApplier(
			20*time.Millisecond,
			100,
			func(ctx context.Context, _ []cores.Delta) error { return nil },
		)
	}
	svc := NewService(NewMemoryStore(nil)).WithBatchApplier(aps)
	if hosts != nil {
		svc.WithHosts(hosts)
	}
	return svc, aps
}

// TestEnqueueUserDelta_EmptyAllowlist_AllAppliers covers
// the v0.5.0-compatible path: a user with no
// allowlist / blocklist fans out to every
// registered applier (default allow). The hosts
// lookup is nil (the field is empty, so the
// lookup is never consulted).
func TestEnqueueUserDelta_EmptyAllowlist_AllAppliers(t *testing.T) {
	t.Parallel()
	node1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	node2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	node3 := uuid.MustParse("33333333-3333-3333-4444-333333333333")
	svc, aps := newFanoutSvc(t, []uuid.UUID{node1, node2, node3}, nil)
	u := &User{ID: uuid.New(), Username: "u"}
	delta := cores.Delta{Kind: cores.DeltaAddUser, UserID: u.ID}
	svc.enqueueUserDelta(context.Background(), delta, u)
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
// is the v0.8.x headline test: a user with a
// non-empty allowlist (host IDs) only fans out to
// appliers whose node ID is in the
// host→node-expanded allowlist. The stub maps
// `hostAllowed` to `[allowedNode]`; the other two
// node appliers are not in the expanded set and
// stay empty.
func TestEnqueueUserDelta_Allowlist_NarrowsToAllowedNodes(t *testing.T) {
	t.Parallel()
	allowedNode := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	other1 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	other2 := uuid.MustParse("33333333-3333-3333-4444-333333333333")
	hostAllowed := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	hosts := &stubHosts{
		hostToNodes: map[uuid.UUID][]uuid.UUID{
			hostAllowed: {allowedNode},
		},
	}
	svc, aps := newFanoutSvc(t, []uuid.UUID{allowedNode, other1, other2}, hosts)
	u := &User{
		ID:             uuid.New(),
		Username:       "u",
		HostsAllowlist: []uuid.UUID{hostAllowed},
	}
	delta := cores.Delta{Kind: cores.DeltaAddUser, UserID: u.ID}
	svc.enqueueUserDelta(context.Background(), delta, u)

	if got := aps[allowedNode].QueueLen(); got != 1 {
		t.Errorf("allowed-node QueueLen = %d, want 1", got)
	}
	if got := aps[other1].QueueLen(); got != 0 {
		t.Errorf("non-allowed #1 QueueLen = %d, want 0", got)
	}
	if got := aps[other2].QueueLen(); got != 0 {
		t.Errorf("non-allowed #2 QueueLen = %d, want 0", got)
	}
}

// TestEnqueueUserDelta_Allowlist_MultiHost covers
// the v0.8.x union semantic: a user with two
// hosts in the allowlist, where each host maps to
// a disjoint set of node IDs, fans out to every
// node in the union. This is the test that would
// have failed under the v0.7.x semantic (where
// the field's UUIDs were treated as node IDs
// directly — two host IDs would not match any of
// the three node appliers, and the fan-out would
// be empty).
func TestEnqueueUserDelta_Allowlist_MultiHost(t *testing.T) {
	t.Parallel()
	nodeA := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	nodeB := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	nodeC := uuid.MustParse("33333333-3333-3333-4444-333333333333")
	hostA := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	hostB := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	hosts := &stubHosts{
		hostToNodes: map[uuid.UUID][]uuid.UUID{
			hostA: {nodeA},
			hostB: {nodeB},
		},
	}
	svc, aps := newFanoutSvc(t, []uuid.UUID{nodeA, nodeB, nodeC}, hosts)
	u := &User{
		ID:             uuid.New(),
		Username:       "u",
		HostsAllowlist: []uuid.UUID{hostA, hostB},
	}
	delta := cores.Delta{Kind: cores.DeltaAddUser, UserID: u.ID}
	svc.enqueueUserDelta(context.Background(), delta, u)

	if got := aps[nodeA].QueueLen(); got != 1 {
		t.Errorf("nodeA QueueLen = %d, want 1 (hostA expansion)", got)
	}
	if got := aps[nodeB].QueueLen(); got != 1 {
		t.Errorf("nodeB QueueLen = %d, want 1 (hostB expansion)", got)
	}
	if got := aps[nodeC].QueueLen(); got != 0 {
		t.Errorf("nodeC QueueLen = %d, want 0 (no host references nodeC)", got)
	}
}

// TestEnqueueUserDelta_Blocklist_SkipsBlockedNodes
// covers the inverse filter: a user with a
// blocklist (host IDs) fans out to every applier
// EXCEPT the nodes the blocklist hosts reference.
// Empty allowlist + non-empty blocklist = "all
// nodes EXCEPT the blocked hosts' nodes".
func TestEnqueueUserDelta_Blocklist_SkipsBlockedNodes(t *testing.T) {
	t.Parallel()
	blockedNode := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	other1 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	other2 := uuid.MustParse("33333333-3333-3333-4444-333333333333")
	hostBlocked := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	hosts := &stubHosts{
		hostToNodes: map[uuid.UUID][]uuid.UUID{
			hostBlocked: {blockedNode},
		},
	}
	svc, aps := newFanoutSvc(t, []uuid.UUID{blockedNode, other1, other2}, hosts)
	u := &User{
		ID:             uuid.New(),
		Username:       "u",
		HostsBlocklist: []uuid.UUID{hostBlocked},
	}
	delta := cores.Delta{Kind: cores.DeltaAddUser, UserID: u.ID}
	svc.enqueueUserDelta(context.Background(), delta, u)

	if got := aps[blockedNode].QueueLen(); got != 0 {
		t.Errorf("blocked-node QueueLen = %d, want 0", got)
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
// allow-expanded set and the block-expanded set
// is skipped (the blocklist wins, as documented in
// the helper's contract). The stub maps hostA
// to {nodeBoth, nodeAllowed} and hostB (block) to
// {nodeBoth}; the result: nodeBoth is skipped,
// nodeAllowed is enqueued.
func TestEnqueueUserDelta_AllowAndBlock_BlockWins(t *testing.T) {
	t.Parallel()
	nodeBoth := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	nodeAllowed := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	hostA := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	hostB := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	hosts := &stubHosts{
		hostToNodes: map[uuid.UUID][]uuid.UUID{
			hostA: {nodeBoth, nodeAllowed},
			hostB: {nodeBoth},
		},
	}
	svc, aps := newFanoutSvc(t, []uuid.UUID{nodeBoth, nodeAllowed}, hosts)
	u := &User{
		ID:             uuid.New(),
		Username:       "u",
		HostsAllowlist: []uuid.UUID{hostA},
		HostsBlocklist: []uuid.UUID{hostB},
	}
	delta := cores.Delta{Kind: cores.DeltaAddUser, UserID: u.ID}
	svc.enqueueUserDelta(context.Background(), delta, u)

	if got := aps[nodeBoth].QueueLen(); got != 0 {
		t.Errorf("both-allow-and-block QueueLen = %d, want 0 (blocklist wins)", got)
	}
	if got := aps[nodeAllowed].QueueLen(); got != 1 {
		t.Errorf("allowed-only QueueLen = %d, want 1", got)
	}
}

// TestEnqueueUserDelta_NilHosts_NonEmptyField_FailsClosed
// pins the v0.8.x fail-closed behaviour: a
// non-empty `HostsAllowlist` with a nil `s.hosts`
// lookup must NOT silently fan out to every node
// (the v0.5.0 default-allow). The fan-out is empty
// and a warning is logged (the test does not
// assert the log; the operational signal is the
// empty QueueLen). This is the anti-regression
// for the v0.7.x misimplementation that
// accidentally treated the field as node IDs —
// the "fix" must not regress to "match everything"
// when the lookup is missing.
func TestEnqueueUserDelta_NilHosts_NonEmptyField_FailsClosed(t *testing.T) {
	t.Parallel()
	node1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	node2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	// hosts = nil; the field has a host id that
	// would expand to both nodes if the lookup
	// were present.
	svc, aps := newFanoutSvc(t, []uuid.UUID{node1, node2}, nil)
	u := &User{
		ID:             uuid.New(),
		Username:       "u",
		HostsAllowlist: []uuid.UUID{uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")},
	}
	delta := cores.Delta{Kind: cores.DeltaAddUser, UserID: u.ID}
	svc.enqueueUserDelta(context.Background(), delta, u)

	if got := aps[node1].QueueLen(); got != 0 {
		t.Errorf("node1 QueueLen = %d, want 0 (nil hosts lookup → fail-closed)", got)
	}
	if got := aps[node2].QueueLen(); got != 0 {
		t.Errorf("node2 QueueLen = %d, want 0 (nil hosts lookup → fail-closed)", got)
	}
}

// TestEnqueueUserDelta_UnknownHostInAllowlist_FailsClosed
// pins the per-host fail-closed behaviour: a host
// id in `HostsAllowlist` that the lookup returns
// ErrNotFound for is treated as "host has no
// nodes" (no fan-out contribution). The user
// could have stale data (a deleted host); the
// safe default is "deny".
func TestEnqueueUserDelta_UnknownHostInAllowlist_FailsClosed(t *testing.T) {
	t.Parallel()
	allowedNode := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	otherNode := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	hostAllowed := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	hostDeleted := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	hosts := &stubHosts{
		hostToNodes: map[uuid.UUID][]uuid.UUID{
			hostAllowed: {allowedNode},
			// hostDeleted intentionally absent.
		},
	}
	svc, aps := newFanoutSvc(t, []uuid.UUID{allowedNode, otherNode}, hosts)
	u := &User{
		ID:             uuid.New(),
		Username:       "u",
		HostsAllowlist: []uuid.UUID{hostAllowed, hostDeleted},
	}
	delta := cores.Delta{Kind: cores.DeltaAddUser, UserID: u.ID}
	svc.enqueueUserDelta(context.Background(), delta, u)

	if got := aps[allowedNode].QueueLen(); got != 1 {
		t.Errorf("allowedNode QueueLen = %d, want 1 (hostAllowed expansion)", got)
	}
	if got := aps[otherNode].QueueLen(); got != 0 {
		t.Errorf("otherNode QueueLen = %d, want 0 (no host references it)", got)
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
	svc, aps := newFanoutSvc(t, []uuid.UUID{node1, node2}, nil)
	delta := cores.Delta{Kind: cores.DeltaAddUser, UserID: uuid.New()}
	svc.enqueueUserDelta(context.Background(), delta, nil)
	if got := aps[node1].QueueLen(); got != 1 {
		t.Errorf("node1 QueueLen = %d, want 1 (nil user → default allow)", got)
	}
	if got := aps[node2].QueueLen(); got != 1 {
		t.Errorf("node2 QueueLen = %d, want 1 (nil user → default allow)", got)
	}
}
