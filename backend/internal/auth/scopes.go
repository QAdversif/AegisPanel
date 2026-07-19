// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package auth implements Aegis's authentication and authorisation
// surface for Phase 1: argon2id password hashing, HS256 JWT access
// tokens, opaque refresh tokens, scope-based ACL, and a chi
// subrouter with /login, /refresh, and /me endpoints.
//
// Storage is in-memory for Phase 0; the pgx-backed Store
// (Phase 1.1) reads the existing `admins` and
// `admin_refresh_tokens` tables.

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

	// ScopeHosts lets a principal manage hosts (bundles of
	// endpoints exposed to end-user VPN clients). Hosts are
	// the product-level entity — a "Latvia / VLESS+HY2"
	// entry in the admin UI is one host with two endpoints.
	// Distinct from ScopeNodes: a node is the server
	// itself, a host is the product the operator sells.
	ScopeHosts Scope = "hosts"
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

// ErrConflict is returned by Store.CreateUser when the
// chosen username or email is already in use. The Store
// maps the underlying UNIQUE-index violation (a 23505 in
// the pgx path) onto this error so the HTTP layer can
// surface a 409 without leaking which constraint fired.
var ErrConflict = errors.New("auth: conflict")

// Store is the persistence boundary for users and refresh tokens.
// Implementations: MemoryStore (Phase 0), PgStore (Phase 1.1).
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

	// FindRefreshUser returns the userID bound to a refresh
	// token hash WITHOUT marking it consumed. Used by the
	// service to detect token reuse: if ConsumeRefresh
	// returns ErrInvalidToken but FindRefreshUser returns a
	// non-empty userID, the token was already consumed —
	// the most likely cause is theft, and the service then
	// calls RevokeChain.
	FindRefreshUser(ctx context.Context, tokenHash string) (userID string, err error)

	// RevokeChain marks every still-valid refresh token belonging
	// to the given user as used. Called when reuse of a
	// consumed token is detected — the most likely cause is
	// token theft, in which case the safest response is to
	// invalidate every outstanding refresh for that user.
	RevokeChain(ctx context.Context, userID string) error

	// CreateUser inserts a new admin user. The caller is
	// responsible for filling every field on the passed
	// User (ID, Username, Email, PasswordHash, Role, Scopes)
	// and for hashing the password with HashPassword
	// beforehand. The Store enforces username + email
	// uniqueness (mirroring the migration's UNIQUE indexes)
	// and returns ErrConflict on collision. A zero ID
	// is replaced with a fresh uuid.New() before the
	// insert.
	CreateUser(ctx context.Context, u *User) error

	// UpdatePassword rotates the user's argon2id password
	// hash. Returns ErrUnauthorised if the user is gone
	// (the handler maps that to 404, not 401). The caller
	// is responsible for hashing the new password with
	// HashPassword beforehand.
	UpdatePassword(ctx context.Context, userID, newHash string) error

	// ListUsers returns every user the store knows about,
	// in no particular order. Used by the `aegis admin
	// list` CLI subcommand and (eventually) the audit
	// log UI's user picker. The returned slice is
	// freshly allocated; callers may mutate without
	// affecting the store.
	ListUsers(ctx context.Context) ([]*User, error)

	// LookupByUsername returns the user with the given
	// username, or ErrUnauthorised if not found. Unlike
	// LookupUser, this call does NOT collapse "not found"
	// into "unauthorised" (both errors look the same to
	// the caller; the distinction is only useful for
	// the CLI). v0.3 splits the two error types if the
	// audit log UI needs a "user not found" 404.
	LookupByUsername(ctx context.Context, username string) (*User, error)
}
