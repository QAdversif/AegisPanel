// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Service is the business-logic layer on top of
// Store. The package's public entry point for the
// admin CRUD surface (when the HTTP handler lands
// in a follow-up PR) and the read path that the
// future multi-user sing-box renderer will use.
//
// # What is NOT here
//
//   - **No per-protocol validation**. The Service
//     does not know the protocol-specific shape of
//     `credential_value` (UUID for VLESS, password
//     for HY2 / Trojan, etc.). The renderer is
//     authoritative for shape validation; the
//     Service just stores the value the admin
//     provides. See the package docstring for the
//     design rationale.
//   - **No cross-entity pre-validation**. The
//     Service does not verify that `user_id` and
//     `inbound_id` exist before insert; the
//     pgx-backed Store relies on the FK constraints
//     from migration 0019 to surface
//     `pgx.ErrForeignKeyViolation`, and the Service
//     translates that to ErrDuplicate / ErrNotFound
//     in a future PR. The MemoryStore does an
//     in-memory pre-check (faster failure for the
//     unit tests; the FK is the canonical gate
//     for production). See `Insert` for the
//     contract.

package credentials

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/audits"
)

// ValidationError is the typed error the Service
// returns for caller-side mistakes (empty
// credential_value, etc.). Distinct from the
// store-level errors (ErrNotFound, ErrDuplicate)
// so the HTTP handler can map to 400 vs 404 / 409
// in a follow-up PR.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("credentials: %s: %s", e.Field, e.Message)
}

// Service is the business-logic layer on top of
// Store. The fields mirror the v0.7.x pattern: a
// `now` function the tests swap for a fixed clock,
// and a nullable `audits` reference the wiring
// installs after construction (the v0.7.x deferred
// call-site from PR #166; the credential package
// gets the same WithAudits pattern when the admin
// HTTP handler lands).
type Service struct {
	store  Store
	now    func() time.Time
	audits *audits.Service
}

// NewService wires a Service around the given store.
// The now function is used only by the test path
// (the MemoryStore stamps timestamps with it); the
// PgStore stamps timestamps via the DB-side
// DEFAULT NOW() in migration 0019.
func NewService(store Store) *Service {
	return &Service{store: store, now: time.Now}
}

// WithAudits installs the audit-log writer. Same
// nil-safe pattern as the other v0.7.x packages
// (PR #166).
func (s *Service) WithAudits(svc *audits.Service) *Service {
	s.audits = svc
	return s
}

// SetClock swaps the time source. Test-only.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Create creates a new credential. Returns
// ErrDuplicate if a credential already exists for
// the (user_id, inbound_id) pair (the caller should
// `Rotate` instead).
//
// The Service does NOT cross-validate that the
// user or the inbound exist; the Store's FK
// constraints (migration 0019) are the canonical
// gate. The MemoryStore does an in-memory
// pre-check for fast failure in unit tests.
func (s *Service) Create(ctx context.Context, userID, inboundID uuid.UUID, value string) (*Credential, error) {
	if userID == uuid.Nil {
		return nil, &ValidationError{Field: "user_id", Message: "must be a non-zero UUID"}
	}
	if inboundID == uuid.Nil {
		return nil, &ValidationError{Field: "inbound_id", Message: "must be a non-zero UUID"}
	}
	if value == "" {
		return nil, &ValidationError{Field: "credential_value", Message: "must be non-empty"}
	}
	row, err := s.store.Insert(ctx, Credential{
		UserID:          userID,
		InboundID:       inboundID,
		CredentialValue: value,
	})
	if err != nil {
		return nil, err
	}
	// v0.7.x deferred: record the audit row. Same
	// after-commit + best-effort contract as the
	// other per-entity Services. The action is
	// `credential.create`, the resource_type is
	// `credential`, the resource_id is the new id.
	// IP/UA is left blank (Service-layer call-site
	// does not have an *http.Request; see
	// audits.RecordFromContext docstring).
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "credential.create",
		ResourceType: "credential",
		ResourceID:   row.ID.String(),
		After:        row,
	})
	return row, nil
}

// Get returns a single credential by id. Returns
// ErrNotFound if no such row.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Credential, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.GetByID(ctx, id)
}

// ListByUser returns every credential for the
// given user, sorted by inbound_id ascending.
func (s *Service) ListByUser(ctx context.Context, userID uuid.UUID) ([]*Credential, error) {
	if userID == uuid.Nil {
		return nil, &ValidationError{Field: "user_id", Message: "must be a non-zero UUID"}
	}
	return s.store.ListByUser(ctx, userID)
}

// ListByInbound returns every credential for the
// given inbound, sorted by user_id ascending.
func (s *Service) ListByInbound(ctx context.Context, inboundID uuid.UUID) ([]*Credential, error) {
	if inboundID == uuid.Nil {
		return nil, &ValidationError{Field: "inbound_id", Message: "must be a non-zero UUID"}
	}
	return s.store.ListByInbound(ctx, inboundID)
}

// Rotate updates the credential_value of an
// existing row, re-stamping updated_at. Returns
// ErrNotFound if the id does not exist.
//
// "Rotate" is the API-friendly name for "UPDATE
// credential_value". A future PR may add a
// "soft rotate" (insert a new row, keep the old
// one active for N hours) — that requires dropping
// the UNIQUE (user_id, inbound_id) constraint and
// adding a "valid_from" / "valid_until" pair; the
// single-row UPDATE here is the Phase 1 behaviour.
func (s *Service) Rotate(ctx context.Context, id uuid.UUID, newValue string) (*Credential, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	if newValue == "" {
		return nil, &ValidationError{Field: "credential_value", Message: "must be non-empty"}
	}
	// Snapshot the pre-rotate row for the audit
	// entry. The MemoryStore's GetByID is fast
	// (in-process map lookup); the PgStore's
	// GetByID is a single SELECT (we pay one
	// round-trip per Rotate to get the Before
	// state, which the audit entry needs).
	pre, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	post, err := s.store.Update(ctx, id, newValue)
	if err != nil {
		return nil, err
	}
	// v0.7.x deferred: record the audit row. Before
	// is the pre-rotate credential; After is the
	// post-rotate credential. The credential_value
	// is in the row but it is the operator's
	// secret — operators with `audits:read` scope
	// can read it back. This is the same trust
	// model as the v0.2.0 audit log (the row
	// stores actor / resource / IP / UA / payload;
	// the payload may include operator secrets
	// when the mutation touches one).
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "credential.rotate",
		ResourceType: "credential",
		ResourceID:   post.ID.String(),
		Before:       pre,
		After:        post,
	})
	return post, nil
}

// Delete removes a credential by id. Returns
// ErrNotFound if the id does not exist.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	// Snapshot the pre-delete row for the audit
	// entry. Same trade-off as Rotate: one extra
	// read per Delete to populate the Before
	// field.
	pre, err := s.store.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	// v0.7.x deferred: record the audit row. Before
	// is the pre-delete credential; After is nil
	// (the row is gone).
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "credential.delete",
		ResourceType: "credential",
		ResourceID:   id.String(),
		Before:       pre,
	})
	return nil
}

// errors is no longer imported here; the Service
// only uses fmt.Errorf for ValidationError
// construction.
