// SPDX-License-Identifier: AGPL-3.0-or-later

package users

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is an in-process implementation of
// Store. It is used by:
//
//   - unit tests that need a fast, hermetic store
//     without spinning up Postgres;
//   - the dev / docker-compose smoke (the panel
//     falls back to MemoryStore if
//     INTEGRATION_DATABASE_URL is unset);
//   - the BatchedApplier's FlushFn re-render path
//     (v0.4.0-d.5) for the same reason — tests
//     need to verify the FlushFn without a DB.
//
// The concurrency model is a single RWMutex: reads
// (Get*, List*) take RLock; writes (Create, Update,
// Delete) take Lock. List returns a freshly-allocated
// slice; mutations by callers are safe.
//
// Three indexes are maintained for O(1) lookup:
//   - byID:    uuid.UUID → *User
//   - byUser:  username   → *User
//   - byToken: sub_token  → *User
//
// The prev-token chain (migration 0011) is supported
// via byPrevToken; the lookup logic in
// GetBySubToken follows the pgx implementation
// (current token first, then prev if usePrev and
// still in grace).
type MemoryStore struct {
	mu          sync.RWMutex
	byID        map[uuid.UUID]*User
	byUser      map[string]*User
	byToken     map[string]*User
	byPrevToken map[string]*User
	now         func() time.Time
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
		byID:        make(map[uuid.UUID]*User),
		byUser:      make(map[string]*User),
		byToken:     make(map[string]*User),
		byPrevToken: make(map[string]*User),
		now:         now,
	}
}

// SetClock swaps the time source. Test-only.
func (s *MemoryStore) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// WithUser copies `u` into the store and indexes it
// by id, by username, and by sub_token (plus the
// prev-token when set). The Create-time validation
// (IsValid) is bypassed for tests that need to seed
// half-built rows (e.g. a user with an invalid
// status, used by the "not live" test paths).
//
// Auto-fills CreatedAt / UpdatedAt from the
// store's clock when they are zero. Returns the
// same store so calls can chain:
//
//	store.WithUser(u1).WithUser(u2).WithUser(u3)
//
// This mirrors the d.0 subscription.MemoryStore.WithUser
// pattern that the v0.4.0-d consolidation dropped —
// the users.MemoryStore now owns the equivalent.
func (s *MemoryStore) WithUser(u *User) *MemoryStore {
	if u == nil {
		return s
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *u
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = s.now().UTC()
	}
	if cp.UpdatedAt.IsZero() {
		cp.UpdatedAt = cp.CreatedAt
	}
	s.byID[cp.ID] = &cp
	s.byUser[cp.Username] = &cp
	if cp.SubToken != "" {
		s.byToken[cp.SubToken] = &cp
	}
	if cp.SubTokenPrev != "" {
		s.byPrevToken[cp.SubTokenPrev] = &cp
	}
	return s
}

// Create inserts a new user. Returns ErrDuplicate
// if Username or SubToken is already in use.
func (s *MemoryStore) Create(ctx context.Context, u *User) error {
	if u == nil {
		return fmt.Errorf("create: nil user")
	}
	if u.ID == uuid.Nil {
		return fmt.Errorf("create: zero id")
	}
	if !u.IsValid() {
		return fmt.Errorf("create: invalid user")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[u.ID]; ok {
		return fmt.Errorf("%w: id %s", ErrDuplicate, u.ID)
	}
	if _, ok := s.byUser[u.Username]; ok {
		return fmt.Errorf("%w: username %q", ErrDuplicate, u.Username)
	}
	if _, ok := s.byToken[u.SubToken]; ok {
		return fmt.Errorf("%w: sub_token", ErrDuplicate)
	}
	if u.SubTokenPrev != "" {
		if _, ok := s.byPrevToken[u.SubTokenPrev]; ok {
			return fmt.Errorf("%w: sub_token_prev", ErrDuplicate)
		}
	}
	now := s.now()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	// Defensive copy: store a snapshot so a caller
	// who mutates u after Create does not corrupt
	// the in-memory row.
	cp := *u
	s.byID[u.ID] = &cp
	s.byUser[u.Username] = &cp
	if u.SubToken != "" {
		s.byToken[u.SubToken] = &cp
	}
	if u.SubTokenPrev != "" {
		s.byPrevToken[u.SubTokenPrev] = &cp
	}
	return nil
}

// GetByID returns the user with the given id, or
// ErrNotFound. The returned pointer is to a fresh
// copy — mutating it does not affect the store.
func (s *MemoryStore) GetByID(ctx context.Context, id uuid.UUID) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("%w: id %s", ErrNotFound, id)
	}
	cp := *u
	return &cp, nil
}

// GetByUsername returns the user with the given
// username, or ErrNotFound.
func (s *MemoryStore) GetByUsername(ctx context.Context, username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.byUser[username]
	if !ok {
		return nil, fmt.Errorf("%w: username %q", ErrNotFound, username)
	}
	cp := *u
	return &cp, nil
}

// GetBySubToken looks up the user by sub_token. The
// usePrev flag controls whether the prev-token chain
// is consulted (see migration 0011 and Store
// interface comment).
//
// Lookup order:
//  1. byToken (current token). If found, return.
//  2. If usePrev is true, byPrevToken; the prev-token
//     is honoured only if SubTokenPrevExpiresAt is
//     non-zero and in the future (per migration 0011).
func (s *MemoryStore) GetBySubToken(ctx context.Context, token string, usePrev bool) (*User, error) {
	if token == "" {
		return nil, fmt.Errorf("%w: empty token", ErrNotFound)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u, ok := s.byToken[token]; ok {
		cp := *u
		return &cp, nil
	}
	if !usePrev {
		return nil, fmt.Errorf("%w: token", ErrNotFound)
	}
	u, ok := s.byPrevToken[token]
	if !ok {
		return nil, fmt.Errorf("%w: token", ErrNotFound)
	}
	if u.SubTokenPrevExpiresAt == nil || s.now().After(*u.SubTokenPrevExpiresAt) {
		return nil, fmt.Errorf("%w: token (prev expired)", ErrNotFound)
	}
	cp := *u
	return &cp, nil
}

// List returns every user, sorted by CreatedAt asc.
func (s *MemoryStore) List(ctx context.Context) ([]*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*User, 0, len(s.byID))
	for _, u := range s.byID {
		cp := *u
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// ListByStatus returns every user with the given
// status, sorted by CreatedAt asc.
func (s *MemoryStore) ListByStatus(ctx context.Context, status Status) ([]*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*User, 0)
	for _, u := range s.byID {
		if u.Status != status {
			continue
		}
		cp := *u
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// Update replaces the stored copy of u.ID. Returns
// ErrNotFound if no such user exists; ErrDuplicate
// if the rename would collide with an existing row.
func (s *MemoryStore) Update(ctx context.Context, u *User) error {
	if u == nil {
		return fmt.Errorf("update: nil user")
	}
	if u.ID == uuid.Nil {
		return fmt.Errorf("update: zero id")
	}
	if !u.IsValid() {
		return fmt.Errorf("update: invalid user")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.byID[u.ID]
	if !ok {
		return fmt.Errorf("%w: id %s", ErrNotFound, u.ID)
	}
	// Detect a username rename and check for
	// collision. The username UNIQUE constraint maps
	// to ErrDuplicate.
	if existing.Username != u.Username {
		if _, taken := s.byUser[u.Username]; taken {
			return fmt.Errorf("%w: username %q", ErrDuplicate, u.Username)
		}
	}
	// Detect a sub_token change and check for
	// collision. The sub_token UNIQUE constraint maps
	// to ErrDuplicate.
	if existing.SubToken != u.SubToken {
		if _, taken := s.byToken[u.SubToken]; taken {
			return fmt.Errorf("%w: sub_token", ErrDuplicate)
		}
	}
	// Detect a sub_token_prev change and check for
	// collision. The partial UNIQUE constraint in
	// migration 0011 maps to ErrDuplicate.
	if existing.SubTokenPrev != u.SubTokenPrev && u.SubTokenPrev != "" {
		if _, taken := s.byPrevToken[u.SubTokenPrev]; taken {
			return fmt.Errorf("%w: sub_token_prev", ErrDuplicate)
		}
	}
	// Drop the old indexes before installing the new
	// row. The byID map's pointer is replaced; the old
	// username / sub_token / sub_token_prev entries
	// are deleted so the new ones (if different)
	// don't collide.
	delete(s.byUser, existing.Username)
	if existing.SubToken != "" {
		delete(s.byToken, existing.SubToken)
	}
	if existing.SubTokenPrev != "" {
		delete(s.byPrevToken, existing.SubTokenPrev)
	}
	u.UpdatedAt = s.now()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = existing.CreatedAt
	}
	cp := *u
	s.byID[u.ID] = &cp
	s.byUser[u.Username] = &cp
	if u.SubToken != "" {
		s.byToken[u.SubToken] = &cp
	}
	if u.SubTokenPrev != "" {
		s.byPrevToken[u.SubTokenPrev] = &cp
	}
	return nil
}

// Delete removes the user with the given id. Returns
// ErrNotFound if no such user exists.
func (s *MemoryStore) Delete(ctx context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("%w: id %s", ErrNotFound, id)
	}
	delete(s.byID, id)
	delete(s.byUser, u.Username)
	if u.SubToken != "" {
		delete(s.byToken, u.SubToken)
	}
	if u.SubTokenPrev != "" {
		delete(s.byPrevToken, u.SubTokenPrev)
	}
	return nil
}

// Sentinel for tests that want to assert the
// in-memory implementation is wired to the Store
// interface. (Compile-time check, not a runtime
// test.)
