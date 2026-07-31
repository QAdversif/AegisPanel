// SPDX-License-Identifier: AGPL-3.0-or-later
//
// MemoryStore is the in-memory implementation of
// Store. It is the v0.7.0 default (selected by
// AEGIS_WEBHOOKS_BACKEND=memory) and is what the
// unit tests use. The shape mirrors MemoryStore
// in the users / plans / audits packages.

package webhooks

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is the in-memory Store. Safe for
// concurrent use; the mutex covers every field.
// The store is intentionally simple — there is no
// cache layer, no soft-delete, no fancy indexing.
// The pgx-backed PgStore is the production target.
type MemoryStore struct {
	mu sync.Mutex

	endpoints  map[uuid.UUID]*Endpoint
	deliveries map[uuid.UUID]*Delivery
	dlq        map[uuid.UUID]*DLQEntry

	// pendingRetries is the v0.7.x work queue:
	// one entry per delivery that is waiting for
	// its next attempt. The map's value is the
	// scheduled wall-clock time; the worker
	// pulls every entry with value <= now on
	// each tick.
	pendingRetries map[uuid.UUID]time.Time

	// URL index is the duplicate-detection
	// helper. The DB has a UNIQUE constraint on
	// the column; the in-memory store has to
	// enforce it itself.
	endpointsByURL map[string]uuid.UUID

	now func() time.Time
	id  func() uuid.UUID
}

// NewMemoryStore wires a MemoryStore with default
// time / uuid sources. Tests inject fixed ones via
// SetClock / SetIDGen.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		endpoints:      make(map[uuid.UUID]*Endpoint),
		deliveries:     make(map[uuid.UUID]*Delivery),
		dlq:            make(map[uuid.UUID]*DLQEntry),
		pendingRetries: make(map[uuid.UUID]time.Time),
		endpointsByURL: make(map[string]uuid.UUID),
		now:            time.Now,
		id:             uuid.New,
	}
}

// SetClock swaps the time source. Test-only.
func (s *MemoryStore) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// SetIDGen swaps the UUID source. Test-only.
func (s *MemoryStore) SetIDGen(gen func() uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id = gen
}

// --- endpoints ---------------------------------------------------------

// CreateEndpoint inserts a new endpoint. Returns
// ErrDuplicate if a row with the same URL already
// exists.
func (s *MemoryStore) CreateEndpoint(_ context.Context, e *Endpoint) error {
	if e == nil {
		return fmt.Errorf("create endpoint: nil")
	}
	if e.ID == uuid.Nil {
		return fmt.Errorf("create endpoint: zero id")
	}
	if !e.IsValid() {
		return fmt.Errorf("create endpoint: invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.endpoints[e.ID]; ok {
		return fmt.Errorf("%w: id %s", ErrDuplicate, e.ID)
	}
	if existingID, ok := s.endpointsByURL[e.URL]; ok {
		return fmt.Errorf("%w: url %q already used by %s", ErrDuplicate, e.URL, existingID)
	}
	now := s.now()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = now
	}
	// Defensive copy so caller mutations don't
	// bleed into the store.
	cp := *e
	cp.Events = append([]EventType(nil), e.Events...)
	s.endpoints[e.ID] = &cp
	s.endpointsByURL[e.URL] = e.ID
	return nil
}

// GetEndpoint returns the endpoint with the given
// id, or ErrNotFound.
func (s *MemoryStore) GetEndpoint(_ context.Context, id uuid.UUID) (*Endpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.endpoints[id]
	if !ok {
		return nil, fmt.Errorf("%w: endpoint id %s", ErrNotFound, id)
	}
	cp := *e
	cp.Events = append([]EventType(nil), e.Events...)
	return &cp, nil
}

// ListEndpoints returns every endpoint, sorted by
// CreatedAt asc.
func (s *MemoryStore) ListEndpoints(_ context.Context) ([]*Endpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Endpoint, 0, len(s.endpoints))
	for _, e := range s.endpoints {
		cp := *e
		cp.Events = append([]EventType(nil), e.Events...)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// UpdateEndpoint replaces the stored copy. Returns
// ErrNotFound if no such endpoint exists;
// ErrDuplicate if the URL rename would collide.
func (s *MemoryStore) UpdateEndpoint(_ context.Context, e *Endpoint) error {
	if e == nil {
		return fmt.Errorf("update endpoint: nil")
	}
	if e.ID == uuid.Nil {
		return fmt.Errorf("update endpoint: zero id")
	}
	if !e.IsValid() {
		return fmt.Errorf("update endpoint: invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.endpoints[e.ID]
	if !ok {
		return fmt.Errorf("%w: endpoint id %s", ErrNotFound, e.ID)
	}
	if existing.URL != e.URL {
		if dupID, dup := s.endpointsByURL[e.URL]; dup && dupID != e.ID {
			return fmt.Errorf("%w: url %q already used by %s", ErrDuplicate, e.URL, dupID)
		}
		delete(s.endpointsByURL, existing.URL)
		s.endpointsByURL[e.URL] = e.ID
	}
	e.UpdatedAt = s.now()
	cp := *e
	cp.Events = append([]EventType(nil), e.Events...)
	s.endpoints[e.ID] = &cp
	return nil
}

// DeleteEndpoint removes the endpoint. Delivery
// rows are deleted (the FK in the pg schema has
// ON DELETE CASCADE; we mirror the semantic here).
// DLQ rows are kept (the FK is logical, not
// physical — see DLQEntry doc comment).
func (s *MemoryStore) DeleteEndpoint(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.endpoints[id]
	if !ok {
		return fmt.Errorf("%w: endpoint id %s", ErrNotFound, id)
	}
	delete(s.endpoints, id)
	delete(s.endpointsByURL, e.URL)
	for did, d := range s.deliveries {
		if d.EndpointID == id {
			delete(s.deliveries, did)
		}
	}
	return nil
}

// --- deliveries --------------------------------------------------------

// CreateDelivery inserts a new delivery row.
func (s *MemoryStore) CreateDelivery(_ context.Context, d *Delivery) error {
	if d == nil {
		return fmt.Errorf("create delivery: nil")
	}
	if d.ID == uuid.Nil {
		return fmt.Errorf("create delivery: zero id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.deliveries[d.ID]; ok {
		return fmt.Errorf("%w: delivery id %s", ErrDuplicate, d.ID)
	}
	now := s.now()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	cp := *d
	cp.Payload = append([]byte(nil), d.Payload...)
	cp.RequestBody = append([]byte(nil), d.RequestBody...)
	s.deliveries[d.ID] = &cp
	return nil
}

// ListDeliveriesByEndpoint returns the delivery
// history for the given endpoint, sorted by
// CreatedAt desc.
func (s *MemoryStore) ListDeliveriesByEndpoint(_ context.Context, endpointID uuid.UUID, limit int) ([]*Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	out := make([]*Delivery, 0, limit)
	for _, d := range s.deliveries {
		if d.EndpointID != endpointID {
			continue
		}
		cp := *d
		cp.Payload = append([]byte(nil), d.Payload...)
		cp.RequestBody = append([]byte(nil), d.RequestBody...)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// --- DLQ ---------------------------------------------------------------

// EnqueueDLQ moves a failed delivery into the DLQ.
func (s *MemoryStore) EnqueueDLQ(_ context.Context, entry *DLQEntry) error {
	if entry == nil {
		return fmt.Errorf("enqueue dlq: nil")
	}
	if entry.ID == uuid.Nil {
		return fmt.Errorf("enqueue dlq: zero id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dlq[entry.ID]; ok {
		return fmt.Errorf("%w: dlq id %s", ErrDuplicate, entry.ID)
	}
	now := s.now()
	if entry.EnqueuedAt.IsZero() {
		entry.EnqueuedAt = now
	}
	cp := *entry
	cp.Payload = append([]byte(nil), entry.Payload...)
	s.dlq[entry.ID] = &cp
	return nil
}

// ListDLQ returns every DLQ entry, sorted by
// EnqueuedAt desc.
func (s *MemoryStore) ListDLQ(_ context.Context, limit int) ([]*DLQEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	out := make([]*DLQEntry, 0, limit)
	for _, e := range s.dlq {
		cp := *e
		cp.Payload = append([]byte(nil), e.Payload...)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].EnqueuedAt.After(out[j].EnqueuedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetDLQ returns the DLQ entry with the given id,
// or ErrNotFound.
func (s *MemoryStore) GetDLQ(_ context.Context, id uuid.UUID) (*DLQEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.dlq[id]
	if !ok {
		return nil, fmt.Errorf("%w: dlq id %s", ErrNotFound, id)
	}
	cp := *e
	cp.Payload = append([]byte(nil), e.Payload...)
	return &cp, nil
}

// DeleteDLQ removes a DLQ entry.
func (s *MemoryStore) DeleteDLQ(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dlq[id]; !ok {
		return fmt.Errorf("%w: dlq id %s", ErrNotFound, id)
	}
	delete(s.dlq, id)
	return nil
}

// --- pending retries (v0.7.x) -----------------------------------------

// EnqueueRetry registers a retry for the given
// delivery. Idempotent: re-enqueueing the same
// delivery_id updates the next_attempt_at.
func (s *MemoryStore) EnqueueRetry(_ context.Context, deliveryID uuid.UUID, nextAttemptAt time.Time) error {
	if deliveryID == uuid.Nil {
		return fmt.Errorf("enqueue retry: zero delivery id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingRetries[deliveryID] = nextAttemptAt
	return nil
}

// DequeueRetry removes a retry row. Idempotent:
// a no-op when the row is already gone.
func (s *MemoryStore) DequeueRetry(_ context.Context, deliveryID uuid.UUID) error {
	if deliveryID == uuid.Nil {
		return fmt.Errorf("dequeue retry: zero delivery id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pendingRetries, deliveryID)
	return nil
}

// ListDueRetries returns up to `limit` delivery
// IDs whose next_attempt_at is at or before `now`,
// ordered by next_attempt_at asc.
func (s *MemoryStore) ListDueRetries(_ context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Copy matching entries out under the lock;
	// the rest of the function is pure.
	type entry struct {
		id uuid.UUID
		ts time.Time
	}
	matches := make([]entry, 0, len(s.pendingRetries))
	for id, ts := range s.pendingRetries {
		if ts.After(now) {
			continue
		}
		matches = append(matches, entry{id: id, ts: ts})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].ts.Equal(matches[j].ts) {
			return matches[i].id.String() < matches[j].id.String()
		}
		return matches[i].ts.Before(matches[j].ts)
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]uuid.UUID, len(matches))
	for i, m := range matches {
		out[i] = m.id
	}
	return out, nil
}

// Compile-time check.
var _ Store = (*MemoryStore)(nil)

// Defensive guard: if the package is updated to
// drop the context parameter, the linter flags it.
// The blank assignment is a no-op at runtime.
var _ = errors.New
