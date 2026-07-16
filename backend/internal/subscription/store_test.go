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
// plan, a single pool, two pool members, and two
// users — one attached to the plan, one not. The IDs
// are stable across calls so tests can reference them
// without re-reading the store.
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

	// User "alice" is on the plan, alive.
	planRef := planID
	s.WithUser(&User{
		ID:       uuid.MustParse("00000000-0000-0000-0000-0000000000d1"),
		Username: "alice",
		Status:   UserStatusActive,
		PlanID:   &planRef,
		SubToken: "tok-alice",
	})

	// User "bob" is on no plan at all.
	s.WithUser(&User{
		ID:       uuid.MustParse("00000000-0000-0000-0000-0000000000d2"),
		Username: "bob",
		Status:   UserStatusActive,
		SubToken: "tok-bob",
	})

	// User "carol" is on the plan but expired.
	s.WithUser(&User{
		ID:       uuid.MustParse("00000000-0000-0000-0000-0000000000d3"),
		Username: "carol",
		Status:   UserStatusExpired,
		PlanID:   &planRef,
		SubToken: "tok-carol",
	})

	return s
}

func TestMemoryStore_GetUserBySubToken(t *testing.T) {
	s := newSeededStore(t)

	got, err := s.GetUserBySubToken(context.Background(), "tok-alice")
	if err != nil {
		t.Fatalf("GetUserBySubToken: %v", err)
	}
	if got.Username != "alice" {
		t.Errorf("username = %q, want %q", got.Username, "alice")
	}

	if _, err := s.GetUserBySubToken(context.Background(), "tok-nope"); err == nil {
		t.Fatal("expected ErrNotFound for unknown token")
	}
}

func TestMemoryStore_GetUserByID(t *testing.T) {
	s := newSeededStore(t)
	id := uuid.MustParse("00000000-0000-0000-0000-0000000000d1")

	got, err := s.GetUserByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.ID != id {
		t.Errorf("id = %s, want %s", got.ID, id)
	}

	if _, err := s.GetUserByID(context.Background(), uuid.New()); err == nil {
		t.Fatal("expected ErrNotFound for unknown id")
	}
}

func TestMemoryStore_ListPoolsForUser(t *testing.T) {
	s := newSeededStore(t)
	alice, _ := s.GetUserBySubToken(context.Background(), "tok-alice")
	bob, _ := s.GetUserBySubToken(context.Background(), "tok-bob")

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
