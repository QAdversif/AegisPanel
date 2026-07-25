// SPDX-License-Identifier: AGPL-3.0-or-later

package users

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Store is the persistence boundary for users. The
// interface is intentionally narrow — handlers, the
// subscription renderer, the sing-box renderer's
// future per-user credential generation, and the
// event-emission side of the BatchedApplier pipeline
// all go through here. Swapping MemoryStore for a
// pgx implementation in Phase 1.1 is a single-file
// change (mirroring the inbounds / nodes packages).
type Store interface {
	// Create inserts a new user. Returns ErrDuplicate
	// if a row with the same Username or SubToken
	// already exists.
	Create(ctx context.Context, u *User) error
	// GetByID returns the user with the given id, or
	// ErrNotFound.
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
	// GetByUsername returns the user with the given
	// username, or ErrNotFound. Used by the
	// subscription package's username-based
	// resolution path.
	GetByUsername(ctx context.Context, username string) (*User, error)
	// GetBySubToken returns the user matching the
	// sub_token (or the previous sub_token, if
	// `usePrev` is true and the previous token is
	// still within its grace window — see migration
	// 0011). Used by the subscription package.
	//
	// The usePrev parameter is the "include the
	// prev-token in the lookup" flag. A normal
	// sub-token fetch (the common path) passes
	// usePrev=true; tests that want to verify the
	// prev-token has expired can pass usePrev=false.
	GetBySubToken(ctx context.Context, token string, usePrev bool) (*User, error)
	// List returns every user, sorted by CreatedAt
	// ascending. The slice is freshly allocated;
	// callers may mutate it. Used by the admin UI's
	// "all users" view and by the FlushFn's re-render
	// path (v0.4.0-d.5).
	List(ctx context.Context) ([]*User, error)
	// ListByStatus returns every user with the given
	// status, sorted by CreatedAt ascending. Used by
	// the FlushFn's re-render path to enumerate
	// active users (skip deleted / expired).
	ListByStatus(ctx context.Context, s Status) ([]*User, error)
	// Update replaces the stored copy of u.ID.
	// Returns ErrNotFound if no such user exists;
	// ErrDuplicate if the update would collide with
	// an existing row (e.g. username change to one
	// already in use).
	Update(ctx context.Context, u *User) error
	// Delete removes the user with the given id.
	// Returns ErrNotFound if no such user exists.
	//
	// The default implementation is a hard delete.
	// Soft-delete (status = deleted) is the
	// Service-layer's job (the Store does not
	// enforce either way). The FlushFn's re-render
	// path uses ListByStatus to skip StatusDeleted
	// users regardless of whether they were
	// hard- or soft-deleted.
	Delete(ctx context.Context, id uuid.UUID) error
}

// ErrNotFound is returned by Store implementations
// when the requested user does not exist. Wrapped
// with %w so callers can use errors.Is.
var ErrNotFound = errors.New("users: not found")

// ErrDuplicate is returned when a Create or Update
// would violate one of the unique constraints:
//
//   - (username) — UNIQUE per migration 0001.
//   - (sub_token) — UNIQUE per migration 0001.
//   - (sub_token_prev) — partial UNIQUE per
//     migration 0011 (only when non-NULL).
//
// The wrapped error message includes the offending
// column so the handler can put it in the 409
// response body.
var ErrDuplicate = errors.New("users: duplicate")

// ErrInvalid is the umbrella error returned by the
// Service layer for input-validation failures. The
// wrapped error is a *ValidationError carrying the
// offending field and a human-readable message.
var ErrInvalid = errors.New("users: invalid input")

// ValidationError is a per-field validation failure.
// The Service layer returns these wrapped in ErrInvalid
// so the handler can format them as a 400 with a
// useful response body.
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
