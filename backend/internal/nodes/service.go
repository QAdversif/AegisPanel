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
	"github.com/QAdversif/AegisPanel/internal/bootstrap"
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
	// v0.8.7: SSH client factory + known_hosts
	// path + service-wide default SSH user
	// for `RefreshAgentBearer`. All three
	// are nil-safe; the method returns an
	// error when the factory is nil so the
	// production wiring in `internal/app/app.go`
	// must call `WithSSHClientFactory` at
	// boot. The pattern matches
	// `WithWebhooks` / `WithAudits` /
	// `WithEnvelope` (nil-safe setter, error
	// in the consumer).
	sshClientFactory func(bootstrap.ClientConfig) (bootstrap.Client, error)
	knownHosts       string
	sshUser          string
	// v0.8.30: mTLS cert issuer. Provision
	// calls `EnsureNodeCerts(ctx, nodeID,
	// addr)` before the SSH dial so the
	// bootstrap installer can push the
	// cert+key files to the node. Nil-safe
	// (a nil issuer means "mTLS not wired";
	// Provision falls back to bearer-only
	// auth, which is the v0.8.29 default).
	agentCA AgentCertIssuer
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

// RotateTransport flips a node's agent_transport
// between "http" and "grpc" (the v0.8.31 closed
// set). The flow:
//
//  1. Validate the new value (closed set;
//     any other value is a ValidationError).
//  2. Read the current row (ErrNotFound if
//     missing).
//  3. No-op + return the current row if the
//     value is already the target (idempotency
//     for cron / remediation runs).
//  4. Call store.SetAgentTransport (single
//     column write; mirrors SetAgentBearer /
//     SetSSHPrivateKeyCiphertext).
//  5. Re-fetch the row to surface the
//     post-rotation `updated_at`.
//  6. Fire webhooks.EventNodeUpdated (the
//     existing "node row changed" event; a
//     dedicated EventNodeTransportRotated
//     would inflate the webhook contract
//     surface for a low-frequency change).
//  7. Record the audit row
//     `node.transport.rotated` with the
//     previous + new transport in the After
//     map and the full row in Before / After.
//
// # v0.8.31 scope
//
// The transport pick at apply time is still
// process-wide via `AEGIS_AGENT_TRANSPORT` (a
// per-node resolver-driven pick is a v0.8.32
// follow-up). The column is observability +
// audit; the rotate-transport flow is the
// operator's source of intent. v0.8.32 will
// read the column to drive the per-node
// transport pick.
//
// # Errors
//
//   - ValidationError: transport is not in
//     {"http", "grpc"}.
//   - ErrNotFound: id is unknown.
func (s *Service) RotateTransport(ctx context.Context, id uuid.UUID, newTransport string) (*Node, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	if err := validateAgentTransport(newTransport); err != nil {
		return nil, err
	}
	// Read the current row. The `Before` snapshot
	// in the audit row carries the pre-rotation
	// transport; the post-rotation row is read
	// from the store after the SetAgentTransport
	// call so the `updated_at` reflects the write.
	cur, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// v0.8.31 idempotency: a rotation to the
	// node's current transport is a no-op. The
	// audit log would otherwise fill with
	// "rotated http -> http" entries on every
	// operator check-in (the CLI is runnable on
	// cron or as a remediation). The
	// deprecation-warning header on
	// GET /api/v1/nodes (PR 2) is the operator's
	// signal that the migration is not done; the
	// audit log is the record of the actual
	// changes, not the "I checked again" runs.
	if cur.AgentTransport == newTransport {
		return cur, nil
	}
	prevTransport := cur.AgentTransport
	if err := s.store.SetAgentTransport(ctx, id, newTransport); err != nil {
		return nil, err
	}
	out, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// v0.7.x: the row changed. The existing
	// EventNodeUpdated event is the cheapest
	// signal — webhook subscribers that don't
	// care about transport-rotation (the common
	// case) can ignore the event; subscribers
	// that do care filter on the After map.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventNodeUpdated, out)
	// v0.8.31 audit: action is the canonical
	// `<resource>.<verb>` (past tense) shape.
	// The `Before` snapshot is the full row so
	// the audit log's `GetByID` path can
	// reconstruct the pre-rotation state
	// (transport + name + address + …); the
	// `After` map is the operator-friendly
	// summary. The transport value is in both
	// the Before row and the After map; the
	// redundancy is intentional — the audit
	// row should be readable without joining
	// the Before / After blobs.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "node.transport.rotated",
		ResourceType: "node",
		ResourceID:   id.String(),
		Before:       cur,
		After: map[string]any{
			"node_name":            cur.Name,
			"address":              cur.Address,
			"agent_transport":      newTransport,
			"agent_transport_prev": prevTransport,
		},
	})
	return out, nil
}

// validateAgentTransport is the v0.8.31 closed-set
// gate. The store + the SQL CHECK constraint are
// the safety nets; this function surfaces a
// clear ValidationError to the CLI / HTTP layer
// before either sees the value.
//
// Adding a new value here requires a migration
// that re-aligns the SQL CHECK constraint (the
// pg_store_state_check_test.go pattern is the
// template for the same check on transport).
func validateAgentTransport(t string) error {
	switch t {
	case AgentTransportHTTP, AgentTransportGRPC:
		return nil
	}
	return &ValidationError{
		Field:   "agent_transport",
		Message: "must be one of: http, grpc",
	}
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
