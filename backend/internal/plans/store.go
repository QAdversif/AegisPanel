// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Store is the persistence boundary for the
// `plans` table (migration 0001_initial.sql). The
// `plan_pool` join table is intentionally NOT
// touched by this Store in v0.6.0 — the
// `subscription` package keeps its read-only view
// of plan_pool for the render path. v0.6.x will
// fold the plan_pool CRUD into this Store and let
// the subscription package delegate to it.
//
// Handlers and the Service layer go through here so
// the MemoryStore can be swapped for a pgx-backed
// PgStore in production without touching call sites
// (mirrors the inbounds / hosts / nodes / users
// pattern).

package plans

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Store is the persistence boundary for plans. The
// interface is intentionally narrow — the HTTP
// handler, future cron jobs, and any migration tool
// all go through here.
type Store interface {
	// Create inserts a new plan. Returns ErrDuplicate
	// if a row with the same Name already exists.
	Create(ctx context.Context, p *Plan) error

	// GetByID returns the plan with the given id, or
	// ErrNotFound.
	GetByID(ctx context.Context, id uuid.UUID) (*Plan, error)

	// GetByName returns the plan with the given name,
	// or ErrNotFound. The Name column is UNIQUE in
	// migration 0001.
	GetByName(ctx context.Context, name string) (*Plan, error)

	// List returns every plan, sorted by CreatedAt
	// ascending. The slice is freshly allocated;
	// callers may mutate it.
	List(ctx context.Context) ([]*Plan, error)

	// Update replaces the stored copy of p.ID.
	// Returns ErrNotFound if no such plan exists;
	// ErrDuplicate if the rename would collide with
	// an existing row.
	Update(ctx context.Context, p *Plan) error

	// Delete removes the plan with the given id.
	// Returns ErrNotFound if no such plan exists.
	//
	// The default implementation is a hard delete.
	// The DB does NOT cascade to `users.plan_id`
	// (there is no FK constraint in migration 0001),
	// so a Delete of a plan that still has users
	// pointing at it is the operator's responsibility
	// — the UI shows a confirm dialog.
	Delete(ctx context.Context, id uuid.UUID) error
}

// ErrNotFound is returned by Store implementations
// when the requested plan does not exist. Wrapped
// with %w so callers can use errors.Is.
var ErrNotFound = errors.New("plans: not found")

// ErrDuplicate is returned when a Create or Update
// would violate the (name) UNIQUE constraint from
// migration 0001. The wrapped error message includes
// the offending column so the handler can put it in
// the 409 response body.
var ErrDuplicate = errors.New("plans: duplicate")

// ErrInvalid is the umbrella error returned by the
// Service layer for input-validation failures. The
// wrapped error is a *ValidationError carrying the
// offending field and a human-readable message.
var ErrInvalid = errors.New("plans: invalid input")

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
