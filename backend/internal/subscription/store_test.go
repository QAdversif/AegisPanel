// SPDX-License-Identifier: AGPL-3.0-or-later

package subscription

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeClock returns a fixed instant so store-internal
// CreatedAt / UpdatedAt assignments are deterministic.
func fakeClock() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

// newSeededStore returns a MemoryStore with a single
// plan, a single pool, and two pool members. The IDs
// are stable across calls so tests can reference them
// without re-reading the store.
//
// As of d-refactor.2 the MemoryStore does not own
// user-CRUD rows (those live in `users.MemoryStore`).
// The pool-list tests construct a `*User` literal at
// the call site rather than seeding it through the
// store, which is how the production code path
// (Service.ResolveHostsForUser) is shaped too: a
// Service pulls the user from the users package and
// passes it to ListPoolsForUser.
func newSeededStore(t *testing.T) *MemoryStore {
	t.Helper()
	s := NewMemoryStore()
	s.SetClock(fakeClock)

	planID := uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	s.WithPlan(&Plan{
		ID:                planID,
		Name:              "starter",
		TrafficLimitBytes: 100 * 1024 * 1024 * 1024,
		Duration:          30 * 24 * time.Hour,
		DeviceLimit:       3,
		ResetPeriod:       ResetMonthly,
	})

	poolID := uuid.MustParse("00000000-0000-0000-0000-0000000000b1")
	s.WithPool(&Pool{
		ID:           poolID,
		Name:         "eu",
		Strategy:     PoolStrategyAll,
		Antiaffinity: true,
	})
	// Two members with different weights.
	s.WithPoolMember(PoolMember{
		PoolID: poolID,
		HostID: uuid.MustParse("00000000-0000-0000-0000-0000000000c1"),
		Weight: 1,
	}).WithPoolMember(PoolMember{
		PoolID: poolID,
		HostID: uuid.MustParse("00000000-0000-0000-0000-0000000000c2"),
		Weight: 2,
	})

	return s
}

// userWithPlan constructs a `*User` literal with a
// non-nil plan_id. As of d-refactor.2 the user-CRUD
// is owned by the users package; the test fixtures
// here do not need a full Store round-trip — a
// literal is enough to drive the
// ListPoolsForUser / ListPoolsAll code paths.
func userWithPlan(planID uuid.UUID) *User {
	return &User{
		ID:       uuid.MustParse("00000000-0000-0000-0000-0000000000d1"),
		Username: "alice",
		Status:   UserStatusActive,
		PlanID:   &planID,
		SubToken: "tok-alice",
	}
}

func userWithoutPlan() *User {
	return &User{
		ID:       uuid.MustParse("00000000-0000-0000-0000-0000000000d2"),
		Username: "bob",
		Status:   UserStatusActive,
		SubToken: "tok-bob",
	}
}

func TestMemoryStore_ListPoolsForUser(t *testing.T) {
	s := newSeededStore(t)
	planID := uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	alice := userWithPlan(planID)
	bob := userWithoutPlan()

	got, err := s.ListPoolsForUser(context.Background(), alice)
	if err != nil {
		t.Fatalf("ListPoolsForUser(alice): %v", err)
	}
	if len(got) != 1 {
		t.Errorf("alice pools = %d, want 1", len(got))
	}

	got, err = s.ListPoolsForUser(context.Background(), bob)
	if err != nil {
		t.Fatalf("ListPoolsForUser(bob): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("bob pools = %d, want 0 (no plan)", len(got))
	}
}

func TestMemoryStore_ListPoolMembers_SortedByHostID(t *testing.T) {
	s := newSeededStore(t)
	poolID := uuid.MustParse("00000000-0000-0000-0000-0000000000b1")

	got, err := s.ListPoolMembers(context.Background(), poolID)
	if err != nil {
		t.Fatalf("ListPoolMembers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// Members are sorted by HostID ascending — the
	// store guarantees this so the Service does not
	// have to re-sort.
	if got[0].HostID.String() > got[1].HostID.String() {
		t.Errorf("not sorted: %s > %s", got[0].HostID, got[1].HostID)
	}
}

func TestMemoryStore_ListPoolsAll(t *testing.T) {
	s := newSeededStore(t)
	got, err := s.ListPoolsAll(context.Background())
	if err != nil {
		t.Fatalf("ListPoolsAll: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len = %d, want 1", len(got))
	}
}

func TestMemoryStore_WithPoolMember_ReplacesDuplicate(t *testing.T) {
	// PRIMARY KEY (pool_id, host_id) in migration 0001
	// means a second add for the same pair replaces
	// the first; the helper mirrors that.
	s := NewMemoryStore()
	poolID := uuid.New()
	hostID := uuid.New()

	s.WithPoolMember(PoolMember{PoolID: poolID, HostID: hostID, Weight: 1})
	s.WithPoolMember(PoolMember{PoolID: poolID, HostID: hostID, Weight: 7})

	members, err := s.ListPoolMembers(context.Background(), poolID)
	if err != nil {
		t.Fatalf("ListPoolMembers: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("len = %d, want 1 (PK collision should replace)", len(members))
	}
	if members[0].Weight != 7 {
		t.Errorf("weight = %d, want 7 (the second add wins)", members[0].Weight)
	}
}
