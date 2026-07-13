// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package auth implements Aegis's authentication and authorisation
// surface for Phase 1: bcrypt password hashing, HS256 JWT access tokens,
// opaque refresh tokens, scope-based ACL, and a chi subrouter with
// /login, /refresh, and /me endpoints.
//
// Storage is in-memory for Phase 0. Phase 2 will swap UserStore for a
// pgx-backed implementation behind the same Store interface; the rest
// of the package (JWT signer, middleware, handlers) is storage-agnostic.

package auth

import (
	"context"
	"errors"
	"time"
)

// Scope is a permission string attached to an access token.
type Scope string

const (
	// ScopeAdmin grants full administrative access. Used for the panel
	// itself, never for end-user VPN clients.
	ScopeAdmin Scope = "admin"

	// ScopeRead grants read-only access to panel data.
	ScopeRead Scope = "read"

	// ScopeWrite grants read+write access (create/update/delete).
	ScopeWrite Scope = "write"

	// ScopeNodes lets a principal manage VPN nodes (BYO registration,
	// rotation, decommission).
	ScopeNodes Scope = "nodes"

	// ScopeUsers lets a principal manage panel users.
	ScopeUsers Scope = "users"

	// ScopeSubscriptions lets a principal issue/revoke subscription
	// tokens and view traffic stats.
	ScopeSubscriptions Scope = "subscriptions"
)

// Scopes is a non-empty set of Scope values.
type Scopes []Scope

// Has reports whether s contains the given scope.
func (s Scopes) Has(want Scope) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

// Strings renders the scopes as a deduplicated []string.
func (s Scopes) Strings() []string {
	seen := make(map[Scope]struct{}, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, string(v))
	}
	return out
}

// ErrUnauthorised is returned by Login when credentials are wrong.
// Callers should map this to HTTP 401.
var ErrUnauthorised = errors.New("auth: unauthorised")

// ErrInvalidToken is returned when a presented token cannot be
// verified, is expired, or is otherwise unusable.
var ErrInvalidToken = errors.New("auth: invalid token")

// Store is the persistence boundary for users and refresh tokens.
// Implementations: MemoryStore (Phase 0), PgStore (Phase 2).
type Store interface {
	// LookupUser returns the user with the given username, or
	// ErrUnauthorised if no such user exists. We collapse
	// "not found" into "unauthorised" to avoid username
	// enumeration via timing or response codes.
	LookupUser(ctx context.Context, username string) (*User, error)

	// SaveRefresh persists a refresh token hash. The token
	// itself is never stored — only its SHA-256 hash.
	SaveRefresh(ctx context.Context, userID string, tokenHash string, expiresAt time.Time) error

	// ConsumeRefresh atomically marks a refresh token as used
	// and returns the owning userID. Returns ErrInvalidToken
	// if the token is unknown, already consumed, or expired.
	ConsumeRefresh(ctx context.Context, tokenHash string) (userID string, err error)
}
