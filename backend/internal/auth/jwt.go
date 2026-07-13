// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AccessTokenTTL is the lifetime of a freshly-minted JWT access token.
// Short by design — refresh tokens are the durable credential.
const AccessTokenTTL = 15 * time.Minute

// RefreshTokenTTL is the lifetime of a refresh token. Long enough to
// survive a workday without re-auth, short enough that a stolen
// device's blast radius is bounded.
const RefreshTokenTTL = 30 * 24 * time.Hour // 30 days

// JWTIssuer — the "iss" claim value Aegis puts on its own tokens.
const JWTIssuer = "aegis-panel"

// JWTAudience — the "aud" claim. Aegis is single-tenant per
// deployment, but the audience lets us tell access tokens for the
// panel apart from subscription tokens in Phase 1+.
const JWTAudience = "aegis-panel/v1"

// Signer mints and verifies HS256 access tokens. The signing key is
// supplied once at construction; rotating the key invalidates every
// outstanding access token, which is the desired behaviour on a
// secret compromise.
type Signer struct {
	secret []byte
	now    func() time.Time // injected for tests
}

// NewSigner constructs a Signer. The secret must be at least 32 bytes
// (config.Config.validate enforces this).
func NewSigner(secret string) *Signer {
	return &Signer{
		secret: []byte(secret),
		now:    time.Now,
	}
}

// SetClock replaces the time source. Test-only.
func (s *Signer) SetClock(now func() time.Time) { s.now = now }

// Issue mints a fresh access token for the given user with the
// requested scopes. jti is a unique identifier the caller can record
// in a revocation list.
func (s *Signer) Issue(userID string, scopes Scopes, jti string) (string, error) {
	now := s.now().UTC()
	claims := Claims{
		Scopes: scopes,
		JWTID:  jti,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    JWTIssuer,
			Audience:  jwt.ClaimStrings{JWTAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return signed, nil
}

// Verify parses a token string, checks the signature, the standard
// claims (exp, nbf, iat, iss, aud), and returns the claims. Returns
// ErrInvalidToken for any failure.
func (s *Signer) Verify(tokenString string) (*Claims, error) {
	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Method.Alg())
		}
		return s.secret, nil
	},
		jwt.WithIssuer(JWTIssuer),
		jwt.WithAudience(JWTAudience),
		jwt.WithLeeway(30*time.Second),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	if !tok.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// NewRefreshToken returns a cryptographically random 32-byte token
// (base16-encoded, 64 chars) plus its SHA-256 hash. The token is
// given to the client; only the hash is stored.
func NewRefreshToken() (token, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("rand: %w", err)
	}
	tok := hex.EncodeToString(buf)
	return tok, HashRefreshToken(tok), nil
}

// HashRefreshToken returns the hex-encoded SHA-256 of a refresh token.
// We store the hash, never the token, so a database leak does not
// leak active refresh tokens.
func HashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ErrInvalidRefreshToken is returned for malformed refresh token
// strings (not hex, wrong length).
var ErrInvalidRefreshToken = errors.New("auth: invalid refresh token")

// ValidateRefreshTokenFormat checks that a refresh token is a 64-char
// lowercase hex string. It does NOT check the database — the Store
// does that. This is just a guard against passing garbage to a hash
// function.
func ValidateRefreshTokenFormat(token string) error {
	if len(token) != 64 {
		return ErrInvalidRefreshToken
	}
	for _, r := range token {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ErrInvalidRefreshToken
		}
	}
	return nil
}
