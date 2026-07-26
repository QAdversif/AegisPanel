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
//   - ErrNotFound / ErrDuplicate -> the sentinel forms
//     that the Service's user-CRUD thin wrappers
//     return. As of d-refactor.2 the actual user-CRUD
//     lives in `internal/users`; these sentinels are
//     what the Service wraps the users-package errors
//     into, so the admin handler does not have to learn
//     a parallel error space.
//
// The user-package's own sentinels are also surfaced
// directly through Service.Get / Service.Update in
// admin_handler.go (which calls s.users directly);
// writeUserError knows how to map both spaces.

package subscription

import (
	"errors"
	"fmt"
)

// ErrNotFound is the sentinel form of "the user does
// not exist" for the Service's user-CRUD thin
// wrappers. The handlers' writeUserError maps it to
// 404 via errors.Is. It is also the Store's "row
// missing" sentinel for the (now-thin) subscription
// Store surface, so the existing
// `errors.Is(err, ErrNotFound)` checks in the package
// keep working.
var ErrNotFound = errors.New("subscription: not found")

// ErrDuplicate is the sentinel form of "username /
// sub_token collision" for the Service's CreateUser /
// ListUsers thin wrappers. The admin handler's
// writeUserError maps it to 409 via errors.Is.
//
// The actual uniqueness check lives in
// `users.Service.Create` (the canonical d.1 surface);
// the subscription Service just wraps the
// `users.ErrDuplicate` sentinel into this one so
// callers that already know subscription.ErrDuplicate
// keep working.
var ErrDuplicate = errors.New("subscription: duplicate")

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

// NotFoundError is returned when a Store lookup misses.
// The handler maps it to 404.
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
