// SPDX-License-Identifier: AGPL-3.0-or-later

package nodes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store is the persistence boundary for nodes. The interface is
// intentionally narrow — anything that touches the wire (HTTP
// handlers, background reconciliation, the agent gRPC server) goes
// through here, so swapping MemoryStore for a pgx implementation
// in Phase 1 is a single-file change.
type Store interface {
	// Create inserts a new node. Returns ErrDuplicate if a node
	// with the same Name already exists.
	Create(ctx context.Context, n *Node) error
	// GetByID returns the node with the given id, or ErrNotFound.
	GetByID(ctx context.Context, id uuid.UUID) (*Node, error)
	// GetByName returns the node with the given name, or
	// ErrNotFound. Names are unique per the migration.
	GetByName(ctx context.Context, name string) (*Node, error)
	// List returns every node, sorted by CreatedAt ascending.
	// The slice is freshly allocated; callers may mutate it.
	List(ctx context.Context) ([]*Node, error)
	// Update replaces the stored copy of n.ID. Returns ErrNotFound
	// if no such node exists. CreatedAt is preserved; UpdatedAt
	// is bumped.
	Update(ctx context.Context, n *Node) error
	// Delete removes the node with the given id. Returns
	// ErrNotFound if no such node exists.
	Delete(ctx context.Context, id uuid.UUID) error
}

// ErrNotFound is returned by Store implementations when the
// requested node does not exist. Wrapped with %w so callers can
// errors.Is.
var ErrNotFound = errors.New("nodes: not found")

// ErrDuplicate is returned when a Create would violate the
// unique constraint on Name.
var ErrDuplicate = errors.New("nodes: duplicate name")

// ErrInvalid is returned by the Service layer when input validation
// fails. The wrapped error includes the offending field.
var ErrInvalid = errors.New("nodes: invalid input")

// ValidationError is a richer form of ErrInvalid. It carries the
// offending field name and a human-readable message so the HTTP
// layer can put them in the response body verbatim.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("nodes: invalid %s: %s", e.Field, e.Message)
}

func (e *ValidationError) Unwrap() error { return ErrInvalid }

// MemoryStore is the Phase 0 default Store. It is concurrency-safe
// (sync.RWMutex around the map) and copy-on-write so that
// callers holding a *Node from GetByID do not see a mutation when
// some other caller Updates the same node.
type MemoryStore struct {
	mu    sync.RWMutex
	byID  map[uuid.UUID]*Node
	byKey map[string]uuid.UUID // name -> id, enforces uniqueness
	now   func() time.Time     // overridable for tests
}

// NewMemoryStore returns a fresh in-memory store. The now func is
// time.Now; tests can pass a fixed clock to make timestamps
// deterministic.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byID:  make(map[uuid.UUID]*Node),
		byKey: make(map[string]uuid.UUID),
		now:   time.Now,
	}
}

// SetClock swaps the time source. Intended for tests only.
func (s *MemoryStore) SetClock(now func() time.Time) { s.now = now }

func (s *MemoryStore) Create(_ context.Context, n *Node) error {
	if n == nil {
		return fmt.Errorf("create: nil node")
	}
	if n.ID == uuid.Nil {
		return fmt.Errorf("create: zero id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byKey[n.Name]; exists {
		return fmt.Errorf("name %q: %w", n.Name, ErrDuplicate)
	}
	if _, exists := s.byID[n.ID]; exists {
		return fmt.Errorf("id %s: %w", n.ID, ErrDuplicate)
	}
	now := s.now().UTC()
	n.CreatedAt = now
	n.UpdatedAt = now
	s.byID[n.ID] = cloneNode(n)
	s.byKey[n.Name] = n.ID
	return nil
}

func (s *MemoryStore) GetByID(_ context.Context, id uuid.UUID) (*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	return cloneNode(n), nil
}

func (s *MemoryStore) GetByName(_ context.Context, name string) (*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byKey[name]
	if !ok {
		return nil, fmt.Errorf("name %q: %w", name, ErrNotFound)
	}
	return cloneNode(s.byID[id]), nil
}

func (s *MemoryStore) List(_ context.Context) ([]*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Node, 0, len(s.byID))
	for _, n := range s.byID {
		out = append(out, cloneNode(n))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *MemoryStore) Update(_ context.Context, n *Node) error {
	if n == nil || n.ID == uuid.Nil {
		return fmt.Errorf("update: missing id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.byID[n.ID]
	if !ok {
		return fmt.Errorf("id %s: %w", n.ID, ErrNotFound)
	}
	// If the caller renamed the node, the unique index on Name
	// must still hold. We compare the desired name against the
	// current key and reject collisions.
	if n.Name != existing.Name {
		if _, conflict := s.byKey[n.Name]; conflict {
			return fmt.Errorf("name %q: %w", n.Name, ErrDuplicate)
		}
		delete(s.byKey, existing.Name)
		s.byKey[n.Name] = n.ID
	}
	n.CreatedAt = existing.CreatedAt
	n.UpdatedAt = s.now().UTC()
	s.byID[n.ID] = cloneNode(n)
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	delete(s.byID, id)
	delete(s.byKey, n.Name)
	return nil
}

// cloneNode returns a deep-enough copy that the caller can mutate
// the returned struct without affecting the stored copy. The
// Tags slice is duplicated; everything else is value-typed.
func cloneNode(n *Node) *Node {
	out := *n
	if n.Tags != nil {
		out.Tags = make([]string, len(n.Tags))
		copy(out.Tags, n.Tags)
	}
	return &out
}
