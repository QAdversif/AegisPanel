// SPDX-License-Identifier: AGPL-3.0-or-later

package inbounds

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/cores"
	"github.com/QAdversif/AegisPanel/internal/inboundtemplates"
	"github.com/QAdversif/AegisPanel/internal/nodes"
	"github.com/QAdversif/AegisPanel/internal/webhooks"
)

// Service is the business-logic layer on top of Store.
// It owns:
//
//   - input validation (name, protocol allow-list,
//     port range, listen address format, params shape);
//   - cross-entity validation (every inbound's
//     NodeID must resolve to a known node);
//   - ID / timestamp generation on Create;
//   - the default Listen normalisation;
//   - v0.5.0 BatchedApplier fan-out: every mutating
//     method (Create/Update/Delete) enqueues a
//     cores.Delta into the BatchedApplier for the
//     inbound's node. Unlike users.Service, which
//     fans out to ALL node appliers (the user's
//     targets are not known at the user level),
//     inbounds.Service narrows to the single
//     applier for inb.NodeID because the inbound
//     already carries the node reference.
//
// Handlers call Service rather than Store directly so
// the rules stay in one place and the pgx migration in
// Phase 1.1 can swap the Store without touching
// validation.
type Service struct {
	store    Store
	nodes    *nodes.Service
	now      func() time.Time
	webhooks *webhooks.Service // v0.7.x: outbound event surface. May be nil (see WithWebhooks).
	audits   *audits.Service   // v0.7.x deferred call-site.
	// v0.8.13+: inbound_templates validator. nil
	// = backwards-compat with v0.8.0-v0.8.12, where
	// no template_id was ever stored on an inbound;
	// the validation is a no-op. Production wiring
	// (cmd/aegis + internal/app) always calls
	// WithTemplates(a.InboundTemplates) after both
	// services are constructed, so the nil branch
	// is dead in production but kept for tests that
	// don't care about the template path.
	templates *inboundtemplates.Service
	// batchedAppliers is the v0.5.0 outbound
	// render+apply fan-out. nil = feature disabled.
	// See AEGIS_BATCHED_APPLIER_ENABLED.
	batchedAppliers map[uuid.UUID]*cores.BatchedApplier
}

// NewService wires a Service around the given store.
// The nodes service is required: every inbound must
// reference a real node, and the only way to check that
// is via nodes.Service.
func NewService(store Store, nodesSvc *nodes.Service) *Service {
	return &Service{store: store, nodes: nodesSvc, now: time.Now}
}

// WithWebhooks installs the outbound event service.
// See plans.Service.WithWebhooks for the rationale.
func (s *Service) WithWebhooks(svc *webhooks.Service) *Service {
	s.webhooks = svc
	return s
}

// WithBatchApplier installs the per-node
// BatchedApplier map. See users.Service.WithBatchApplier
// for the broader rationale. Unlike users (which
// fans out to every node), inbounds narrows the
// fan-out to the single applier for the inbound's
// node because the inbound already carries the
// node reference.
func (s *Service) WithBatchApplier(aps map[uuid.UUID]*cores.BatchedApplier) *Service {
	s.batchedAppliers = aps
	return s
}

// WithAudits installs the audit-log writer. Same
// nil-safe pattern as WithWebhooks.
func (s *Service) WithAudits(svc *audits.Service) *Service {
	s.audits = svc
	return s
}

// WithTemplates installs the inbound_templates
// validator used to enforce the v0.8.13+ rules:
//
//   - every inbound with a non-nil TemplateID
//     must reference an existing template;
//   - the template's protocol must match the
//     inbound's protocol (sing-box renders one
//     protocol family per inbound; cross-protocol
//     templates are a config error, not a runtime
//     fallback).
//
// Same nil-safe pattern as WithWebhooks / WithAudits.
// nil = the validation is a no-op (the v0.8.0-v0.8.12
// contract, where no inbound ever had a template_id).
// Production wiring in internal/app always calls
// WithTemplates; the nil branch is for unit tests
// that don't care about the template path.
func (s *Service) WithTemplates(svc *inboundtemplates.Service) *Service {
	s.templates = svc
	return s
}

// enqueueForNode fans out a Delta to the BatchedApplier
// for the given node (the inbound's NodeID). The
// delta's UserID is uuid.Nil because an inbound
// change is a "the whole config changed" event, not
// a per-user one. The BatchedApplier's coalescing
// logic (cores.BatchedApplier.absorb) keeps the
// last-write-wins under uuid.Nil so multiple
// inbound CRUD events in the same window collapse
// to a single FlushFn call.
func (s *Service) enqueueForNode(nodeID uuid.UUID, kind cores.DeltaKind) {
	if len(s.batchedAppliers) == 0 || nodeID == uuid.Nil {
		return
	}
	ap, ok := s.batchedAppliers[nodeID]
	if !ok || ap == nil {
		// No applier for this node — either the
		// node is offline (no applier was
		// created) or the feature is disabled
		// for this node. Silent skip: the
		// apply will fire on the next online
		// transition (future PR) or via
		// external config push.
		return
	}
	ap.Enqueue(cores.Delta{
		Kind:   kind,
		UserID: uuid.Nil, // inbound change, not user-scoped
	})
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

// ListAll returns every inbound across every node,
// sorted for stable diffs in the panel UI. Used by
// the panel-wide GET /api/v1/inbounds endpoint so the
// admin UI can preload the full inbound map in a
// single round-trip (instead of N per-node requests
// to /api/v1/nodes/{nodeId}/inbounds).
func (s *Service) ListAll(ctx context.Context) ([]*Inbound, error) {
	return s.store.ListAll(ctx)
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
	// v0.8.x: optional FK to an inbound_templates
	// row. Nil = use the inline Params (the
	// v0.8.0-v0.8.12 default); non-nil = the
	// renderer's follow-up PR will read the
	// template's params instead. Validation
	// (template must exist + protocols must match)
	// is the v0.8.13 follow-up PR; this PR only
	// stores the value.
	TemplateID *uuid.UUID
	Params     map[string]any
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
	// v0.8.13+: when a TemplateID is supplied,
	// the referenced template must exist and its
	// protocol must match the inbound's protocol.
	// nil TemplateID = the v0.8.0-v0.8.12 default
	// (no template; inline Params). The check is
	// skipped entirely when s.templates is nil
	// (see WithTemplates for the rationale).
	if in.TemplateID != nil {
		if err := s.validateTemplateID(ctx, *in.TemplateID, in.Protocol); err != nil {
			return nil, err
		}
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
		TemplateID:  in.TemplateID,
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
	out, err := s.store.GetByID(ctx, i.ID)
	if err != nil {
		return nil, err
	}
	// v0.7.x: see plans.Service.Create.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventInboundCreated, out)
	// v0.7.x deferred: record the audit row.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "inbound.create",
		ResourceType: "inbound",
		ResourceID:   out.ID.String(),
		After:        out,
	})
	// v0.5.0: enqueue a DeltaAddUser for the
	// inbound's node so the next FlushFn
	// re-renders the node config with the new
	// inbound. The delta's UserID is uuid.Nil
	// (inbound change, not user-scoped); the
	// BatchedApplier's coalescing keeps
	// last-write-wins under uuid.Nil.
	s.enqueueForNode(out.NodeID, cores.DeltaAddUser)
	return out, nil
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
	// v0.8.x: optional FK to inbound_templates. nil
	// = do not change; &uuid.Nil{} (JSON null) =
	// clear the template reference; any other
	// non-nil = set the FK to that value. The
	// renderer's follow-up PR validates the
	// template exists + protocols match; this PR
	// only stores the value.
	TemplateID *uuid.UUID
	Params     *map[string]any
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
	if in.TemplateID != nil {
		// v0.8.x: pointer semantics. nil = "do not
		// touch"; &uuid.Nil{} (the JSON `null`
		// round-trip) = "clear the template
		// reference". Same pattern as the other
		// *T fields on UpdateInput.
		if *in.TemplateID == uuid.Nil {
			existing.TemplateID = nil
		} else {
			// v0.8.13+: validate before the
			// assignment so a failed check
			// leaves existing.TemplateID
			// untouched. The protocol to
			// compare against is the
			// effective protocol: the
			// patch's in.Protocol (if
			// non-nil) was applied earlier
			// in this method, otherwise it
			// is the inbound's existing
			// protocol.
			if err := s.validateTemplateID(ctx, *in.TemplateID, existing.Protocol); err != nil {
				return nil, err
			}
			id := *in.TemplateID
			existing.TemplateID = &id
		}
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
	out, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// v0.7.x: see plans.Service.Create.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventInboundUpdated, out)
	// v0.7.x deferred: record the audit row.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "inbound.update",
		ResourceType: "inbound",
		ResourceID:   out.ID.String(),
		Before:       existing,
		After:        out,
	})
	// v0.5.0: enqueue a DeltaAddUser for the
	// (possibly new) node. An Update that changes
	// the inbound's NodeID (rare; the schema
	// rejects it today but a future move-inbound
	// flow might) would re-render the wrong
	// node; the current schema does not allow
	// it, so the simplification is safe.
	s.enqueueForNode(out.NodeID, cores.DeltaAddUser)
	return out, nil
}

// Delete removes an inbound by id. ErrNotFound bubbles
// up from the store.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	// v0.5.0: capture the inbound's NodeID
	// BEFORE the row is gone, so the enqueue
	// can target the right applier.
	prev, err := s.store.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	// v0.7.x: the inbound is gone by the time we
	// dispatch, so the payload carries only
	// the identifier.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventInboundDeleted, map[string]string{
		"id": id.String(),
	})
	// v0.7.x deferred: record the audit row.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "inbound.delete",
		ResourceType: "inbound",
		ResourceID:   id.String(),
		Before:       prev,
	})
	// v0.5.0: enqueue a DeltaRemoveUser for
	// the inbound's previous node. The appliers'
	// cancel/replace logic will drop this delta
	// if a DeltaAddUser for the same node
	// (uuid.Nil) is enqueued in the same
	// window — a delete-then-recreate sequence
	// — so a no-op render is the right answer.
	s.enqueueForNode(prev.NodeID, cores.DeltaRemoveUser)
	return nil
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

// validateTemplateID enforces the v0.8.13+ rules
// on the inbound's TemplateID:
//
//   - the templates service must be wired
//     (production always does via WithTemplates;
//     a nil service is the v0.8.0-v0.8.12 contract
//     and skips the check, matching the renderer
//     PR's "nil templateSrc keeps the v0.8.0-v0.8.12
//     default" decision);
//   - the template must exist;
//   - the template's protocol must match the
//     inbound's protocol. Cross-protocol templates
//     are a config error: sing-box renders one
//     protocol family per inbound, and the
//     sing-box provider would reject the
//     mismatched blob downstream. Better to
//     fail-fast at the CRUD boundary.
//
// Returns a *ValidationError on failure so the
// HTTP layer can map field=templateId into a
// 400-class response. Any other error (e.g. a pg
// blip in the templates store) is wrapped with
// %w so the handler can distinguish "bad input"
// from "infrastructure".
func (s *Service) validateTemplateID(ctx context.Context, templateID uuid.UUID, inboundProto Protocol) error {
	if s.templates == nil {
		// v0.8.0-v0.8.12 contract: no template_id
		// validation when the templates service
		// is not wired. See Service.WithTemplates.
		return nil
	}
	if templateID == uuid.Nil {
		return &ValidationError{Field: "templateId", Message: "must be a non-zero UUID"}
	}
	tpl, err := s.templates.Get(ctx, templateID)
	if err != nil {
		if errors.Is(err, inboundtemplates.ErrNotFound) {
			return &ValidationError{
				Field:   "templateId",
				Message: "template not found: " + templateID.String(),
			}
		}
		return fmt.Errorf("lookup template %s: %w", templateID, err)
	}
	if string(tpl.Protocol) != string(inboundProto) {
		return &ValidationError{
			Field: "templateId",
			Message: fmt.Sprintf(
				"template protocol %q does not match inbound protocol %q",
				tpl.Protocol, inboundProto,
			),
		}
	}
	return nil
}
