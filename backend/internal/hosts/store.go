// SPDX-License-Identifier: AGPL-3.0-or-later

package hosts

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store is the persistence boundary for hosts. The
// interface is intentionally narrow — handlers,
// subscription services, and the future background
// reconciler all go through here, so swapping MemoryStore
// for a pgx implementation in Phase 1 is a single-file
// change (mirroring the nodes package).
type Store interface {
	// Create inserts a new host. Returns ErrDuplicate if
	// a host with the same Remark already exists.
	Create(ctx context.Context, h *Host) error
	// GetByID returns the host with the given id, or
	// ErrNotFound.
	GetByID(ctx context.Context, id uuid.UUID) (*Host, error)
	// GetByRemark returns the host with the given remark
	// (case-insensitive), or ErrNotFound. Remarks are
	// unique per the architecture.
	GetByRemark(ctx context.Context, remark string) (*Host, error)
	// List returns every host. The slice is freshly
	// allocated and safe for the caller to mutate.
	List(ctx context.Context) ([]*Host, error)
	// Update replaces the stored copy of h.ID. Returns
	// ErrNotFound if no such host exists. CreatedAt is
	// preserved; UpdatedAt is bumped.
	Update(ctx context.Context, h *Host) error
	// Delete removes the host with the given id. Returns
	// ErrNotFound if no such host exists.
	Delete(ctx context.Context, id uuid.UUID) error
}

// ErrNotFound is returned by Store implementations when the
// requested host does not exist. Wrapped with %w so callers
// can use errors.Is.
var ErrNotFound = errors.New("hosts: not found")

// ErrDuplicate is returned when a Create would violate the
// unique constraint on Remark.
var ErrDuplicate = errors.New("hosts: duplicate remark")

// ErrInvalid is the umbrella error returned by the Service
// layer for input-validation failures. The wrapped error is
// a *ValidationError carrying the offending field.
var ErrInvalid = errors.New("hosts: invalid input")

// ValidationError is a richer form of ErrInvalid. It carries
// the offending field name and a human-readable message so
// the HTTP layer can put them in the response body verbatim.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("hosts: invalid %s: %s", e.Field, e.Message)
}

func (e *ValidationError) Unwrap() error { return ErrInvalid }

// MemoryStore is the Phase 0 default Store. It is
// concurrency-safe (sync.RWMutex around the map) and
// copy-on-write so that callers holding a *Host from
// GetByID do not see a mutation when some other caller
// Updates the same host.
type MemoryStore struct {
	mu    sync.RWMutex
	byID  map[uuid.UUID]*Host
	byKey map[string]uuid.UUID // lower-cased remark -> id
	now   func() time.Time     // overridable for tests
}

// NewMemoryStore returns a fresh in-memory store. The clock
// is time.Now; tests can pass a fixed clock via SetClock.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byID:  make(map[uuid.UUID]*Host),
		byKey: make(map[string]uuid.UUID),
		now:   time.Now,
	}
}

// SetClock swaps the time source. Intended for tests only.
func (s *MemoryStore) SetClock(now func() time.Time) { s.now = now }

// Create inserts h into the store. ErrDuplicate is returned
// if the Remark or ID collides with an existing row.
// CreatedAt and UpdatedAt are stamped from s.now. The
// Service layer is expected to have validated h.IsValid
// before this call.
func (s *MemoryStore) Create(_ context.Context, h *Host) error {
	if h == nil {
		return fmt.Errorf("create: nil host")
	}
	if h.ID == uuid.Nil {
		return fmt.Errorf("create: zero id")
	}
	remarkKey := remarkKey(h.Remark)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byKey[remarkKey]; exists {
		return fmt.Errorf("remark %q: %w", h.Remark, ErrDuplicate)
	}
	if _, exists := s.byID[h.ID]; exists {
		return fmt.Errorf("id %s: %w", h.ID, ErrDuplicate)
	}
	now := s.now().UTC()
	h.CreatedAt = now
	h.UpdatedAt = now
	s.byID[h.ID] = cloneHost(h)
	s.byKey[remarkKey] = h.ID
	return nil
}

// GetByID returns the host with the given id or ErrNotFound.
func (s *MemoryStore) GetByID(_ context.Context, id uuid.UUID) (*Host, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	return cloneHost(h), nil
}

// GetByRemark returns the host with the given remark
// (case-insensitive) or ErrNotFound.
func (s *MemoryStore) GetByRemark(_ context.Context, remark string) (*Host, error) {
	key := remarkKey(remark)
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byKey[key]
	if !ok {
		return nil, fmt.Errorf("remark %q: %w", remark, ErrNotFound)
	}
	return cloneHost(s.byID[id]), nil
}

// List returns every host sorted by Priority ascending,
// then CreatedAt ascending. The subscription service relies
// on the order: hosts with the same priority are surfaced
// in subscription URLs in the order they were created.
func (s *MemoryStore) List(_ context.Context) ([]*Host, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Host, 0, len(s.byID))
	for _, h := range s.byID {
		out = append(out, cloneHost(h))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// Update replaces the stored copy of h.ID. ErrNotFound if
// the id is unknown; ErrDuplicate if the rename would
// collide with an existing host. CreatedAt is preserved;
// UpdatedAt is bumped.
func (s *MemoryStore) Update(_ context.Context, h *Host) error {
	if h == nil || h.ID == uuid.Nil {
		return fmt.Errorf("update: missing id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.byID[h.ID]
	if !ok {
		return fmt.Errorf("id %s: %w", h.ID, ErrNotFound)
	}
	// If the caller renamed the host, the unique index on
	// Remark must still hold. We compare the desired
	// remark against the current key and reject
	// collisions.
	newKey := remarkKey(h.Remark)
	if newKey != remarkKey(existing.Remark) {
		if _, conflict := s.byKey[newKey]; conflict {
			return fmt.Errorf("remark %q: %w", h.Remark, ErrDuplicate)
		}
		delete(s.byKey, remarkKey(existing.Remark))
		s.byKey[newKey] = h.ID
	}
	h.CreatedAt = existing.CreatedAt
	h.UpdatedAt = s.now().UTC()
	s.byID[h.ID] = cloneHost(h)
	return nil
}

// Delete removes the host with the given id. Returns
// ErrNotFound if no such host exists.
func (s *MemoryStore) Delete(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	delete(s.byID, id)
	delete(s.byKey, remarkKey(h.Remark))
	return nil
}

// cloneHost returns a deep-enough copy that the caller can
// mutate the returned struct without affecting the stored
// copy. Slices are duplicated; the Endpoint slice is
// duplicated element-by-element so that mutating an
// Endpoint's override fields in the returned copy does not
// leak back.
func cloneHost(h *Host) *Host {
	out := *h
	if h.Endpoints != nil {
		out.Endpoints = make([]Endpoint, len(h.Endpoints))
		for i, ep := range h.Endpoints {
			out.Endpoints[i] = cloneEndpoint(ep)
		}
	}
	if h.StatusFilter != nil {
		out.StatusFilter = make([]UserStatus, len(h.StatusFilter))
		copy(out.StatusFilter, h.StatusFilter)
	}
	if h.Tags != nil {
		out.Tags = make([]string, len(h.Tags))
		copy(out.Tags, h.Tags)
	}
	if h.Balancer != nil {
		b := *h.Balancer
		if h.Balancer.FailoverEndpointIDs != nil {
			b.FailoverEndpointIDs = make([]uuid.UUID, len(h.Balancer.FailoverEndpointIDs))
			copy(b.FailoverEndpointIDs, h.Balancer.FailoverEndpointIDs)
		}
		out.Balancer = &b
	}
	return &out
}

func cloneEndpoint(e Endpoint) Endpoint {
	out := e
	if e.Address != nil {
		out.Address = make([]string, len(e.Address))
		copy(out.Address, e.Address)
	}
	if e.SNI != nil {
		out.SNI = make([]string, len(e.SNI))
		copy(out.SNI, e.SNI)
	}
	if e.Host != nil {
		out.Host = make([]string, len(e.Host))
		copy(out.Host, e.Host)
	}
	if e.Port != nil {
		p := *e.Port
		out.Port = &p
	}
	if e.DownloadHostID != nil {
		id := *e.DownloadHostID
		out.DownloadHostID = &id
	}
	return out
}

// remarkKey normalises a host remark to the key the
// store indexes on. Lower-cased so the UI does not have
// to enforce case uniqueness.
func remarkKey(remark string) string {
	// We intentionally do not trim spaces here — the
	// Service layer rejects leading / trailing whitespace
	// and the store sees the canonical form. Lower-casing
	// is the only normalisation the index needs.
	return toLowerASCII(remark)
}

// toLowerASCII is a tiny stdlib-free lowercase so the
// store package has no dependency on strings. ASCII-only
// is intentional: the service validator already enforces
// a printable-ASCII remark.
func toLowerASCII(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
