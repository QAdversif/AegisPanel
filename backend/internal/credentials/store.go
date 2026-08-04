// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Store interface + MemoryStore for the
// `user_inbound_credentials` table. The pgx-backed
// PgStore lives in `pg_store.go`. The pattern mirrors
// the rest of the v0.7.x packages: a small Store
// interface, a Phase 0 in-process MemoryStore, a
// Phase 1 PgStore selected by
// AEGIS_CREDENTIALS_BACKEND=pg.

package credentials

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned by Store implementations
// when the requested row does not exist.
var ErrNotFound = errors.New("credentials: not found")

// ErrDuplicate is returned by Store implementations
// when an insert would violate the UNIQUE
// (user_id, inbound_id) constraint. A duplicate
// means a credential already exists for that
// (user, inbound) pair — the caller should
// `Rotate` instead of `Insert`.
var ErrDuplicate = errors.New("credentials: duplicate (user, inbound) pair")

// Store is the persistence boundary for the
// `user_inbound_credentials` table. The interface
// is intentionally small: only the write path the
// Service needs (Insert / Update / Delete) and the
// read paths the follow-up PRs will need
// (GetByID / ListByUser / ListByInbound).
type Store interface {
	// Insert appends a new credential. The store
	// fills the `id`, `created_at`, `updated_at`
	// fields on the returned copy (the input is
	// treated as read-only by convention). Returns
	// ErrDuplicate on a UNIQUE (user_id, inbound_id)
	// violation.
	Insert(ctx context.Context, c Credential) (*Credential, error)

	// Update changes the credential_value of an
	// existing row, re-stamping updated_at. Returns
	// ErrNotFound if the id does not exist.
	Update(ctx context.Context, id uuid.UUID, newValue string) (*Credential, error)

	// Delete removes a row by id. Returns
	// ErrNotFound if the id does not exist.
	Delete(ctx context.Context, id uuid.UUID) error

	// GetByID returns the full row. Returns
	// ErrNotFound if the id does not exist.
	GetByID(ctx context.Context, id uuid.UUID) (*Credential, error)

	// ListByUser returns every credential for the
	// given user, ordered by inbound_id (stable,
	// deterministic, helpful for tests).
	ListByUser(ctx context.Context, userID uuid.UUID) ([]*Credential, error)

	// ListByInbound returns every credential for
	// the given inbound, ordered by user_id
	// (the multi-user renderer's primary access
	// pattern: "for this inbound, who's allowed?").
	ListByInbound(ctx context.Context, inboundID uuid.UUID) ([]*Credential, error)

	// ListAll returns every credential in the
	// store, ordered by user_id then inbound_id
	// (the cross-user admin table's primary
	// access pattern: "what credentials exist
	// across the panel?"). The MemoryStore walks
	// the rows map; the PgStore issues a single
	// SELECT with the same ordering.
	ListAll(ctx context.Context) ([]*Credential, error)
}

// MemoryStore is the Phase 0 default. It is safe
// for concurrent use; the credential-write path
// is read-mostly so a single sync.RWMutex is enough.
// The id is a fresh uuid (uuid.New) on every Insert;
// the timestamp is filled from the configured clock
// (defaults to time.Now; tests inject a fixed clock
// via SetClock).
type MemoryStore struct {
	mu     sync.RWMutex
	nextID uint64 // not used; the id is a uuid (uuid.New). Kept for the symmetry with the v0.6.0 pattern; safe to remove in a future PR.
	rows   map[uuid.UUID]*Credential
	byUser map[uuid.UUID][]uuid.UUID // user_id -> list of credential ids
	byInb  map[uuid.UUID][]uuid.UUID // inbound_id -> list of credential ids
	now    func() time.Time
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		rows:   make(map[uuid.UUID]*Credential),
		byUser: make(map[uuid.UUID][]uuid.UUID),
		byInb:  make(map[uuid.UUID][]uuid.UUID),
		now:    time.Now,
	}
}

// SetClock swaps the time source. Test-only.
func (s *MemoryStore) SetClock(now func() time.Time) { s.now = now }

// Insert appends a new credential. The id, createdAt,
// and updatedAt fields are filled on the returned
// copy. The input is treated as read-only.
//
// The MemoryStore's UNIQUE check is in-memory (the
// PgStore relies on the SQL UNIQUE constraint from
// migration 0019). A duplicate (user_id, inbound_id)
// pair returns ErrDuplicate.
func (s *MemoryStore) Insert(_ context.Context, c Credential) (*Credential, error) {
	if c.UserID == uuid.Nil {
		return nil, errors.New("credentials: Insert: user_id is required")
	}
	if c.InboundID == uuid.Nil {
		return nil, errors.New("credentials: Insert: inbound_id is required")
	}
	if c.CredentialValue == "" {
		return nil, errors.New("credentials: Insert: credential_value is required")
	}
	now := s.now().UTC()
	row := &Credential{
		ID:              uuid.New(),
		UserID:          c.UserID,
		InboundID:       c.InboundID,
		CredentialValue: c.CredentialValue,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.byUser[c.UserID] {
		if existingRow, ok := s.rows[existing]; ok && existingRow.InboundID == c.InboundID {
			return nil, ErrDuplicate
		}
	}
	s.rows[row.ID] = row
	s.byUser[c.UserID] = append(s.byUser[c.UserID], row.ID)
	s.byInb[c.InboundID] = append(s.byInb[c.InboundID], row.ID)
	_ = atomic.AddUint64(&s.nextID, 1) // mirror the v0.6.0 atomic pattern; not strictly needed for uuids
	return row, nil
}

// Update changes the credential_value of an existing
// row. The updated_at field is re-stamped. Returns
// ErrNotFound if the id does not exist.
func (s *MemoryStore) Update(_ context.Context, id uuid.UUID, newValue string) (*Credential, error) {
	if newValue == "" {
		return nil, errors.New("credentials: Update: credential_value is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return nil, ErrNotFound
	}
	row.CredentialValue = newValue
	row.UpdatedAt = s.now().UTC()
	cp := *row
	return &cp, nil
}

// Delete removes a row by id. Returns ErrNotFound
// if the id does not exist.
func (s *MemoryStore) Delete(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.rows, id)
	s.byUser[row.UserID] = removeID(s.byUser[row.UserID], id)
	s.byInb[row.InboundID] = removeID(s.byInb[row.InboundID], id)
	return nil
}

// GetByID returns the full row.
func (s *MemoryStore) GetByID(_ context.Context, id uuid.UUID) (*Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.rows[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *row
	return &cp, nil
}

// ListByUser returns every credential for the user,
// sorted by inbound_id ascending (stable, deterministic,
// friendly to the subscription resolver that calls
// this with a known user_id).
func (s *MemoryStore) ListByUser(_ context.Context, userID uuid.UUID) ([]*Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := s.byUser[userID]
	out := make([]*Credential, 0, len(ids))
	for _, id := range ids {
		if row, ok := s.rows[id]; ok {
			cp := *row
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].InboundID.String() != out[j].InboundID.String() {
			return out[i].InboundID.String() < out[j].InboundID.String()
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	return out, nil
}

// ListByInbound returns every credential for the
// inbound, sorted by user_id ascending (the
// multi-user renderer's primary access pattern).
func (s *MemoryStore) ListByInbound(_ context.Context, inboundID uuid.UUID) ([]*Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := s.byInb[inboundID]
	out := make([]*Credential, 0, len(ids))
	for _, id := range ids {
		if row, ok := s.rows[id]; ok {
			cp := *row
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UserID.String() != out[j].UserID.String() {
			return out[i].UserID.String() < out[j].UserID.String()
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	return out, nil
}

// ListAll returns every credential in the store,
// sorted by (user_id, inbound_id) ascending. The
// MemoryStore walks the rows map (Phase 0 has 1-3
// users; the walk is fine).
func (s *MemoryStore) ListAll(_ context.Context) ([]*Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Credential, 0, len(s.rows))
	for _, row := range s.rows {
		cp := *row
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UserID.String() != out[j].UserID.String() {
			return out[i].UserID.String() < out[j].UserID.String()
		}
		if out[i].InboundID.String() != out[j].InboundID.String() {
			return out[i].InboundID.String() < out[j].InboundID.String()
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	return out, nil
}

// removeID returns a new slice with id removed.
// O(n) but n is the per-(user|inbound) row count,
// which is bounded by the panel's user base
// (small for Phase 0 / MemoryStore; the PgStore
// does the equivalent in SQL with the FK index).
func removeID(ids []uuid.UUID, id uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, x := range ids {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}
