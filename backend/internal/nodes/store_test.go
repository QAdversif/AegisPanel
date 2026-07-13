// SPDX-License-Identifier: AGPL-3.0-or-later

package nodes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemoryStore_CreateGetList(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	// Monotonic-increasing clock so CreatedAt strictly
	// orders the rows. A fixed clock makes List's sort
	// unstable, which under -race sometimes returns rows in
	// the wrong order and trips this assertion.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var tick int64
	s.SetClock(func() time.Time {
		tick++
		return t0.Add(time.Duration(tick) * time.Microsecond)
	})

	n1 := &Node{ID: uuid.New(), Name: "alpha", Region: "eu", State: StateNew, Address: "10.0.0.1:22"}
	n2 := &Node{ID: uuid.New(), Name: "bravo", Region: "us", State: StateOnline, Address: "10.0.0.2:22"}

	for _, n := range []*Node{n1, n2} {
		if err := s.Create(ctx, n); err != nil {
			t.Fatalf("create %s: %v", n.Name, err)
		}
	}

	// Timestamps assigned by the store. Each Create bumps
	// the internal tick so n2's CreatedAt is strictly after
	// n1's — the relative order matters, the absolute value
	// does not.
	if n2.CreatedAt.Before(n1.CreatedAt) {
		t.Fatalf("n2.CreatedAt (%v) should be after n1.CreatedAt (%v)", n2.CreatedAt, n1.CreatedAt)
	}
	if !n1.UpdatedAt.Equal(n1.CreatedAt) {
		t.Fatalf("UpdatedAt = %v, want %v (equal to CreatedAt on Create)", n1.UpdatedAt, n1.CreatedAt)
	}

	// GetByID returns a defensive copy — mutating the result
	// must not affect the stored value.
	got, err := s.GetByID(ctx, n1.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	got.Name = "MUTATED"
	got.Tags = []string{"injected"}
	again, _ := s.GetByID(ctx, n1.ID)
	if again.Name != "alpha" {
		t.Fatalf("stored Name mutated through returned pointer: %q", again.Name)
	}
	if again.Tags != nil {
		t.Fatalf("stored Tags mutated through returned pointer: %v", again.Tags)
	}

	// GetByName works.
	byName, err := s.GetByName(ctx, "bravo")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if byName.ID != n2.ID {
		t.Fatalf("GetByName returned wrong id")
	}

	// List returns both, sorted by CreatedAt.
	all, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List len = %d, want 2", len(all))
	}
	if all[0].Name != "alpha" || all[1].Name != "bravo" {
		t.Fatalf("List order wrong: %v", all)
	}
}

func TestMemoryStore_Create_DuplicateName(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	n := &Node{ID: uuid.New(), Name: "alpha", Region: "eu", Address: "10.0.0.1:22"}
	if err := s.Create(ctx, n); err != nil {
		t.Fatalf("first create: %v", err)
	}
	dup := &Node{ID: uuid.New(), Name: "alpha", Region: "us", Address: "10.0.0.2:22"}
	err := s.Create(ctx, dup)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate create err = %v, want ErrDuplicate", err)
	}
}

func TestMemoryStore_Create_DuplicateID(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	id := uuid.New()
	if err := s.Create(ctx, &Node{ID: id, Name: "alpha", Region: "eu", Address: "10.0.0.1:22"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := s.Create(ctx, &Node{ID: id, Name: "bravo", Region: "us", Address: "10.0.0.2:22"})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate id err = %v, want ErrDuplicate", err)
	}
}

func TestMemoryStore_Update_NotFound(t *testing.T) {
	s := NewMemoryStore()
	err := s.Update(context.Background(), &Node{ID: uuid.New(), Name: "x", Region: "y", Address: "z"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_Update_RenameCollision(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	a := &Node{ID: uuid.New(), Name: "alpha", Region: "eu", Address: "10.0.0.1:22"}
	b := &Node{ID: uuid.New(), Name: "bravo", Region: "us", Address: "10.0.0.2:22"}
	for _, n := range []*Node{a, b} {
		if err := s.Create(ctx, n); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a.Name = "bravo" // would collide with b
	err := s.Update(ctx, a)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("rename collision err = %v, want ErrDuplicate", err)
	}
}

func TestMemoryStore_Update_BumpsUpdatedAt(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return t0 })
	n := &Node{ID: uuid.New(), Name: "alpha", Region: "eu", Address: "10.0.0.1:22"}
	if err := s.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}

	t1 := t0.Add(time.Hour)
	s.SetClock(func() time.Time { return t1 })
	n.Region = "us"
	if err := s.Update(ctx, n); err != nil {
		t.Fatalf("update: %v", err)
	}

	again, _ := s.GetByID(ctx, n.ID)
	if !again.UpdatedAt.Equal(t1) {
		t.Fatalf("UpdatedAt = %v, want %v", again.UpdatedAt, t1)
	}
	if !again.CreatedAt.Equal(t0) {
		t.Fatalf("CreatedAt should be preserved on update, got %v", again.CreatedAt)
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	n := &Node{ID: uuid.New(), Name: "alpha", Region: "eu", Address: "10.0.0.1:22"}
	if err := s.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Delete(ctx, n.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := s.GetByID(ctx, n.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete GetByID err = %v, want ErrNotFound", err)
	}
	// Idempotent at the store level? No — Delete returns
	// ErrNotFound on a second call so the handler can map it
	// to 404.
	err = s.Delete(ctx, n.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete err = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_TagsCopyOnRead(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	n := &Node{
		ID: uuid.New(), Name: "alpha", Region: "eu", Address: "10.0.0.1:22",
		Tags: []string{"vless", "reality"},
	}
	if err := s.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := s.GetByID(ctx, n.ID)
	got.Tags[0] = "MUTATED"
	again, _ := s.GetByID(ctx, n.ID)
	if again.Tags[0] != "vless" {
		t.Fatalf("Tags slice shared between calls: %v", again.Tags)
	}
}
