// SPDX-License-Identifier: AGPL-3.0-or-later

package hosts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// Service is the business-logic layer on top of Store. It
// owns:
//
//   - input validation (remark, type, endpoint count, the
//     protocol allow-list, weight bounds, balancer strategy,
//     status filter values, failover endpoint references);
//   - cross-entity validation (every Endpoint.NodeID must
//     resolve to a known node);
//   - ID / timestamp generation on Create;
//   - the per-Endpoint ID and weight default.
//
// Handlers call Service rather than Store directly so the
// rules stay in one place and the pgx migration in Phase 1
// can swap the Store without touching validation.
type Service struct {
	store Store
	nodes *nodes.Service
	now   func() time.Time
}

// NewService wires a Service around the given store. The
// nodes service is required: every Endpoint must reference
// a real node, and the only way to check that is via the
// nodes.Service.
func NewService(store Store, nodesSvc *nodes.Service) *Service {
	return &Service{store: store, nodes: nodesSvc, now: time.Now}
}

// SetClock swaps the time source. Intended for tests only;
// the clock is propagated to any MemoryStore so the
// timestamps stored in Create / Update are deterministic.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
	if ms, ok := s.store.(*MemoryStore); ok {
		ms.SetClock(now)
	}
}

// List returns every host. See Store.List for ordering.
func (s *Service) List(ctx context.Context) ([]*Host, error) {
	return s.store.List(ctx)
}

// Get returns a single host by id. ErrNotFound bubbles up
// from the store unchanged so the handler can map it to
// 404.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Host, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.GetByID(ctx, id)
}

// CreateInput is the payload the HTTP handler passes in.
// The caller can leave ID zero and let the service assign
// one, or pre-assign if they have a deterministic ID
// requirement.
type CreateInput struct {
	ID           uuid.UUID
	Remark       string
	DisplayName  string
	Type         HostType
	Enabled      *bool
	Priority     *int
	StatusFilter []UserStatus
	Country      string
	City         string
	Tags         []string
	Endpoints    []Endpoint
	Balancer     *Balancer
}

// Create validates the input, fills in defaults and IDs,
// and persists a new host. The returned *Host has its ID,
// CreatedAt, and UpdatedAt fields populated.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Host, error) {
	remark := strings.TrimSpace(in.Remark)
	if err := validateRemark(remark); err != nil {
		return nil, err
	}
	if err := validateType(in.Type); err != nil {
		return nil, err
	}
	if err := validateStatusFilter(in.StatusFilter); err != nil {
		return nil, err
	}
	if err := validateDisplayName(in.DisplayName); err != nil {
		return nil, err
	}
	if err := validateCountry(in.Country); err != nil {
		return nil, err
	}
	if err := validateCity(in.City); err != nil {
		return nil, err
	}
	priority := 0
	if in.Priority != nil {
		priority = *in.Priority
	}
	if err := validatePriority(priority); err != nil {
		return nil, err
	}
	endpoints, err := s.normaliseEndpoints(ctx, in.Endpoints)
	if err != nil {
		return nil, err
	}
	if err := s.validateEndpointsCount(in.Type, endpoints); err != nil {
		return nil, err
	}
	balancer, err := s.normaliseBalancer(in.Type, in.Balancer, endpoints)
	if err != nil {
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
	h := &Host{
		ID:           id,
		Remark:       remark,
		DisplayName:  strings.TrimSpace(in.DisplayName),
		Type:         in.Type,
		Enabled:      enabled,
		Priority:     priority,
		StatusFilter: normaliseStatusFilter(in.StatusFilter),
		Country:      strings.TrimSpace(in.Country),
		City:         strings.TrimSpace(in.City),
		Tags:         normaliseTags(in.Tags),
		Endpoints:    endpoints,
		Balancer:     balancer,
	}
	if err := s.store.Create(ctx, h); err != nil {
		// ErrDuplicate is the only store-level error we
		// surface to the handler as-is; everything else
		// gets wrapped with a fresh context.
		if errors.Is(err, ErrDuplicate) {
			return nil, err
		}
		return nil, fmt.Errorf("create: %w", err)
	}
	// Re-fetch to return the timestamps the store assigned.
	return s.store.GetByID(ctx, h.ID)
}

// UpdateInput is what HTTP PUT / JSON-patch bodies
// unmarshal into. Pointer fields mean "leave unchanged";
// nil means "do not touch".
type UpdateInput struct {
	Remark       *string
	DisplayName  *string
	Type         *HostType
	Enabled      *bool
	Priority     *int
	StatusFilter *[]UserStatus
	Country      *string
	City         *string
	Tags         *[]string
	Endpoints    *[]Endpoint
	Balancer     *Balancer
}

// Update applies the patch to the stored host. The input
// is partial: any nil pointer field means "do not change".
// Empty string values for required fields are rejected with
// a ValidationError pointing at the offending field.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*Host, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	existing, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if in.Remark != nil {
		remark := strings.TrimSpace(*in.Remark)
		if err := validateRemark(remark); err != nil {
			return nil, err
		}
		existing.Remark = remark
	}
	if in.DisplayName != nil {
		existing.DisplayName = strings.TrimSpace(*in.DisplayName)
	}
	if in.Type != nil {
		if err := validateType(*in.Type); err != nil {
			return nil, err
		}
		// Type change can change the endpoints count
		// requirement; defer the count check to the
		// end where we have the final endpoints slice.
		existing.Type = *in.Type
	}
	if in.Enabled != nil {
		existing.Enabled = *in.Enabled
	}
	if in.Priority != nil {
		if err := validatePriority(*in.Priority); err != nil {
			return nil, err
		}
		existing.Priority = *in.Priority
	}
	if in.StatusFilter != nil {
		if err := validateStatusFilter(*in.StatusFilter); err != nil {
			return nil, err
		}
		existing.StatusFilter = normaliseStatusFilter(*in.StatusFilter)
	}
	if in.Country != nil {
		existing.Country = strings.TrimSpace(*in.Country)
	}
	if in.City != nil {
		existing.City = strings.TrimSpace(*in.City)
	}
	if in.Tags != nil {
		existing.Tags = normaliseTags(*in.Tags)
	}
	if in.Endpoints != nil {
		endpoints, err := s.normaliseEndpoints(ctx, *in.Endpoints)
		if err != nil {
			return nil, err
		}
		existing.Endpoints = endpoints
	}
	if in.Balancer != nil {
		existing.Balancer = in.Balancer
	}
	// After every change, re-run the cross-field checks
	// (endpoints count for the chosen type, balancer
	// reference integrity).
	if err := s.validateEndpointsCount(existing.Type, existing.Endpoints); err != nil {
		return nil, err
	}
	balancer, err := s.normaliseBalancer(existing.Type, existing.Balancer, existing.Endpoints)
	if err != nil {
		return nil, err
	}
	existing.Balancer = balancer

	if err := s.store.Update(ctx, existing); err != nil {
		if errors.Is(err, ErrDuplicate) {
			return nil, err
		}
		return nil, fmt.Errorf("update: %w", err)
	}
	return s.store.GetByID(ctx, id)
}

// Delete removes a host by id. ErrNotFound bubbles up
// from the store.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.Delete(ctx, id)
}

// --- internal helpers ---------------------------------------------------

// allowedProtocols is the closed set of protocol families
// an Endpoint may reference. The set matches the
// per-protocol renderers in the sing-box provider so a
// typo in a Host's endpoint cannot leak through to the
// subscription service.
//
// # Future migration
//
// When the Inbound model lands, this allow-list moves
// from "string check" to "FK exists in `inbounds` table".
// See the package comment in host.go.
var allowedProtocols = map[string]struct{}{
	"vless":       {},
	"hysteria2":   {},
	"shadowsocks": {},
	"trojan":      {},
}

func isAllowedProtocol(p string) bool {
	_, ok := allowedProtocols[p]
	return ok
}

// normaliseEndpoints walks the input slice, assigning an
// ID and a default weight to each endpoint and validating
// that the NodeID resolves and the protocol is allowed.
func (s *Service) normaliseEndpoints(ctx context.Context, in []Endpoint) ([]Endpoint, error) {
	if len(in) == 0 {
		return nil, &ValidationError{Field: "endpoints", Message: "must contain at least one entry"}
	}
	out := make([]Endpoint, 0, len(in))
	for i, ep := range in {
		// Assign a server-side ID if the caller did not.
		// Endpoints are addressed by ID in the
		// subscription service (failover_endpoint_ids),
		// so the ID has to be stable across re-renders.
		if ep.ID == uuid.Nil {
			ep.ID = uuid.New()
		}
		// Default weight 1 — zero is rejected by
		// validateWeight so the caller has to be
		// explicit about an unweighted endpoint.
		if ep.Weight == 0 {
			ep.Weight = 1
		}
		if err := validateEndpointNode(ctx, s.nodes, ep.NodeID); err != nil {
			return nil, err
		}
		if err := validateEndpointProtocol(ep.Protocol); err != nil {
			return nil, err
		}
		if err := validateWeight(ep.Weight); err != nil {
			return nil, err
		}
		if err := validateEndpointOverrides(ep); err != nil {
			return nil, err
		}
		_ = i
		out = append(out, ep)
	}
	return out, nil
}

// validateEndpointsCount enforces the v3 model invariant
// from ARCHITECTURE.md §10:
//
//	type=direct   → endpoints.length == 1
//	type=balancer → endpoints.length >= 2
func (s *Service) validateEndpointsCount(t HostType, eps []Endpoint) error {
	switch t {
	case HostTypeDirect:
		if len(eps) != 1 {
			return &ValidationError{
				Field:   "endpoints",
				Message: fmt.Sprintf("type=direct requires exactly 1 endpoint, got %d", len(eps)),
			}
		}
	case HostTypeBalancer:
		if len(eps) < 2 {
			return &ValidationError{
				Field:   "endpoints",
				Message: fmt.Sprintf("type=balancer requires at least 2 endpoints, got %d", len(eps)),
			}
		}
	default:
		// validateType is the gate, this branch is
		// defensive.
		return &ValidationError{Field: "type", Message: "unknown type: " + string(t)}
	}
	return nil
}

// normaliseBalancer enforces the cross-field rules between
// Type, Balancer, and Endpoints:
//
//   - type=balancer: balancer must be non-nil and carry a
//     valid strategy; failover_endpoint_ids (if set) must
//     reference endpoints in the same host.
//   - type=direct: balancer must be nil.
func (s *Service) normaliseBalancer(t HostType, b *Balancer, eps []Endpoint) (*Balancer, error) {
	switch t {
	case HostTypeDirect:
		if b != nil {
			return nil, &ValidationError{
				Field:   "balancer",
				Message: "type=direct must not carry a balancer block",
			}
		}
		return nil, nil
	case HostTypeBalancer:
		if b == nil {
			return nil, &ValidationError{
				Field:   "balancer",
				Message: "type=balancer requires a balancer block",
			}
		}
		if err := validateStrategy(b.Strategy); err != nil {
			return nil, err
		}
		if err := validateHealthcheck(b.HealthcheckURL, b.HealthcheckIntervalSec); err != nil {
			return nil, err
		}
		// Failover endpoints must be in the same host.
		if len(b.FailoverEndpointIDs) > 0 {
			known := make(map[uuid.UUID]struct{}, len(eps))
			for _, ep := range eps {
				known[ep.ID] = struct{}{}
			}
			for _, fid := range b.FailoverEndpointIDs {
				if _, ok := known[fid]; !ok {
					return nil, &ValidationError{
						Field:   "balancer.failover_endpoint_ids",
						Message: fmt.Sprintf("endpoint %s is not part of this host", fid),
					}
				}
			}
		}
		return b, nil
	}
	return nil, &ValidationError{Field: "type", Message: "unknown type: " + string(t)}
}
