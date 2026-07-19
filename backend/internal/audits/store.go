// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Store interface + MemoryStore implementation for
// the audit log. The pgx-backed PgStore lives in
// pg_store.go.

package audits

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Store is the persistence boundary for audit
// entries. The interface is intentionally small —
// only the write path the rest of the panel needs
// (Record) and the read path the v0.2.0 UI needs
// (List + GetByID).
type Store interface {
	// Insert appends an entry. The Store fills the
	// ID + CreatedAt fields on the returned
	// AuditEntry (the input is treated as
	// read-only). Returns the persisted copy so
	// the caller can echo it back to the API
	// client (the change-password handler does
	// this in v0.2.0 as a smoke test for the
	// write path).
	Insert(ctx context.Context, e Entry) (*AuditEntry, error)

	// List returns entries matching the filter,
	// ordered by created_at DESC. The slice is
	// freshly allocated; callers may mutate
	// without affecting the store.
	List(ctx context.Context, f ListFilter) ([]*AuditEntry, error)

	// GetByID returns the entry with the given
	// id, or ErrNotFound if no such row exists.
	// The "before" / "after" fields are returned
	// in full (the list path elides them to keep
	// the response compact).
	GetByID(ctx context.Context, id string) (*AuditEntry, error)
}

// ErrNotFound is returned by Store implementations
// when the requested row does not exist.
var ErrNotFound = errors.New("audits: not found")

// MemoryStore is the Phase 0 default. It is safe
// for concurrent use; the audit-write path is
// read-mostly so a single sync.RWMutex around the
// entries slice is enough. The ID is a fresh
// uint64 on every Insert (atomic.AddUint64 to
// avoid holding the lock for the id mint).
type MemoryStore struct {
	mu      sync.RWMutex
	nextID  uint64
	entries []*AuditEntry
	now     func() time.Time
}

// NewMemoryStore returns an empty MemoryStore. The
// time source defaults to time.Now; tests call
// SetClock to make List filters deterministic.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		entries: make([]*AuditEntry, 0),
		now:     time.Now,
	}
}

// SetClock swaps the time source. Test-only.
func (s *MemoryStore) SetClock(now func() time.Time) { s.now = now }

// Insert appends a new entry. The createdAt and
// id fields are filled on the returned copy. The
// input Entry is treated as read-only.
func (s *MemoryStore) Insert(_ context.Context, e Entry) (*AuditEntry, error) {
	if e.Action == "" {
		return nil, fmt.Errorf("audits: Insert: action is required")
	}
	if e.ResourceType == "" {
		return nil, fmt.Errorf("audits: Insert: resource_type is required")
	}
	createdAt := e.CreatedAt
	if createdAt.IsZero() {
		createdAt = s.now().UTC()
	}
	id := atomic.AddUint64(&s.nextID, 1)
	row := &AuditEntry{
		ID:            fmt.Sprintf("%d", id),
		ActorID:       e.ActorID,
		ActorUsername: e.ActorUsername,
		Action:        e.Action,
		ResourceType:  e.ResourceType,
		ResourceID:    e.ResourceID,
		Before:        e.Before,
		After:         e.After,
		IP:            e.IP,
		UserAgent:     e.UserAgent,
		CreatedAt:     createdAt,
	}
	s.mu.Lock()
	s.entries = append(s.entries, row)
	s.mu.Unlock()
	return row, nil
}

// List returns the entries matching the filter,
// ordered by created_at DESC. The /{id} path
// returns the full Before / After blobs; the list
// path elides them by setting them to nil on the
// returned copy (saves bandwidth when the table
// grows).
func (s *MemoryStore) List(_ context.Context, f ListFilter) ([]*AuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := f.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}

	// Filter in-place. The list is already small
	// (Phase 0; MemoryStore); for the pg path the
	// WHERE is built in SQL.
	matched := make([]*AuditEntry, 0, len(s.entries))
	for _, e := range s.entries {
		if f.ActorID != "" && e.ActorID != f.ActorID {
			continue
		}
		if f.Action != "" && e.Action != f.Action {
			continue
		}
		if f.ResourceType != "" && e.ResourceType != f.ResourceType {
			continue
		}
		if f.ResourceID != "" && e.ResourceID != f.ResourceID {
			continue
		}
		if !f.Since.IsZero() && e.CreatedAt.Before(f.Since) {
			continue
		}
		if !f.Until.IsZero() && e.CreatedAt.After(f.Until) {
			continue
		}
		matched = append(matched, e)
	}

	// Sort by created_at DESC. Stable sort keeps
	// the insertion order for entries with the
	// exact same timestamp (rare but possible in
	// tests using SetClock).
	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})

	// Cap to limit + return copies (the caller may
	// mutate the slice without affecting the store).
	if len(matched) > limit {
		matched = matched[:limit]
	}
	out := make([]*AuditEntry, 0, len(matched))
	for _, e := range matched {
		cp := *e
		// Elide the bulky Before / After fields in
		// the list path. The /{id} path returns the
		// full row via GetByID.
		cp.Before = nil
		cp.After = nil
		out = append(out, &cp)
	}
	return out, nil
}

// GetByID returns the full entry (Before / After
// included). Returns ErrNotFound if no such row.
func (s *MemoryStore) GetByID(_ context.Context, id string) (*AuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.entries {
		if e.ID == id {
			cp := *e
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("id %s: %w", id, ErrNotFound)
}
