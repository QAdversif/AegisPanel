// SPDX-License-Identifier: AGPL-3.0-or-later

package inboundtemplates

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/webhooks"
)

// Service is the business-logic layer on top of
// Store. It owns:
//
//   - input validation (name, protocol allow-list,
//     description length, params shape);
//   - ID / timestamp generation on Create;
//   - audit log writes via audits.Service;
//   - webhook dispatch via webhooks.Service.
//
// Handlers call Service rather than Store directly so
// the rules stay in one place and the pgx migration in
// Phase 1.1 can swap the Store without touching
// validation.
//
// Note: unlike inbounds.Service, this Service does
// NOT participate in the BatchedApplier fan-out —
// template changes do not directly drive a re-render.
// The renderer's hot path (internal/cores/builder)
// reads the template by id on every flush, so a
// template change is picked up on the next
// inbound-level enqueue (the inbound's next CRUD
// operation, or the next user/host delta on a node
// that has an inbound referencing the template).
type Service struct {
	store    Store
	now      func() time.Time
	webhooks *webhooks.Service // outbound event surface. May be nil.
	audits   *audits.Service   // audit log writer. May be nil.
}

// NewService wires a Service around the given store.
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

// Get returns a single template by id. ErrNotFound
// bubbles up from the store unchanged so the handler
// can map it to 404.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*InboundTemplate, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.GetByID(ctx, id)
}

// GetByName returns a single template by name. The
// name is the operator's human-readable identity.
func (s *Service) GetByName(ctx context.Context, name string) (*InboundTemplate, error) {
	return s.store.GetByName(ctx, name)
}

// List returns every template, sorted by Name.
func (s *Service) List(ctx context.Context) ([]*InboundTemplate, error) {
	return s.store.List(ctx)
}

// ListByProtocol returns every template with the
// given protocol. Used by the admin UI's "show me
// all VLESS templates" view and the inbound-create
// form's "template" dropdown filter.
func (s *Service) ListByProtocol(ctx context.Context, p Protocol) ([]*InboundTemplate, error) {
	if !isAllowedProtocol(p) {
		return nil, &ValidationError{Field: "protocol", Message: fmt.Sprintf("unknown protocol: %q", p)}
	}
	return s.store.ListByProtocol(ctx, p)
}

// GetManyByID returns a map keyed by template id with
// the matching *InboundTemplate. v0.8.13+ builder
// integration: the renderer calls this once per flush
// with the deduplicated TemplateIDs of every inbound
// on the node; the result is consulted for each
// inbound's `params[tag]` entry (the inbound's
// inline `params` is the fallback when the lookup
// doesn't return a row for the inbound's TemplateID).
// Template ids that don't resolve are omitted from
// the result; the builder treats a missing entry as
// "use the inbound's inline params" (fail-soft, same
// pattern as the host source and users source).
func (s *Service) GetManyByID(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*InboundTemplate, error) {
	return s.store.GetManyByID(ctx, ids)
}

// CreateInput is the payload the HTTP handler
// passes in. The caller can leave ID zero and let
// the service assign one, or pre-assign if they
// have a deterministic ID requirement.
type CreateInput struct {
	ID          uuid.UUID
	Name        string
	Protocol    Protocol
	Params      map[string]any
	Description string
}

// Create validates the input, fills in defaults and
// IDs, and persists a new template. The returned
// *InboundTemplate has its ID, CreatedAt, and
// UpdatedAt fields populated.
func (s *Service) Create(ctx context.Context, in CreateInput) (*InboundTemplate, error) {
	if err := validateName(in.Name); err != nil {
		return nil, err
	}
	if err := validateProtocol(in.Protocol); err != nil {
		return nil, err
	}
	if err := validateDescription(in.Description); err != nil {
		return nil, err
	}
	id := in.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	t := &InboundTemplate{
		ID:          id,
		Name:        in.Name,
		Protocol:    in.Protocol,
		Params:      cloneParams(in.Params),
		Description: in.Description,
	}
	if err := s.store.Create(ctx, t); err != nil {
		if errors.Is(err, ErrDuplicate) {
			return nil, err
		}
		return nil, fmt.Errorf("create: %w", err)
	}
	out, err := s.store.GetByID(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	// v0.8.x: outbound event for the cabinet
	// surface. May be nil (see WithWebhooks).
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventInboundTemplateCreated, out)
	// v0.8.x: record the audit row.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "inbound_template.create",
		ResourceType: "inbound_template",
		ResourceID:   out.ID.String(),
		After:        out,
	})
	return out, nil
}

// UpdateInput is what HTTP PUT / JSON-patch bodies
// unmarshal into. Pointer fields mean "leave
// unchanged"; nil means "do not touch".
type UpdateInput struct {
	Name        *string
	Protocol    *Protocol
	Params      *map[string]any
	Description *string
}

// Update applies the patch to the stored template.
// The input is partial: any nil pointer field means
// "do not change". Empty string values for required
// fields are rejected with a ValidationError pointing
// at the offending field.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*InboundTemplate, error) {
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
	if in.Protocol != nil {
		if err := validateProtocol(*in.Protocol); err != nil {
			return nil, err
		}
		existing.Protocol = *in.Protocol
	}
	if in.Params != nil {
		existing.Params = cloneParams(*in.Params)
	}
	if in.Description != nil {
		if err := validateDescription(*in.Description); err != nil {
			return nil, err
		}
		existing.Description = *in.Description
	}

	if err := s.store.Update(ctx, existing); err != nil {
		if errors.Is(err, ErrDuplicate) {
			return nil, err
		}
		return nil, fmt.Errorf("update: %w", err)
	}
	out, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// v0.8.x: outbound event for the cabinet.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventInboundTemplateUpdated, out)
	// v0.8.x: record the audit row.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "inbound_template.update",
		ResourceType: "inbound_template",
		ResourceID:   out.ID.String(),
		Before:       existing,
		After:        out,
	})
	return out, nil
}

// Delete removes a template by id. ErrNotFound
// bubbles up from the store. Note: deleting a
// template does NOT cascade-delete the inbounds that
// reference it — the FK is ON DELETE SET NULL, so
// referencing inbounds fall back to the inline-params
// path (the v0.8.0-v0.8.12 default).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	prev, err := s.store.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	// v0.8.x: the template is gone by the time we
	// dispatch, so the payload carries only the
	// identifier.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventInboundTemplateDeleted, map[string]string{
		"id": id.String(),
	})
	// v0.8.x: record the audit row.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "inbound_template.delete",
		ResourceType: "inbound_template",
		ResourceID:   id.String(),
		Before:       prev,
	})
	return nil
}
