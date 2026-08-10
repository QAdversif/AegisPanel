// SPDX-License-Identifier: AGPL-3.0-or-later

package inboundtemplates

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store is the persistence boundary for
// inbound templates. The interface is intentionally
// narrow — handlers, the future sing-box renderer
// (which expands template_id -> params at apply
// time), and any other consumer go through here, so
// swapping MemoryStore for a pgx implementation in
// Phase 1.1 is a single-file change (mirroring the
// inbounds package).
type Store interface {
	// Create inserts a new template. Returns
	// ErrDuplicate if a row with the same Name
	// already exists, or if the ID collides.
	Create(ctx context.Context, t *InboundTemplate) error
	// GetByID returns the template with the given
	// id, or ErrNotFound.
	GetByID(ctx context.Context, id uuid.UUID) (*InboundTemplate, error)
	// GetByName returns the template with the given
	// name, or ErrNotFound. The name is unique per
	// the migration's UNIQUE (name) constraint.
	GetByName(ctx context.Context, name string) (*InboundTemplate, error)
	// List returns every template, sorted by Name
	// ascending. The slice is freshly allocated;
	// callers may mutate it.
	List(ctx context.Context) ([]*InboundTemplate, error)
	// v0.8.13+ builder integration: GetManyByID
	// returns a map keyed by template id with the
	// template's Params. Template ids that don't
	// resolve are omitted from the result (the
	// caller treats a missing entry as "use the
	// inbound's inline params"). A nil map + nil
	// error is a valid empty result; an empty input
	// slice returns an empty (not nil) map. The
	// builder calls this once per flush with the
	// deduplicated TemplateIDs of every inbound on
	// the node — the lookup is O(1) per template
	// via the byID map.
	GetManyByID(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*InboundTemplate, error)
	// ListByProtocol returns every template with
	// the given protocol across the panel, sorted
	// by Name ascending. Used by the admin UI's
	// "show me all VLESS templates" view and the
	// inbound-create form's "template" dropdown.
	ListByProtocol(ctx context.Context, p Protocol) ([]*InboundTemplate, error)
	// Update replaces the stored copy of t.ID.
	// Returns ErrNotFound if no such template
	// exists; ErrDuplicate if the rename would
	// collide with an existing row.
	Update(ctx context.Context, t *InboundTemplate) error
	// Delete removes the template with the given id.
	// Returns ErrNotFound if no such template
	// exists. Note: deleting a template does NOT
	// cascade-delete the inbounds that reference
	// it — the FK is ON DELETE SET NULL, so the
	// inbounds fall back to the inline-params path.
	Delete(ctx context.Context, id uuid.UUID) error
}

// ErrNotFound is returned by Store implementations
// when the requested template does not exist.
// Wrapped with %w so callers can use errors.Is.
var ErrNotFound = errors.New("inboundtemplates: not found")

// ErrDuplicate is returned when a Create or Update
// would violate the UNIQUE (name) constraint.
// The wrapped error message includes the offending
// name so the handler can put it in the 409
// response body.
var ErrDuplicate = errors.New("inboundtemplates: duplicate")

// ErrInvalid is the umbrella error returned by the
// Service layer for input-validation failures. The
// wrapped error is a *ValidationError carrying the
// offending field.
var ErrInvalid = errors.New("inboundtemplates: invalid input")

// MemoryStore is the Phase 0 default Store. It is
// concurrency-safe (sync.RWMutex around the maps)
// and copy-on-write so that callers holding an
// *InboundTemplate from GetByID do not see a mutation
// when some other caller Updates the same template.
type MemoryStore struct {
	mu    sync.RWMutex
	byID  map[uuid.UUID]*InboundTemplate
	byKey map[string]*InboundTemplate // name -> template
	now   func() time.Time
}

// NewMemoryStore returns a fresh in-memory store.
// The clock is time.Now; tests can pass a fixed
// clock via SetClock.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byID:  make(map[uuid.UUID]*InboundTemplate),
		byKey: make(map[string]*InboundTemplate),
		now:   time.Now,
	}
}

// SetClock swaps the time source. Intended for
// tests only.
func (s *MemoryStore) SetClock(now func() time.Time) { s.now = now }

// Create inserts t into the store. ErrDuplicate is
// returned if the (Name) collides with an existing
// row, or if the ID collides. CreatedAt and
// UpdatedAt are stamped from s.now. The Service
// layer is expected to have validated t.IsValid
// before this call.
func (s *MemoryStore) Create(_ context.Context, t *InboundTemplate) error {
	if t == nil {
		return fmt.Errorf("create: nil template")
	}
	if t.ID == uuid.Nil {
		return fmt.Errorf("create: zero id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byKey[t.Name]; exists {
		return fmt.Errorf("name %q: %w", t.Name, ErrDuplicate)
	}
	if _, exists := s.byID[t.ID]; exists {
		return fmt.Errorf("id %s: %w", t.ID, ErrDuplicate)
	}
	now := s.now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now
	s.byID[t.ID] = cloneTemplate(t)
	s.byKey[t.Name] = t
	return nil
}

// GetByID returns the template with the given id or
// ErrNotFound.
func (s *MemoryStore) GetByID(_ context.Context, id uuid.UUID) (*InboundTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	return cloneTemplate(t), nil
}

// GetByName returns the template with the given name
// or ErrNotFound.
func (s *MemoryStore) GetByName(_ context.Context, name string) (*InboundTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.byKey[name]
	if !ok {
		return nil, fmt.Errorf("name %q: %w", name, ErrNotFound)
	}
	return cloneTemplate(t), nil
}

// List returns every template, sorted by Name
// ascending.
func (s *MemoryStore) List(_ context.Context) ([]*InboundTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*InboundTemplate, 0, len(s.byID))
	for _, t := range s.byID {
		out = append(out, cloneTemplate(t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetManyByID returns a map keyed by template id with
// the matching *InboundTemplate. Template ids that
// don't resolve are omitted from the result. An empty
// `ids` slice returns an empty (not nil) map. The
// builder's per-flush call goes through this method.
func (s *MemoryStore) GetManyByID(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*InboundTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[uuid.UUID]*InboundTemplate, len(ids))
	for _, id := range ids {
		if t, ok := s.byID[id]; ok {
			out[id] = cloneTemplate(t)
		}
	}
	return out, nil
}

// ListByProtocol returns every template with the
// given protocol, sorted by Name ascending.
func (s *MemoryStore) ListByProtocol(_ context.Context, p Protocol) ([]*InboundTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*InboundTemplate, 0)
	for _, t := range s.byID {
		if t.Protocol != p {
			continue
		}
		out = append(out, cloneTemplate(t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Update replaces the stored copy of t.ID.
// ErrNotFound if the id is unknown; ErrDuplicate if
// the rename would collide with an existing row.
// CreatedAt is preserved; UpdatedAt is bumped.
func (s *MemoryStore) Update(_ context.Context, t *InboundTemplate) error {
	if t == nil || t.ID == uuid.Nil {
		return fmt.Errorf("update: missing id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.byID[t.ID]
	if !ok {
		return fmt.Errorf("id %s: %w", t.ID, ErrNotFound)
	}
	oldKey := existing.Name
	newKey := t.Name
	if newKey != oldKey {
		if _, conflict := s.byKey[newKey]; conflict {
			return fmt.Errorf("name %q: %w", t.Name, ErrDuplicate)
		}
		delete(s.byKey, oldKey)
		s.byKey[newKey] = t
	}
	t.CreatedAt = existing.CreatedAt
	t.UpdatedAt = s.now().UTC()
	s.byID[t.ID] = cloneTemplate(t)
	return nil
}

// Delete removes the template with the given id.
// Returns ErrNotFound if no such template exists.
func (s *MemoryStore) Delete(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	delete(s.byID, id)
	delete(s.byKey, t.Name)
	return nil
}

// cloneTemplate returns a deep-enough copy that the
// caller can mutate the returned struct without
// affecting the stored copy. The Params map is
// duplicated; everything else is value-typed.
func cloneTemplate(t *InboundTemplate) *InboundTemplate {
	out := *t
	if t.Params != nil {
		out.Params = make(map[string]any, len(t.Params))
		for k, v := range t.Params {
			out.Params[k] = v
		}
	}
	return &out
}
