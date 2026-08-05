// SPDX-License-Identifier: AGPL-3.0-or-later

package nodes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/crypto/envelope"
	"github.com/QAdversif/AegisPanel/internal/webhooks"
)

// Service is the business-logic layer on top of Store. It owns
// input validation, ID/timestamp generation, and the
// well-known-states transition. Handlers should call Service
// rather than Store directly so the rules stay in one place.
type Service struct {
	store    Store
	now      func() time.Time
	webhooks *webhooks.Service     // v0.7.x: outbound event surface. May be nil (see WithWebhooks).
	audits   *audits.Service       // v0.7.x deferred call-site: every mutating method records an audit_log row after the row is committed.
	envelope envelope.SecretCipher // v0.8.5: nil-safe; the GetStoredKey method returns an error when nil.
}

// NewService wires a Service around the given store. The clock
// is time.Now by default; tests can swap it via SetClock.
func NewService(store Store) *Service {
	return &Service{store: store, now: time.Now}
}

// WithWebhooks installs the outbound event service.
// See plans.Service.WithWebhooks for the rationale.
func (s *Service) WithWebhooks(svc *webhooks.Service) *Service {
	s.webhooks = svc
	return s
}

// WithAudits installs the audit-log writer. Same
// nil-safe pattern as WithWebhooks.
func (s *Service) WithAudits(svc *audits.Service) *Service {
	s.audits = svc
	return s
}

// WithEnvelope installs the age cipher used by
// GetStoredKey to decrypt `ssh_private_key_ciphertext`.
// The setter is nil-safe (a nil cipher disables
// the stored-key read path, matching the
// `WithAudits` / `WithWebhooks` pattern). The
// production wiring in `internal/app/app.go`
// installs the same envelope the webhooks
// Store uses.
func (s *Service) WithEnvelope(cipher envelope.SecretCipher) *Service {
	s.envelope = cipher
	return s
}

// SetClock swaps the time source. Intended for tests only. The
// clock is propagated to any MemoryStore so the timestamps
// stored in Create / Update are deterministic as well.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
	if ms, ok := s.store.(*MemoryStore); ok {
		ms.SetClock(now)
	}
}

// List returns every node in CreatedAt order.
func (s *Service) List(ctx context.Context) ([]*Node, error) {
	return s.store.List(ctx)
}

// Get returns a single node by id.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Node, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.GetByID(ctx, id)
}

// CreateInput is the payload the HTTP handler passes in. The
// caller can leave ID zero and let the service assign one, or
// pre-assign if they have a deterministic ID requirement.
type CreateInput struct {
	ID           uuid.UUID
	Name         string
	Region       string
	State        State
	Address      string
	CapacityHint string
	Tags         []string
}

// Create validates the input, fills in ID and timestamps, and
// persists a new node. The returned *Node has its ID, CreatedAt,
// and UpdatedAt fields populated.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Node, error) {
	if err := validateName(in.Name); err != nil {
		return nil, err
	}
	if err := validateRegion(in.Region); err != nil {
		return nil, err
	}
	if err := validateAddress(in.Address); err != nil {
		return nil, err
	}
	state := in.State
	if state == "" {
		state = StateNew
	}
	if err := validateState(state); err != nil {
		return nil, err
	}

	id := in.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	n := &Node{
		ID:           id,
		Name:         in.Name,
		Region:       in.Region,
		State:        state,
		Address:      in.Address,
		CapacityHint: strings.TrimSpace(in.CapacityHint),
		Tags:         normaliseTags(in.Tags),
	}
	if err := s.store.Create(ctx, n); err != nil {
		// ErrDuplicate is the only store-level error we surface
		// to the handler as-is; everything else gets wrapped
		// with a fresh context.
		if errors.Is(err, ErrDuplicate) {
			return nil, err
		}
		return nil, fmt.Errorf("create: %w", err)
	}
	// Re-fetch to return the timestamps the store assigned.
	out, err := s.store.GetByID(ctx, n.ID)
	if err != nil {
		return nil, err
	}
	// v0.7.x: see plans.Service.Create.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventNodeCreated, out)
	// v0.7.x deferred: record the audit row.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "node.create",
		ResourceType: "node",
		ResourceID:   out.ID.String(),
		After:        out,
	})
	return out, nil
}

// UpdateInput is what HTTP PUT/JSON-patch bodies unmarshal into.
// Only non-pointer fields are required; nil-pointer fields are
// left unchanged on the stored node.
type UpdateInput struct {
	Name         *string
	Region       *string
	State        *State
	Address      *string
	CapacityHint *string
	Tags         *[]string
}

// Update applies the patch to the stored node. The input is
// partial: any nil pointer field means "do not change". Empty
// string values for required fields are rejected with a
// ValidationError pointing at the offending field.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*Node, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	existing, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if in.Name != nil {
		if err := validateName(*in.Name); err != nil {
			return nil, err
		}
		existing.Name = *in.Name
	}
	if in.Region != nil {
		if err := validateRegion(*in.Region); err != nil {
			return nil, err
		}
		existing.Region = *in.Region
	}
	if in.State != nil {
		if err := validateState(*in.State); err != nil {
			return nil, err
		}
		existing.State = *in.State
	}
	if in.Address != nil {
		if err := validateAddress(*in.Address); err != nil {
			return nil, err
		}
		existing.Address = *in.Address
	}
	if in.CapacityHint != nil {
		existing.CapacityHint = strings.TrimSpace(*in.CapacityHint)
	}
	if in.Tags != nil {
		existing.Tags = normaliseTags(*in.Tags)
	}

	if err := s.store.Update(ctx, existing); err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	out, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// v0.7.x: see plans.Service.Create.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventNodeUpdated, out)
	// v0.7.x deferred: record the audit row.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "node.update",
		ResourceType: "node",
		ResourceID:   out.ID.String(),
		Before:       existing,
		After:        out,
	})
	return out, nil
}

// Delete removes a node by id. Idempotent at the store level — a
// missing id is reported as ErrNotFound.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	// v0.7.x deferred: fetch the row before
	// deleting so the audit entry has a Before.
	cur, err := s.store.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	// v0.7.x: the node is gone by the time we
	// dispatch, so the payload carries only
	// the identifier.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventNodeDeleted, map[string]string{
		"id": id.String(),
	})
	// v0.7.x deferred: record the audit row.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "node.delete",
		ResourceType: "node",
		ResourceID:   id.String(),
		Before:       cur,
	})
	return nil
}

// --- validation helpers --------------------------------------------------

// maxNameLen is the longest name we will store. Matches the
// implicit UI cap; bump via a migration if you need more.
const maxNameLen = 63

// maxRegionLen keeps region labels printable in tabular UI.
const maxRegionLen = 32

// maxAddressLen is generous; the agent re-validates the SSH
// endpoint on connect.
const maxAddressLen = 255

// maxTagLen and maxTags bound the tag list so a careless operator
// cannot bloat the row.
const (
	maxTagLen = 32
	maxTags   = 16
)

func validateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return &ValidationError{Field: "name", Message: "must not be empty"}
	}
	if len(name) > maxNameLen {
		return &ValidationError{Field: "name", Message: "exceeds maximum length"}
	}
	// Keep the character set boring on purpose: lowercase
	// letters, digits, dot, dash, underscore. The UI never
	// needs more, and excluding spaces simplifies SQL
	// injection audits.
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return &ValidationError{
				Field:   "name",
				Message: "must contain only letters, digits, '-', '_', '.'",
			}
		}
	}
	return nil
}

func validateRegion(region string) error {
	region = strings.TrimSpace(region)
	if region == "" {
		return &ValidationError{Field: "region", Message: "must not be empty"}
	}
	if len(region) > maxRegionLen {
		return &ValidationError{Field: "region", Message: "exceeds maximum length"}
	}
	return nil
}

func validateAddress(address string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return &ValidationError{Field: "address", Message: "must not be empty"}
	}
	// Format: "host:port". We are intentionally permissive
	// about what counts as a host — IPv4, IPv6, DNS name — so
	// the SSH agent can do its own format check at connect
	// time. We do require exactly one colon.
	host, port, ok := splitHostPort(address)
	if !ok {
		return &ValidationError{Field: "address", Message: "must be host:port"}
	}
	if host == "" {
		return &ValidationError{Field: "address", Message: "host part must not be empty"}
	}
	if port == "" {
		return &ValidationError{Field: "address", Message: "port part must not be empty"}
	}
	if len(address) > maxAddressLen {
		return &ValidationError{Field: "address", Message: "exceeds maximum length"}
	}
	return nil
}

func validateState(s State) error {
	switch s {
	case StateNew, StateOnline, StateDraining, StateOffline, StateDisabled:
		return nil
	}
	return &ValidationError{Field: "state", Message: "unknown state: " + string(s)}
}

func splitHostPort(addr string) (host, port string, ok bool) {
	// The standard library's net.SplitHostPort accepts bracketed
	// IPv6 ("[::1]:22") which we want to allow. We do not
	// import net here to keep the validation layer pure and
	// free of side effects.
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i], addr[i+1:], true
	}
	return "", "", false
}

func normaliseTags(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		if len(tag) > maxTagLen {
			continue
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
		if len(out) >= maxTags {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
