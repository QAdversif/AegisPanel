// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT body issued by Aegis. It carries enough context
// to drive authorisation decisions without a database round-trip on
// every request: principal identity, granted scopes, the access
// token's own identifier (for revocation lists), and standard
// registered claims (iat, exp, nbf, iss, aud).
//
// Scopes are encoded as a JSON array (default encoding). This
// departs from RFC 8693's space-separated string form for simplicity;
// external OAuth tooling can still inspect the `scope_count` claim
// for tooling that needs the count, and we never plan to expose
// Aegis tokens to third-party OAuth clients.
type Claims struct {
	// Scopes granted to this token.
	Scopes Scopes `json:"scope,omitempty"`

	// JWTID — unique identifier for the token, used in
	// server-side revocation lists (Phase 2). Maps to "jti".
	JWTID string `json:"jti,omitempty"`

	jwt.RegisteredClaims
}

// HasScope reports whether the claims grant the given scope.
func (c Claims) HasScope(want Scope) bool { return c.Scopes.Has(want) }

// IsExpired returns true if the token is past its exp claim.
// jwt.RegisteredClaims.ExpiresAt may be nil for malformed tokens.
func (c Claims) IsExpired(now time.Time) bool {
	if c.ExpiresAt == nil {
		return true
	}
	return !now.Before(c.ExpiresAt.Time)
}
