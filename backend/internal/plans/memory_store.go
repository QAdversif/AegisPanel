// SPDX-License-Identifier: AGPL-3.0-or-later
//
// MemoryStore is an in-process implementation of
// Store. It is used by:
//
//   - unit tests that need a fast, hermetic store
//     without spinning up Postgres;
//   - the dev / docker-compose smoke (the panel
//     falls back to MemoryStore if
//     AEGIS_PLANS_BACKEND is unset or =memory).
//
// The concurrency model is a single RWMutex: reads
// (Get*, List*) take RLock; writes (Create, Update,
// Delete) take Lock. List returns a freshly-allocated
// slice; mutations by callers are safe.
//
// Three indexes are maintained for O(1) lookup:
//   - byID:   uuid.UUID → *Plan
//   - byName: name       → *Plan
//
// (no third index for the (plan_id, host_pool_id)
// join — that lives in `plan_pool` which the
// subscription package reads in v0.6.0.)

package plans

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is the in-process Store
// implementation. See the package doc comment for
// the rationale.
type MemoryStore struct {
	mu     sync.RWMutex
	byID   map[uuid.UUID]*Plan
	byName map[string]*Plan
	now    func() time.Time
}

// NewMemoryStore returns an empty MemoryStore. The
// now function is used to stamp CreatedAt / UpdatedAt
// on writes; tests inject a fixed clock to make
// timestamps deterministic.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{
		byID:   make(map[uuid.UUID]*Plan),
		byName: make(map[string]*Plan),
		now:    now,
	}
}

// SetClock swaps the time source. Test-only.
func (s *MemoryStore) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// WithPlan copies p into the store and indexes it by
// id and by name. The Create-time validation
// (IsValid) is bypassed for tests that need to seed
// half-built rows (e.g. a plan with an invalid
// ResetPeriod, used by the validator tests).
//
// Auto-fills CreatedAt / UpdatedAt from the store's
// clock when they are zero. Returns the same store
// so calls can chain:
//
//	store.WithPlan(p1).WithPlan(p2).WithPlan(p3)
//
// This mirrors the users.MemoryStore.WithUser
// pattern.
func (s *MemoryStore) WithPlan(p *Plan) *MemoryStore {
	if p == nil {
		return s
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *p
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = s.now().UTC()
	}
	if cp.UpdatedAt.IsZero() {
		cp.UpdatedAt = cp.CreatedAt
	}
	s.byID[cp.ID] = &cp
	s.byName[cp.Name] = &cp
	return s
}

// Create inserts a new plan. Returns ErrDuplicate
// if Name is already in use.
func (s *MemoryStore) Create(ctx context.Context, p *Plan) error {
	if p == nil {
		return fmt.Errorf("create: nil plan")
	}
	if p.ID == uuid.Nil {
		return fmt.Errorf("create: zero id")
	}
	if !p.IsValid() {
		return fmt.Errorf("create: invalid plan")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[p.ID]; ok {
		return fmt.Errorf("%w: id %s", ErrDuplicate, p.ID)
	}
	if _, ok := s.byName[p.Name]; ok {
		return fmt.Errorf("%w: name %q", ErrDuplicate, p.Name)
	}
	now := s.now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	// Defensive copy: store a snapshot so a caller
	// who mutates p after Create does not corrupt
	// the in-memory row.
	cp := *p
	s.byID[p.ID] = &cp
	s.byName[p.Name] = &cp
	return nil
}

// GetByID returns the plan with the given id, or
// ErrNotFound. The returned pointer is to a fresh
// copy — mutating it does not affect the store.
func (s *MemoryStore) GetByID(ctx context.Context, id uuid.UUID) (*Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("%w: id %s", ErrNotFound, id)
	}
	cp := *p
	return &cp, nil
}

// GetByName returns the plan with the given name, or
// ErrNotFound.
func (s *MemoryStore) GetByName(ctx context.Context, name string) (*Plan, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: empty name", ErrNotFound)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: name %q", ErrNotFound, name)
	}
	cp := *p
	return &cp, nil
}

// List returns every plan, sorted by CreatedAt asc.
func (s *MemoryStore) List(ctx context.Context) ([]*Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Plan, 0, len(s.byID))
	for _, p := range s.byID {
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// Update replaces the stored copy of p.ID. Returns
// ErrNotFound if no such plan exists; ErrDuplicate
// if the rename would collide with an existing row.
func (s *MemoryStore) Update(ctx context.Context, p *Plan) error {
	if p == nil {
		return fmt.Errorf("update: nil plan")
	}
	if p.ID == uuid.Nil {
		return fmt.Errorf("update: zero id")
	}
	if !p.IsValid() {
		return fmt.Errorf("update: invalid plan")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.byID[p.ID]
	if !ok {
		return fmt.Errorf("%w: id %s", ErrNotFound, p.ID)
	}
	// Detect a Name rename and check for collision.
	// The Name UNIQUE constraint maps to
	// ErrDuplicate.
	if existing.Name != p.Name {
		if _, taken := s.byName[p.Name]; taken {
			return fmt.Errorf("%w: name %q", ErrDuplicate, p.Name)
		}
	}
	// Drop the old index entries before installing
	// the new row.
	delete(s.byName, existing.Name)
	p.UpdatedAt = s.now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = existing.CreatedAt
	}
	cp := *p
	s.byID[p.ID] = &cp
	s.byName[p.Name] = &cp
	return nil
}

// Delete removes the plan with the given id. Returns
// ErrNotFound if no such plan exists.
func (s *MemoryStore) Delete(ctx context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("%w: id %s", ErrNotFound, id)
	}
	delete(s.byID, id)
	delete(s.byName, p.Name)
	return nil
}

// Sentinel for tests that want to assert the
// in-memory implementation is wired to the Store
// interface. (Compile-time check, not a runtime
// test.)
var _ Store = (*MemoryStore)(nil)
