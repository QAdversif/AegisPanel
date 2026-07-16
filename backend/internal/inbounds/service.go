// SPDX-License-Identifier: AGPL-3.0-or-later

package inbounds

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// Service is the business-logic layer on top of Store.
// It owns:
//
//   - input validation (name, protocol allow-list,
//     port range, listen address format, params shape);
//   - cross-entity validation (every inbound's
//     NodeID must resolve to a known node);
//   - ID / timestamp generation on Create;
//   - the default Listen normalisation.
//
// Handlers call Service rather than Store directly so
// the rules stay in one place and the pgx migration in
// Phase 1.1 can swap the Store without touching
// validation.
type Service struct {
	store Store
	nodes *nodes.Service
	now   func() time.Time
}

// NewService wires a Service around the given store.
// The nodes service is required: every inbound must
// reference a real node, and the only way to check that
// is via nodes.Service.
func NewService(store Store, nodesSvc *nodes.Service) *Service {
	return &Service{store: store, nodes: nodesSvc, now: time.Now}
}

// SetClock swaps the time source. Intended for tests
// only; the clock is propagated to any MemoryStore so
// the timestamps stored in Create / Update are
// deterministic.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
	if ms, ok := s.store.(*MemoryStore); ok {
		ms.SetClock(now)
	}
}

// Get returns a single inbound by id. ErrNotFound
// bubbles up from the store unchanged so the handler
// can map it to 404.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Inbound, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.GetByID(ctx, id)
}

// ListByNode returns every inbound belonging to the
// given node, sorted by ListenPort ascending.
func (s *Service) ListByNode(ctx context.Context, nodeID uuid.UUID) ([]*Inbound, error) {
	if nodeID == uuid.Nil {
		return nil, &ValidationError{Field: "node_id", Message: "must be a non-zero UUID"}
	}
	return s.store.ListByNode(ctx, nodeID)
}

// ListByProtocol returns every inbound with the given
// protocol across all nodes.
func (s *Service) ListByProtocol(ctx context.Context, p Protocol) ([]*Inbound, error) {
	if !isAllowedProtocol(p) {
		return nil, &ValidationError{Field: "protocol", Message: "unknown protocol: " + string(p)}
	}
	return s.store.ListByProtocol(ctx, p)
}

// CreateInput is the payload the HTTP handler passes
// in. The caller can leave ID zero and let the service
// assign one, or pre-assign if they have a
// deterministic ID requirement.
type CreateInput struct {
	ID          uuid.UUID
	NodeID      uuid.UUID
	Name        string
	Protocol    Protocol
	Listen      string
	ListenPort  int
	ListenPorts []int
	Enabled     *bool
	Tags        []string
	Params      map[string]any
}

// Create validates the input, fills in defaults and
// IDs, and persists a new inbound. The returned
// *Inbound has its ID, CreatedAt, and UpdatedAt fields
// populated.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Inbound, error) {
	name := strings.TrimSpace(in.Name)
	if err := validateName(name); err != nil {
		return nil, err
	}
	if err := validateNode(ctx, s.nodes, in.NodeID); err != nil {
		return nil, err
	}
	if err := validateProtocol(in.Protocol); err != nil {
		return nil, err
	}
	if err := validatePort(in.ListenPort); err != nil {
		return nil, err
	}
	listen := in.Listen
	if listen == "" {
		listen = defaultListen
	}
	if err := validateListen(listen); err != nil {
		return nil, err
	}
	extraPorts, err := normaliseListenPorts(in.ListenPorts)
	if err != nil {
		return nil, err
	}
	if err := validateTags(in.Tags); err != nil {
		return nil, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	id := in.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	i := &Inbound{
		ID:          id,
		NodeID:      in.NodeID,
		Name:        name,
		Protocol:    in.Protocol,
		Listen:      listen,
		ListenPort:  in.ListenPort,
		ListenPorts: extraPorts,
		Enabled:     enabled,
		Tags:        normaliseTags(in.Tags),
		Params:      cloneParams(in.Params),
	}
	if err := s.store.Create(ctx, i); err != nil {
		// ErrDuplicate is the only store-level error
		// we surface to the handler as-is;
		// everything else gets wrapped with a fresh
		// context.
		if errors.Is(err, ErrDuplicate) {
			return nil, err
		}
		return nil, fmt.Errorf("create: %w", err)
	}
	return s.store.GetByID(ctx, i.ID)
}

// UpdateInput is what HTTP PUT / JSON-patch bodies
// unmarshal into. Pointer fields mean "leave
// unchanged"; nil means "do not touch".
type UpdateInput struct {
	Name        *string
	Protocol    *Protocol
	Listen      *string
	ListenPort  *int
	ListenPorts *[]int
	Enabled     *bool
	Tags        *[]string
	Params      *map[string]any
}

// Update applies the patch to the stored inbound. The
// input is partial: any nil pointer field means "do
// not change". Empty string values for required fields
// are rejected with a ValidationError pointing at the
// offending field.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*Inbound, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	existing, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if err := validateName(name); err != nil {
			return nil, err
		}
		existing.Name = name
	}
	if in.Protocol != nil {
		if err := validateProtocol(*in.Protocol); err != nil {
			return nil, err
		}
		existing.Protocol = *in.Protocol
	}
	if in.Listen != nil {
		listen := *in.Listen
		if listen == "" {
			listen = defaultListen
		}
		if err := validateListen(listen); err != nil {
			return nil, err
		}
		existing.Listen = listen
	}
	if in.ListenPort != nil {
		if err := validatePort(*in.ListenPort); err != nil {
			return nil, err
		}
		existing.ListenPort = *in.ListenPort
	}
	if in.ListenPorts != nil {
		extraPorts, err := normaliseListenPorts(*in.ListenPorts)
		if err != nil {
			return nil, err
		}
		existing.ListenPorts = extraPorts
	}
	if in.Enabled != nil {
		existing.Enabled = *in.Enabled
	}
	if in.Tags != nil {
		if err := validateTags(*in.Tags); err != nil {
			return nil, err
		}
		existing.Tags = normaliseTags(*in.Tags)
	}
	if in.Params != nil {
		existing.Params = cloneParams(*in.Params)
	}

	if err := s.store.Update(ctx, existing); err != nil {
		if errors.Is(err, ErrDuplicate) {
			return nil, err
		}
		return nil, fmt.Errorf("update: %w", err)
	}
	return s.store.GetByID(ctx, id)
}

// Delete removes an inbound by id. ErrNotFound bubbles
// up from the store.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.Delete(ctx, id)
}

// --- internal helpers ---------------------------------------------------

// allowedProtocols is the closed set of protocol
// families an inbound may declare. The set matches
// the CHECK constraint in migration 0003 and the
// per-protocol renderers in the sing-box provider.
var allowedProtocols = map[Protocol]struct{}{
	ProtocolVLESS:       {},
	ProtocolHysteria2:   {},
	ProtocolShadowsocks: {},
	ProtocolTrojan:      {},
}

func isAllowedProtocol(p Protocol) bool {
	_, ok := allowedProtocols[p]
	return ok
}

// cloneParams returns a shallow copy of the params
// map. The values are kept as-is (any) — the panel
// does not own the per-protocol schema, and the
// sing-box provider is the authoritative validator.
func cloneParams(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
