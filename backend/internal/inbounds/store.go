// SPDX-License-Identifier: AGPL-3.0-or-later

package inbounds

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store is the persistence boundary for inbounds. The
// interface is intentionally narrow — handlers, the
// host manager, and the future agent gRPC client all go
// through here, so swapping MemoryStore for a pgx
// implementation in Phase 1.1 is a single-file change
// (mirroring the nodes package).
type Store interface {
	// Create inserts a new inbound. Returns ErrDuplicate
	// if a row with the same (NodeID, Name) already
	// exists, or if a row with the same
	// (NodeID, ListenPort) already exists.
	Create(ctx context.Context, i *Inbound) error
	// GetByID returns the inbound with the given id, or
	// ErrNotFound.
	GetByID(ctx context.Context, id uuid.UUID) (*Inbound, error)
	// GetByNodeAndName returns the inbound with the
	// given (NodeID, Name), or ErrNotFound. The pair
	// is unique per the migration's UNIQUE
	// (node_id, name) constraint.
	GetByNodeAndName(ctx context.Context, nodeID uuid.UUID, name string) (*Inbound, error)
	// GetByNodeAndPort returns the inbound with the
	// given (NodeID, ListenPort), or ErrNotFound. The
	// pair is unique per the migration's UNIQUE
	// (node_id, listen_port) constraint.
	GetByNodeAndPort(ctx context.Context, nodeID uuid.UUID, port int) (*Inbound, error)
	// ListByNode returns every inbound belonging to
	// the given node, sorted by ListenPort ascending.
	// The slice is freshly allocated; callers may
	// mutate it.
	ListByNode(ctx context.Context, nodeID uuid.UUID) ([]*Inbound, error)
	// ListByProtocol returns every inbound with the
	// given protocol across all nodes, sorted by
	// NodeID then ListenPort ascending. Used by the
	// admin UI's "show me all VLESS inbounds" view.
	ListByProtocol(ctx context.Context, p Protocol) ([]*Inbound, error)
	// Update replaces the stored copy of i.ID. Returns
	// ErrNotFound if no such inbound exists; ErrDuplicate
	// if the rename or port change would collide with
	// an existing row.
	Update(ctx context.Context, i *Inbound) error
	// Delete removes the inbound with the given id.
	// Returns ErrNotFound if no such inbound exists.
	Delete(ctx context.Context, id uuid.UUID) error
}

// ErrNotFound is returned by Store implementations when
// the requested inbound does not exist. Wrapped with %w
// so callers can use errors.Is.
var ErrNotFound = errors.New("inbounds: not found")

// ErrDuplicate is returned when a Create or Update would
// violate one of the unique constraints:
//
//   - (node_id, name) — see migration 0003.
//   - (node_id, listen_port) — see migration 0003.
//
// The wrapped error message includes the offending pair
// so the handler can put it in the 409 response body.
var ErrDuplicate = errors.New("inbounds: duplicate")

// ErrInvalid is the umbrella error returned by the
// Service layer for input-validation failures. The
// wrapped error is a *ValidationError carrying the
// offending field.
var ErrInvalid = errors.New("inbounds: invalid input")

// ValidationError is a richer form of ErrInvalid. It
// carries the offending field name and a human-readable
// message so the HTTP layer can put them in the
// response body verbatim.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("inbounds: invalid %s: %s", e.Field, e.Message)
}

func (e *ValidationError) Unwrap() error { return ErrInvalid }

// MemoryStore is the Phase 0 default Store. It is
// concurrency-safe (sync.RWMutex around the maps) and
// copy-on-write so that callers holding an *Inbound
// from GetByID do not see a mutation when some other
// caller Updates the same inbound.
type MemoryStore struct {
	mu         sync.RWMutex
	byID       map[uuid.UUID]*Inbound
	byNodeKey  map[nodeKey]*Inbound     // (nodeID, name) -> inbound
	byNodePort map[nodePortKey]*Inbound // (nodeID, port) -> inbound
	now        func() time.Time
}

// nodeKey is the (NodeID, Name) pair that uniquely
// identifies an inbound's name slot on a node.
type nodeKey struct {
	nodeID uuid.UUID
	name   string
}

// nodePortKey is the (NodeID, ListenPort) pair that
// uniquely identifies an inbound's port slot on a
// node.
type nodePortKey struct {
	nodeID uuid.UUID
	port   int
}

// NewMemoryStore returns a fresh in-memory store. The
// clock is time.Now; tests can pass a fixed clock via
// SetClock.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byID:       make(map[uuid.UUID]*Inbound),
		byNodeKey:  make(map[nodeKey]*Inbound),
		byNodePort: make(map[nodePortKey]*Inbound),
		now:        time.Now,
	}
}

// SetClock swaps the time source. Intended for tests
// only.
func (s *MemoryStore) SetClock(now func() time.Time) { s.now = now }

// Create inserts i into the store. ErrDuplicate is
// returned if the (NodeID, Name) or (NodeID, ListenPort)
// pair collides with an existing row, or if the ID
// collides. CreatedAt and UpdatedAt are stamped from
// s.now. The Service layer is expected to have
// validated i.IsValid before this call.
func (s *MemoryStore) Create(_ context.Context, i *Inbound) error {
	if i == nil {
		return fmt.Errorf("create: nil inbound")
	}
	if i.ID == uuid.Nil {
		return fmt.Errorf("create: zero id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	nameKey := nodeKey{nodeID: i.NodeID, name: i.Name}
	portKey := nodePortKey{nodeID: i.NodeID, port: i.ListenPort}
	if _, exists := s.byNodeKey[nameKey]; exists {
		return fmt.Errorf("node %s name %q: %w", i.NodeID, i.Name, ErrDuplicate)
	}
	if _, exists := s.byNodePort[portKey]; exists {
		return fmt.Errorf("node %s port %d: %w", i.NodeID, i.ListenPort, ErrDuplicate)
	}
	if _, exists := s.byID[i.ID]; exists {
		return fmt.Errorf("id %s: %w", i.ID, ErrDuplicate)
	}
	now := s.now().UTC()
	i.CreatedAt = now
	i.UpdatedAt = now
	s.byID[i.ID] = cloneInbound(i)
	s.byNodeKey[nameKey] = i
	s.byNodePort[portKey] = i
	return nil
}

// GetByID returns the inbound with the given id or
// ErrNotFound.
func (s *MemoryStore) GetByID(_ context.Context, id uuid.UUID) (*Inbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	return cloneInbound(i), nil
}

// GetByNodeAndName returns the inbound with the given
// (NodeID, Name) or ErrNotFound.
func (s *MemoryStore) GetByNodeAndName(_ context.Context, nodeID uuid.UUID, name string) (*Inbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.byNodeKey[nodeKey{nodeID: nodeID, name: name}]
	if !ok {
		return nil, fmt.Errorf("node %s name %q: %w", nodeID, name, ErrNotFound)
	}
	return cloneInbound(i), nil
}

// GetByNodeAndPort returns the inbound with the given
// (NodeID, ListenPort) or ErrNotFound.
func (s *MemoryStore) GetByNodeAndPort(_ context.Context, nodeID uuid.UUID, port int) (*Inbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.byNodePort[nodePortKey{nodeID: nodeID, port: port}]
	if !ok {
		return nil, fmt.Errorf("node %s port %d: %w", nodeID, port, ErrNotFound)
	}
	return cloneInbound(i), nil
}

// ListByNode returns every inbound belonging to the
// given node, sorted by ListenPort ascending. The
// returned slice is freshly allocated and safe for the
// caller to mutate.
func (s *MemoryStore) ListByNode(_ context.Context, nodeID uuid.UUID) ([]*Inbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Inbound, 0)
	for _, i := range s.byID {
		if i.NodeID != nodeID {
			continue
		}
		out = append(out, cloneInbound(i))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ListenPort != out[j].ListenPort {
			return out[i].ListenPort < out[j].ListenPort
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ListByProtocol returns every inbound with the given
// protocol across all nodes, sorted by NodeID then
// ListenPort ascending.
func (s *MemoryStore) ListByProtocol(_ context.Context, p Protocol) ([]*Inbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Inbound, 0)
	for _, i := range s.byID {
		if i.Protocol != p {
			continue
		}
		out = append(out, cloneInbound(i))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID.String() < out[j].NodeID.String()
		}
		if out[i].ListenPort != out[j].ListenPort {
			return out[i].ListenPort < out[j].ListenPort
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Update replaces the stored copy of i.ID. ErrNotFound
// if the id is unknown; ErrDuplicate if the rename or
// port change would collide with an existing row.
// CreatedAt is preserved; UpdatedAt is bumped.
func (s *MemoryStore) Update(_ context.Context, i *Inbound) error {
	if i == nil || i.ID == uuid.Nil {
		return fmt.Errorf("update: missing id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.byID[i.ID]
	if !ok {
		return fmt.Errorf("id %s: %w", i.ID, ErrNotFound)
	}
	oldNameKey := nodeKey{nodeID: existing.NodeID, name: existing.Name}
	oldPortKey := nodePortKey{nodeID: existing.NodeID, port: existing.ListenPort}
	newNameKey := nodeKey{nodeID: i.NodeID, name: i.Name}
	newPortKey := nodePortKey{nodeID: i.NodeID, port: i.ListenPort}
	// Validate the rename. We allow the inbound to
	// keep its own name / port (i.e. newKey == oldKey
	// is a no-op for the index). Otherwise we check
	// the candidate key for collisions.
	if newNameKey != oldNameKey {
		if _, conflict := s.byNodeKey[newNameKey]; conflict {
			return fmt.Errorf("node %s name %q: %w", i.NodeID, i.Name, ErrDuplicate)
		}
		delete(s.byNodeKey, oldNameKey)
		s.byNodeKey[newNameKey] = i
	}
	if newPortKey != oldPortKey {
		if _, conflict := s.byNodePort[newPortKey]; conflict {
			return fmt.Errorf("node %s port %d: %w", i.NodeID, i.ListenPort, ErrDuplicate)
		}
		delete(s.byNodePort, oldPortKey)
		s.byNodePort[newPortKey] = i
	}
	i.CreatedAt = existing.CreatedAt
	i.UpdatedAt = s.now().UTC()
	s.byID[i.ID] = cloneInbound(i)
	return nil
}

// Delete removes the inbound with the given id. Returns
// ErrNotFound if no such inbound exists.
func (s *MemoryStore) Delete(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	delete(s.byID, id)
	delete(s.byNodeKey, nodeKey{nodeID: i.NodeID, name: i.Name})
	delete(s.byNodePort, nodePortKey{nodeID: i.NodeID, port: i.ListenPort})
	return nil
}

// cloneInbound returns a deep-enough copy that the
// caller can mutate the returned struct without
// affecting the stored copy. Slices and the Params map
// are duplicated; everything else is value-typed.
func cloneInbound(i *Inbound) *Inbound {
	out := *i
	if i.Tags != nil {
		out.Tags = make([]string, len(i.Tags))
		copy(out.Tags, i.Tags)
	}
	if i.Params != nil {
		out.Params = make(map[string]any, len(i.Params))
		for k, v := range i.Params {
			out.Params[k] = v
		}
	}
	return &out
}
