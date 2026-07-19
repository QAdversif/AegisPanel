// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Service is the public entry point for the audit
// log. The rest of the panel calls Service.Record
// from each mutating handler; the HTTP handler
// (handler.go) calls Service.List and GetByID for
// the read API.

package audits

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
)

// Service is the audit-log entry point. main.go
// constructs one Service per process; the same
// instance is shared by every mutating handler in
// the v0.3+ wiring. v0.2.0 only writes entries from
// the change-password handler.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService wires a Service from a Store. The
// store is the only thing swapped between Phase 0
// (MemoryStore) and the Phase 1 PgStore.
func NewService(store Store) *Service {
	return &Service{
		store: store,
		now:   time.Now,
	}
}

// SetClock replaces the time source. Test-only.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Record appends a new audit entry. Errors are
// logged at warn level and swallowed — the audit
// log is a best-effort observability surface, not
// a critical consistency boundary. A failure to
// write a row should not roll back the mutation
// the operator just made; the cost of a missed
// audit row is "operator cannot trace this
// particular change", not data corruption.
//
// The returned AuditEntry is the persisted copy
// (with ID + CreatedAt filled). Callers that
// don't need it can ignore the return.
func (s *Service) Record(ctx context.Context, e Entry) (*AuditEntry, error) {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = s.now().UTC()
	}
	row, err := s.store.Insert(ctx, e)
	if err != nil {
		log.Warn().
			Err(err).
			Str("action", e.Action).
			Str("resource_type", e.ResourceType).
			Str("resource_id", e.ResourceID).
			Msg("audit: failed to record entry")
		return nil, err
	}
	return row, nil
}

// List returns entries matching the filter.
// The handler passes the input ListFilter through
// unchanged; the Service is a thin wrapper today,
// but keeping the indirection means future
// concerns (rate-limiting the read path, paging,
// metrics) can be added in one place.
func (s *Service) List(ctx context.Context, f ListFilter) ([]*AuditEntry, error) {
	if f.Limit < 0 {
		return nil, fmt.Errorf("audits: List: limit must be non-negative")
	}
	return s.store.List(ctx, f)
}

// GetByID returns the full entry. ErrNotFound if
// no such row.
func (s *Service) GetByID(ctx context.Context, id string) (*AuditEntry, error) {
	return s.store.GetByID(ctx, id)
}
