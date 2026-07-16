// SPDX-License-Identifier: AGPL-3.0-or-later

package panelcfg

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store is the persistence boundary for the
// panel-wide configuration. The interface is
// intentionally narrow — only the read path the
// router needs and the write path the rotation API
// needs. Future global configs (e.g. a feature-flag
// table) land in a separate `Store` so the router's
// boot-time read does not need to know about them.
type Store interface {
	// GetActive returns the currently-active sub_path
	// config. The "active" row is the unique row
	// with `is_active = true` AND (expires_at IS NULL
	// OR expires_at > now). If multiple rows are
	// active, the most recently created wins (the
	// rotation flips the old row to inactive
	// transactionally with the new insert).
	GetActive(ctx context.Context) (*SubPathConfig, error)
	// GetByID returns the row by primary key. Used
	// for the admin surface (`GET /api/v1/admin/
	// panel/sub-path`).
	GetByID(ctx context.Context, id uuid.UUID) (*SubPathConfig, error)
	// SetActive replaces the active row's sub_path
	// with a new random value, marks the old row
	// inactive, and returns the new row. The
	// transaction is atomic: a crash mid-rotation
	// cannot leave the panel with two active rows.
	SetActive(ctx context.Context, newPath string, graceWindow time.Duration) (*SubPathConfig, error)
	// Reset deactivates the active row and re-inserts
	// the default empty sub_path. Used by the
	// admin "reset to default" action.
	Reset(ctx context.Context) (*SubPathConfig, error)
}

// ErrNotFound is returned by Store implementations
// when the requested row does not exist.
var ErrNotFound = errors.New("panelcfg: not found")

// MemoryStore is the Phase 0 default Store. It is
// concurrency-safe (sync.RWMutex around the rows
// slice) and the singleton-row invariant is enforced
// in SetActive / Reset. The "active" row is the
// most recently created row whose IsActive is true
// AND (ExpiresAt is nil OR in the future).
type MemoryStore struct {
	mu   sync.RWMutex
	rows map[uuid.UUID]*SubPathConfig
	now  func() time.Time
}

// NewMemoryStore returns a fresh in-memory store. The
// store seeds the default empty sub_path row so
// the router has something to read on boot even
// before the admin rotates for the first time.
func NewMemoryStore() *MemoryStore {
	s := &MemoryStore{
		rows: make(map[uuid.UUID]*SubPathConfig),
		now:  time.Now,
	}
	// Seed the default row. ID is the sentinel.
	s.rows[SentinelID] = &SubPathConfig{
		ID:        SentinelID,
		SubPath:   DefaultSubPath,
		IsActive:  true,
		CreatedAt: s.now().UTC(),
		ExpiresAt: nil,
	}
	return s
}

// SetClock swaps the time source. Tests use a
// fixed clock so the rotation-grace semantics are
// deterministic.
func (s *MemoryStore) SetClock(now func() time.Time) { s.now = now }

// GetActive returns the unique active row. The
// MemoryStore enforces the "at most one active row"
// invariant in SetActive; GetActive picks the
// newest active row whose ExpiresAt is in the future
// (or nil).
func (s *MemoryStore) GetActive(ctx context.Context) (*SubPathConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.now()
	var best *SubPathConfig
	for _, r := range s.rows {
		if !r.IsActive {
			continue
		}
		if r.ExpiresAt != nil && !r.ExpiresAt.After(now) {
			continue
		}
		if best == nil || r.CreatedAt.After(best.CreatedAt) {
			best = r
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%w", ErrNotFound)
	}
	cp := *best
	return &cp, nil
}

// GetByID returns the row with the given id.
func (s *MemoryStore) GetByID(ctx context.Context, id uuid.UUID) (*SubPathConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rows[id]
	if !ok {
		return nil, fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	cp := *r
	return &cp, nil
}

// SetActive rotates the active row. The old active
// row is marked inactive and (optionally) given a
// grace expiry. A new row with the new path is
// inserted with IsActive=true. The function is
// idempotent: calling it twice with the same
// `newPath` produces two rows (the second wins).
func (s *MemoryStore) SetActive(ctx context.Context, newPath string, graceWindow time.Duration) (*SubPathConfig, error) {
	if err := ValidatePath(newPath); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	// Mark every currently-active row as inactive.
	// If a grace window is set, the row's ExpiresAt
	// is set to now+grace so the router can serve
	// requests on the old path for that window.
	for _, r := range s.rows {
		if r.IsActive {
			r.IsActive = false
			if graceWindow > 0 {
				exp := now.Add(graceWindow)
				r.ExpiresAt = &exp
			} else {
				r.ExpiresAt = nil
			}
		}
	}
	// Insert the new row. The new row's ID is a
	// fresh UUID — the sentinel is reserved for the
	// "first" row (the one seeded by the store) and
	// by convention, the sentinel stays the
	// "default" row that the operator can reset to.
	// Rotation rows carry their own IDs.
	newRow := &SubPathConfig{
		ID:        uuid.New(),
		SubPath:   newPath,
		IsActive:  true,
		CreatedAt: now,
		ExpiresAt: nil,
	}
	s.rows[newRow.ID] = newRow
	cp := *newRow
	return &cp, nil
}

// Reset deactivates the active row and re-inserts
// the default empty sub_path at the sentinel id. The
// operator uses this to "go back to the documented
// /api/v1/sub/<token> path" after a rotation
// experiment.
func (s *MemoryStore) Reset(ctx context.Context) (*SubPathConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	for _, r := range s.rows {
		if r.IsActive {
			r.IsActive = false
			r.ExpiresAt = nil
		}
	}
	defaultRow := &SubPathConfig{
		ID:        SentinelID,
		SubPath:   DefaultSubPath,
		IsActive:  true,
		CreatedAt: now,
		ExpiresAt: nil,
	}
	s.rows[SentinelID] = defaultRow
	cp := *defaultRow
	return &cp, nil
}
