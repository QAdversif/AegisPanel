// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Error types for the subscription package. Handlers
// map these to HTTP status codes:
//   - ValidationError  -> 400
//   - NotFoundError    -> 404
//   - UserNotLiveError -> 403 (the user exists but is
//                          not entitled to a
//                          subscription right now:
//                          expired, disabled, deleted)
//
// As of d-refactor.3 the user-CRUD surface has fully
// moved to `internal/users`. The subscription package
// no longer returns ErrNotFound / ErrDuplicate
// sentinels for user-CRUD paths; those live in the
// users package now. The three error types below are
// the render-side failures (the Service's
// ResolveHostsForUser / lookupUserBySubToken paths
// return them; the handler maps to HTTP status).

package subscription

import (
	"fmt"
)

// ValidationError is returned by Service methods when an
// input is rejected before any Store call. The Field /
// Message pair is what the handler surfaces to the API
// client (no internal Go types).
type ValidationError struct {
	Field   string
	Message string
}

// Error implements `error`.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Message)
}

// NotFoundError is returned by the render handler's
// lookupUserBySubToken path (and would be returned by
// any future Store lookup that misses). The handler
// maps it to 404.
type NotFoundError struct {
	What string // "user", "pool", …
	Key  string // the value that was looked up
}

// Error implements `error`.
func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s not found: %s", e.What, e.Key)
}

// UserNotLiveError is returned by ResolveHostsForUser
// when the user exists but is not in a state that
// entitles them to a subscription. The handler maps it
// to 403.
type UserNotLiveError struct {
	Status UserStatus
}

// Error implements `error`.
func (e *UserNotLiveError) Error() string {
	return fmt.Sprintf("user is not live: status=%s", e.Status)
}
