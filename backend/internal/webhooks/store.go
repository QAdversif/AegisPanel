// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Store is the persistence boundary for the
// webhook surface. Three tables back it:
//
//   - webhook_endpoints   (migration 0001)
//   - webhook_deliveries  (migration 0014)
//   - webhook_dlq         (migration 0014)
//
// Handlers, the dispatcher, and any future
// migration tool go through the Store so the
// MemoryStore can be swapped for the pgx-backed
// PgStore in production without touching call
// sites. The pattern mirrors users / nodes / hosts
// / plans / audits.

package webhooks

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Store is the persistence boundary. The interface
// is intentionally narrow — three concerns
// (endpoints, deliveries, DLQ) live on the same
// Store so the call site does not have to juggle
// three injected dependencies for one webhook.
type Store interface {
	// --- endpoints ---

	// CreateEndpoint inserts a new endpoint. Returns
	// ErrDuplicate if a row with the same URL
	// already exists.
	CreateEndpoint(ctx context.Context, e *Endpoint) error

	// GetEndpoint returns the endpoint with the
	// given id, or ErrNotFound.
	GetEndpoint(ctx context.Context, id uuid.UUID) (*Endpoint, error)

	// ListEndpoints returns every endpoint,
	// sorted by CreatedAt asc. The slice is
	// freshly allocated; callers may mutate it.
	ListEndpoints(ctx context.Context) ([]*Endpoint, error)

	// UpdateEndpoint replaces the stored copy of
	// e.ID. Returns ErrNotFound if no such
	// endpoint exists; ErrDuplicate if the URL
	// rename would collide.
	UpdateEndpoint(ctx context.Context, e *Endpoint) error

	// DeleteEndpoint removes the endpoint with
	// the given id. ON DELETE CASCADE removes
	// the delivery history. DLQ rows are kept
	// (the FK is not enforced on the DLQ side —
	// see the DLQEntry doc comment).
	DeleteEndpoint(ctx context.Context, id uuid.UUID) error

	// --- deliveries ---

	// CreateDelivery inserts a new delivery row.
	// The Store stamps CreatedAt; the Service
	// fills the rest.
	CreateDelivery(ctx context.Context, d *Delivery) error

	// ListDeliveriesByEndpoint returns the
	// delivery history for the given endpoint,
	// sorted by CreatedAt desc. The optional
	// `limit` caps the result; 0 means default
	// (DefaultListLimit). The slice is freshly
	// allocated.
	ListDeliveriesByEndpoint(ctx context.Context, endpointID uuid.UUID, limit int) ([]*Delivery, error)

	// --- DLQ ---

	// EnqueueDLQ moves a failed delivery into
	// the DLQ. The Store allocates a new ID and
	// stamps EnqueuedAt; the Service fills the
	// snapshot fields from the Delivery.
	EnqueueDLQ(ctx context.Context, entry *DLQEntry) error

	// ListDLQ returns every DLQ entry, sorted by
	// EnqueuedAt desc. The optional `limit` caps
	// the result; 0 means default. The slice is
	// freshly allocated.
	ListDLQ(ctx context.Context, limit int) ([]*DLQEntry, error)

	// GetDLQ returns the DLQ entry with the
	// given id, or ErrNotFound.
	GetDLQ(ctx context.Context, id uuid.UUID) (*DLQEntry, error)

	// DeleteDLQ removes a DLQ entry (the
	// operator marks it as resolved / dropped).
	DeleteDLQ(ctx context.Context, id uuid.UUID) error
}

// ErrNotFound is returned by Store implementations
// when the requested row does not exist. Wrapped
// with %w so callers can use errors.Is.
var ErrNotFound = errors.New("webhooks: not found")

// ErrDuplicate is returned when a Create or Update
// would violate a UNIQUE constraint. The wrapped
// error message includes the offending column so
// the handler can put it in the 409 response body.
var ErrDuplicate = errors.New("webhooks: duplicate")

// ErrInvalid is the umbrella error returned by the
// Service layer for input-validation failures. The
// wrapped error is a *ValidationError carrying the
// offending field and a human-readable message.
var ErrInvalid = errors.New("webhooks: invalid input")

// ValidationError is a per-field validation failure.
// The Service layer returns these wrapped in
// ErrInvalid so the handler can format them as a 400
// with a useful response body.
type ValidationError struct {
	Field   string
	Message string
}

// Error implements the error interface. The format
// "<field>: <message>" is stable; tests assert on it.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// Is allows errors.Is(err, ErrInvalid) to work for
// *ValidationError wrapped via fmt.Errorf("%w", ...).
func (e *ValidationError) Is(target error) bool {
	return target == ErrInvalid
}

// DefaultListLimit is the cap the Store applies
// when the caller passes 0 to ListDeliveries /
// ListDLQ. Matches the audits package's default.
const DefaultListLimit = 100

// MaxListLimit is the hard cap the Store applies
// when the caller passes a value > MaxListLimit to
// ListDeliveries / ListDLQ.
const MaxListLimit = 1000
