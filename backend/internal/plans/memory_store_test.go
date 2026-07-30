// SPDX-License-Identifier: AGPL-3.0-or-later

package plans

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

	p := &Plan{
		ID:                uuid.New(),
		Name:              "starter",
		TrafficLimitBytes: 5_000_000_000,
		Duration:          30 * 24 * time.Hour,
		DeviceLimit:       3,
		ResetPeriod:       ResetMonthly,
		PriceCents:        500,
	}
	if err := s.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "starter" {
		t.Errorf("Name = %q, want %q", got.Name, "starter")
	}
	if !got.CreatedAt.Equal(fixedClock()) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, fixedClock())
	}

	got.Name = "starter-v2"
	got.PriceCents = 700
	if err := s.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got2, err := s.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID after Update: %v", err)
	}
	if got2.Name != "starter-v2" {
		t.Errorf("Name after Update = %q, want %q", got2.Name, "starter-v2")
	}
	if got2.PriceCents != 700 {
		t.Errorf("PriceCents = %d, want 700", got2.PriceCents)
	}
	// The byName index must reflect the rename.
	if _, err := s.GetByName(ctx, "starter-v2"); err != nil {
		t.Errorf("GetByName(starter-v2) after rename: %v", err)
	}
	if _, err := s.GetByName(ctx, "starter"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByName(starter) after rename: err = %v, want ErrNotFound", err)
	}

	if err := s.Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetByID(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID after Delete: err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetByName(ctx, "starter-v2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByName after Delete: err = %v, want ErrNotFound", err)
	}
}

// TestMemoryStore_GetByName covers the name-based
// read path and the empty-name short-circuit.
func TestMemoryStore_GetByName(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	p := &Plan{
		ID:          uuid.New(),
		Name:        "pro",
		Duration:    30 * 24 * time.Hour,
		ResetPeriod: ResetMonthly,
	}
	if err := s.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.GetByName(ctx, "pro")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("ID = %s, want %s", got.ID, p.ID)
	}
	if _, err := s.GetByName(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByName(missing): err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetByName(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByName(empty): err = %v, want ErrNotFound", err)
	}
}

// TestMemoryStore_List exercises the sorted list
// path. Three plans are inserted in non-monotonic
// order; the result must be sorted by CreatedAt asc.
func TestMemoryStore_List(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	// Insert in non-monotonic order so a buggy
	// sort would surface.
	mk := func(name string, daysAgo int) *Plan {
		return &Plan{
			ID:          uuid.New(),
			Name:        name,
			Duration:    30 * 24 * time.Hour,
			ResetPeriod: ResetMonthly,
			CreatedAt:   fixedClock().Add(-time.Duration(daysAgo) * 24 * time.Hour),
		}
	}
	p1 := mk("c", 2)
	p2 := mk("a", 0)
	p3 := mk("b", 1)
	// WithPlan bypasses Create-time validation and
	// the auto-stamp; we set CreatedAt explicitly.
	s.WithPlan(p1).WithPlan(p2).WithPlan(p3)
	out, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}
	if out[0].Name != "c" || out[1].Name != "b" || out[2].Name != "a" {
		t.Errorf("List order = [%s %s %s], want [c b a]",
			out[0].Name, out[1].Name, out[2].Name)
	}
	// Mutate the returned slice; the store must be
	// unaffected.
	out[0].Name = "mutated"
	again, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List (second): %v", err)
	}
	for _, p := range again {
		if p.Name == "mutated" {
			t.Errorf("MemoryStore leaked caller mutation: got %q", p.Name)
		}
	}
}

// TestMemoryStore_DuplicateName covers the
// (name) UNIQUE constraint surface. Two plans
// with the same name must fail with ErrDuplicate.
func TestMemoryStore_DuplicateName(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	p1 := &Plan{
		ID:          uuid.New(),
		Name:        "dupe",
		Duration:    30 * 24 * time.Hour,
		ResetPeriod: ResetMonthly,
	}
	if err := s.Create(ctx, p1); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	p2 := &Plan{
		ID:          uuid.New(),
		Name:        "dupe",
		Duration:    60 * 24 * time.Hour,
		ResetPeriod: ResetMonthly,
	}
	if err := s.Create(ctx, p2); !errors.Is(err, ErrDuplicate) {
		t.Errorf("Create duplicate: err = %v, want ErrDuplicate", err)
	}
	// Rename p1 to free the name; p2 should now
	// succeed.
	p1.Name = "dupe-renamed"
	if err := s.Update(ctx, p1); err != nil {
		t.Fatalf("Update rename: %v", err)
	}
	if err := s.Create(ctx, p2); err != nil {
		t.Errorf("Create after rename: %v", err)
	}
}

// TestMemoryStore_UpdateNotFound covers the
// "update a row that does not exist" branch.
func TestMemoryStore_UpdateNotFound(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	p := &Plan{
		ID:          uuid.New(),
		Name:        "ghost",
		Duration:    30 * 24 * time.Hour,
		ResetPeriod: ResetMonthly,
	}
	if err := s.Update(ctx, p); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update missing: err = %v, want ErrNotFound", err)
	}
}

// TestMemoryStore_DeleteNotFound covers the
// "delete a row that does not exist" branch.
func TestMemoryStore_DeleteNotFound(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	if err := s.Delete(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete missing: err = %v, want ErrNotFound", err)
	}
}

// TestMemoryStore_InvalidGuards covers the
// Store-side cheap pre-flight checks: nil input,
// zero id, IsValid-failing payload.
func TestMemoryStore_InvalidGuards(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	if err := s.Create(ctx, nil); err == nil {
		t.Errorf("Create(nil): want error, got nil")
	}
	if err := s.Create(ctx, &Plan{ID: uuid.Nil, Name: "x", Duration: 1, ResetPeriod: ResetMonthly}); err == nil {
		t.Errorf("Create(zero id): want error, got nil")
	}
	if err := s.Create(ctx, &Plan{ID: uuid.New(), Name: "", Duration: 1, ResetPeriod: ResetMonthly}); err == nil {
		t.Errorf("Create(empty name): want error, got nil")
	}
	// Negative traffic limit.
	if err := s.Create(ctx, &Plan{
		ID: uuid.New(), Name: "x",
		Duration: 1, ResetPeriod: ResetMonthly, TrafficLimitBytes: -1,
	}); err == nil {
		t.Errorf("Create(neg traffic): want error, got nil")
	}
	// Bad reset period.
	if err := s.Create(ctx, &Plan{
		ID: uuid.New(), Name: "x",
		Duration: 1, ResetPeriod: "yearly",
	}); err == nil {
		t.Errorf("Create(bad reset): want error, got nil")
	}
}
