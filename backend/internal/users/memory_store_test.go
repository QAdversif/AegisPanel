// SPDX-License-Identifier: AGPL-3.0-or-later

package users

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fixedClock is a deterministic time source for
// tests. Now() always returns the same value so
// timestamps in the store are predictable.
func fixedClock() time.Time {
	return time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
}

// newMemStore is a one-line constructor used by
// every test in this file.
func newMemStore() *MemoryStore {
	return NewMemoryStore(fixedClock)
}

// TestMemoryStore_BasicCRUD exercises Create / Get
// / Update / Delete via the MemoryStore. The
// MemoryStore is the canonical "no Postgres
// needed" path so this test is the smoke for the
// whole package.
func TestMemoryStore_BasicCRUD(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()

	u := &User{
		ID:       uuid.New(),
		Username: "alice",
		Status:   StatusActive,
		SubToken: "fixture-sub-token-not-a-real-secret",
	}
	if err := s.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Username != "alice" {
		t.Errorf("Username = %q, want %q", got.Username, "alice")
	}
	if !got.CreatedAt.Equal(fixedClock()) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, fixedClock())
	}

	got.Username = "alice2"
	got.TrafficLimitBytes = 1_000_000
	if err := s.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got2, err := s.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID after Update: %v", err)
	}
	if got2.Username != "alice2" {
		t.Errorf("Username after Update = %q, want %q", got2.Username, "alice2")
	}
	if got2.TrafficLimitBytes != 1_000_000 {
		t.Errorf("TrafficLimitBytes = %d, want 1000000", got2.TrafficLimitBytes)
	}

	if err := s.Delete(ctx, u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetByID(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID after Delete: err = %v, want ErrNotFound", err)
	}
}

// TestMemoryStore_DuplicateUsername is the
// (username) UNIQUE-constraint test.
func TestMemoryStore_DuplicateUsername(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	if err := s.Create(ctx, &User{ID: uuid.New(), Username: "alice", Status: StatusActive, SubToken: "t1"}); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	err := s.Create(ctx, &User{ID: uuid.New(), Username: "alice", Status: StatusActive, SubToken: "t2"})
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("err = %v, want ErrDuplicate", err)
	}
}

// TestMemoryStore_DuplicateSubToken is the
// (sub_token) UNIQUE-constraint test.
func TestMemoryStore_DuplicateSubToken(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	if err := s.Create(ctx, &User{ID: uuid.New(), Username: "alice", Status: StatusActive, SubToken: "t1"}); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	err := s.Create(ctx, &User{ID: uuid.New(), Username: "bob", Status: StatusActive, SubToken: "t1"})
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("err = %v, want ErrDuplicate", err)
	}
}

// TestMemoryStore_GetBySubToken_PrevTokenActive
// exercises migration 0011: the prev-token is
// honoured when within its grace window.
func TestMemoryStore_GetBySubToken_PrevTokenActive(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	now := fixedClock()
	prevExpires := now.Add(24 * time.Hour)
	u := &User{
		ID:                    uuid.New(),
		Username:              "alice",
		Status:                StatusActive,
		SubToken:              "tok-current",
		SubTokenPrev:          "tok-prev",
		SubTokenPrevExpiresAt: &prevExpires,
	}
	if err := s.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.GetBySubToken(ctx, "tok-prev", true)
	if err != nil {
		t.Fatalf("GetBySubToken(prev, usePrev=true): %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("ID = %s, want %s", got.ID, u.ID)
	}
}

// TestMemoryStore_GetBySubToken_PrevTokenExpired
// confirms the prev-token lookup rejects when
// outside the grace window.
func TestMemoryStore_GetBySubToken_PrevTokenExpired(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	now := fixedClock()
	expired := now.Add(-1 * time.Hour) // already expired
	u := &User{
		ID:                    uuid.New(),
		Username:              "alice",
		Status:                StatusActive,
		SubToken:              "tok-current",
		SubTokenPrev:          "tok-prev",
		SubTokenPrevExpiresAt: &expired,
	}
	if err := s.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := s.GetBySubToken(ctx, "tok-prev", true)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (expired prev)", err)
	}
}

// TestMemoryStore_GetBySubToken_UsePrevFalse confirms
// the usePrev=false path skips the prev-token chain.
func TestMemoryStore_GetBySubToken_UsePrevFalse(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	now := fixedClock()
	prevExpires := now.Add(24 * time.Hour)
	u := &User{
		ID:                    uuid.New(),
		Username:              "alice",
		Status:                StatusActive,
		SubToken:              "tok-current",
		SubTokenPrev:          "tok-prev",
		SubTokenPrevExpiresAt: &prevExpires,
	}
	if err := s.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := s.GetBySubToken(ctx, "tok-prev", false)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (usePrev=false)", err)
	}
}

// TestMemoryStore_List_ListByStatus exercises the
// two list paths.
func TestMemoryStore_List_ListByStatus(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	for i, name := range []string{"alice", "bob", "carol"} {
		status := StatusActive
		if i == 1 {
			status = StatusDisabled
		}
		if err := s.Create(ctx, &User{
			ID:       uuid.New(),
			Username: name,
			Status:   status,
			SubToken: "tok-" + name,
		}); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	all, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("List: len = %d, want 3", len(all))
	}
	active, err := s.ListByStatus(ctx, StatusActive)
	if err != nil {
		t.Fatalf("ListByStatus(Active): %v", err)
	}
	if len(active) != 2 {
		t.Errorf("ListByStatus(Active): len = %d, want 2", len(active))
	}
}

// TestMemoryStore_Update_RenameCollision exercises
// the (username) UNIQUE constraint on Update.
func TestMemoryStore_Update_RenameCollision(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	if err := s.Create(ctx, &User{ID: uuid.New(), Username: "alice", Status: StatusActive, SubToken: "t1"}); err != nil {
		t.Fatalf("Create alice: %v", err)
	}
	bob := &User{ID: uuid.New(), Username: "bob", Status: StatusActive, SubToken: "t2"}
	if err := s.Create(ctx, bob); err != nil {
		t.Fatalf("Create bob: %v", err)
	}
	bob.Username = "alice" // collision
	err := s.Update(ctx, bob)
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("Update: err = %v, want ErrDuplicate", err)
	}
}
