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

package subscription

import "fmt"

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
