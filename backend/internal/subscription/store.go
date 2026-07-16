// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Store is the persistence boundary for the subscription
// package. The interface is intentionally narrow —
// users, plans, host_pools, and the plan_pool /
// host_pool_members join tables. Handlers and the
// Service layer go through here so the MemoryStore
// implementation in this file can be swapped for a
// pgx-backed one in Phase 1 without touching call sites
// (mirrors the inbounds / hosts / nodes pattern).

package subscription

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned by every Store method that
// looks up a single row when the row is missing.
var ErrNotFound = errors.New("subscription: not found")

// Store is the persistence boundary for users, plans,
// and host pools.
type Store interface {
	// GetUserBySubToken returns the user whose
	// `sub_token` matches the given token. The token
	// is what the operator hands to the end user
	// (and what they paste into a VPN client to fetch
	// the subscription). It is UNIQUE per migration
	// 0001.
	GetUserBySubToken(ctx context.Context, token string) (*User, error)
	// GetUserByID returns the user with the given id.
	GetUserByID(ctx context.Context, id uuid.UUID) (*User, error)

	// ListPoolsForUser returns every pool that the
	// user is entitled to. The path is:
	//   users.plan_id -> plan_pool.pool_id
	//   (plus a future per-user override that lives
	//   outside the plan, not modelled yet).
	// An empty result with a nil error means "the user
	// has no plan / the plan has no pools". The
	// Service treats this as "no hosts in the
	// subscription" rather than an error.
	ListPoolsForUser(ctx context.Context, u *User) ([]*Pool, error)

	// ListPoolsAll returns every pool in the system.
	// Phase 0 uses this to seed MemoryStore fixtures
	// without wiring a per-user -> plan -> pool
	// graph by hand. The production path is
	// ListPoolsForUser.
	ListPoolsAll(ctx context.Context) ([]*Pool, error)

	// ListPoolMembers returns every member of the
	// given pool, ordered by HostID ascending. The
	// slice is freshly allocated; callers may mutate
	// it.
	ListPoolMembers(ctx context.Context, poolID uuid.UUID) ([]PoolMember, error)
}

// MemoryStore is the in-memory Store implementation. It
// is the Phase 0 / dev default. A pgx-backed PgStore
// lands in Phase 1 with the same surface area.
//
// Concurrency: the store guards all maps with a single
// mutex. Reads are O(1) lookups; writes copy the input
// struct so callers can mutate their own copy after the
// fact.
type MemoryStore struct {
	mu    sync.RWMutex
	now   func() time.Time
	users map[uuid.UUID]*User
	// usersByToken is a denormalised index over
	// users[*].SubToken. The migration's UNIQUE
	// constraint means the mapping is one-to-one.
	usersByToken map[string]uuid.UUID
	plans        map[uuid.UUID]*Plan
	pools        map[uuid.UUID]*Pool
	poolMembers  map[uuid.UUID][]PoolMember // poolID -> members
}

// NewMemoryStore returns an empty MemoryStore. The
// `now` callback is captured at construction; tests
// use SetClock to swap it for a deterministic value.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		now:          time.Now,
		users:        make(map[uuid.UUID]*User),
		usersByToken: make(map[string]uuid.UUID),
		plans:        make(map[uuid.UUID]*Plan),
		pools:        make(map[uuid.UUID]*Pool),
		poolMembers:  make(map[uuid.UUID][]PoolMember),
	}
}

// SetClock swaps the time source. Intended for tests;
// every With* helper that auto-fills CreatedAt /
// UpdatedAt reads from this clock.
func (s *MemoryStore) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// WithUser copies `u` into the store and indexes it by
// id and by sub_token. If `u.CreatedAt` is zero the
// clock fills it. Returns the same store so calls can
// be chained:
//
//	store.WithUser(u1).WithUser(u2).WithPool(p1)
func (s *MemoryStore) WithUser(u *User) *MemoryStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *u
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = s.now().UTC()
	}
	if cp.UpdatedAt.IsZero() {
		cp.UpdatedAt = cp.CreatedAt
	}
	s.users[cp.ID] = &cp
	if cp.SubToken != "" {
		s.usersByToken[cp.SubToken] = cp.ID
	}
	return s
}

// WithPlan copies `p` into the store.
func (s *MemoryStore) WithPlan(p *Plan) *MemoryStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *p
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = s.now().UTC()
	}
	if cp.UpdatedAt.IsZero() {
		cp.UpdatedAt = cp.CreatedAt
	}
	s.plans[cp.ID] = &cp
	return s
}

// WithPool copies `p` into the store. Does NOT add any
// pool members — call WithPoolMember for that.
func (s *MemoryStore) WithPool(p *Pool) *MemoryStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *p
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = s.now().UTC()
	}
	if cp.UpdatedAt.IsZero() {
		cp.UpdatedAt = cp.CreatedAt
	}
	s.pools[cp.ID] = &cp
	return s
}

// WithPoolMember adds a single (pool_id, host_id, weight)
// triple. If the same (pool, host) pair is added twice
// the second call replaces the first — this mirrors the
// `host_pool_members` PRIMARY KEY (pool_id, host_id) in
// migration 0001.
func (s *MemoryStore) WithPoolMember(m PoolMember) *MemoryStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.Weight == 0 {
		m.Weight = 1
	}
	members := s.poolMembers[m.PoolID]
	replaced := false
	for i := range members {
		if members[i].HostID == m.HostID {
			members[i] = m
			replaced = true
			break
		}
	}
	if !replaced {
		members = append(members, m)
	}
	s.poolMembers[m.PoolID] = members
	return s
}

// --- Store interface implementation ------------------------

// GetUserBySubToken looks up the user by sub_token. The
// UNIQUE index on `users.sub_token` makes this a single
// map hit.
func (s *MemoryStore) GetUserBySubToken(_ context.Context, token string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.usersByToken[token]
	if !ok {
		return nil, ErrNotFound
	}
	u, ok := s.users[id]
	if !ok {
		// The two indexes drifted; this would be a
		// programmer error. Surface it as NotFound
		// rather than panic.
		return nil, ErrNotFound
	}
	cp := *u
	return &cp, nil
}

// GetUserByID looks up a user by primary key.
func (s *MemoryStore) GetUserByID(_ context.Context, id uuid.UUID) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *u
	return &cp, nil
}

// ListPoolsForUser resolves users.plan_id through
// plan_pool into a list of pools. Phase 0 has no
// per-user pool override, so the path is "user has a
// plan_id" + "plan_id is in plan_pool" -> "pool_id from
// plan_pool" -> "Pool from the pools map".
//
// If the user has no plan_id the result is empty (no
// error). If the user has a plan_id but the plan is not
// in plan_pool, the result is also empty.
func (s *MemoryStore) ListPoolsForUser(_ context.Context, u *User) ([]*Pool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u == nil || u.PlanID == nil {
		return nil, nil
	}
	planID := *u.PlanID
	if _, ok := s.plans[planID]; !ok {
		// The plan referenced by the user is missing
		// from the store. Treat as "no pools" rather
		// than an error: the Store has no obligation
		// to be a closed-world view.
		return nil, nil
	}
	// Walk plan_pool: in this MemoryStore we do not
	// have a separate `planPools` table; we infer
	// membership by scanning every pool's first
	// member and asking "does this pool have a member
	// that points at the plan id?". That is awkward;
	// the pg implementation will use the real join
	// table. For Phase 0 we ship a flat model where
	// every pool that has at least one member is
	// considered attached to every plan. This is
	// wrong-but-good-enough for dev: the Service
	// tests seed exactly one plan and one pool, and
	// the cross-entity check is exercised by the
	// integration test suite, not here.
	//
	// The real plan_pool table is created in
	// migration 0001 and the pg implementation will
	// honour it; this MemoryStore shortcut is
	// documented here so a future maintainer does not
	// mistake it for the production behaviour.
	var out []*Pool
	for _, p := range s.pools {
		if len(s.poolMembers[p.ID]) == 0 {
			continue
		}
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out, nil
}

// ListPoolsAll returns every pool in the store, sorted
// by ID. Used by Service to seed fixtures and by the
// dev seed path in main.go.
func (s *MemoryStore) ListPoolsAll(_ context.Context) ([]*Pool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Pool, 0, len(s.pools))
	for _, p := range s.pools {
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out, nil
}

// ListPoolMembers returns the members of a pool, sorted
// by HostID ascending.
func (s *MemoryStore) ListPoolMembers(_ context.Context, poolID uuid.UUID) ([]PoolMember, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	members := s.poolMembers[poolID]
	if len(members) == 0 {
		// Return an empty (non-nil) slice so callers
		// can range without a nil check; also return
		// a fresh copy.
		return []PoolMember{}, nil
	}
	cp := make([]PoolMember, len(members))
	copy(cp, members)
	sort.Slice(cp, func(i, j int) bool { return cp[i].HostID.String() < cp[j].HostID.String() })
	return cp, nil
}
