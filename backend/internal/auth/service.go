// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
)

// Service is the public entry point for auth. It owns the Signer
// and the Store, and is the only thing main.go (and tests) need to
// talk to.
type Service struct {
	signer *Signer
	store  Store
	now    func() time.Time
}

// NewService wires a Service from a Signer and a Store. The store
// is the only thing that will be swapped when Phase 2 lands pgx.
func NewService(signer *Signer, store Store) *Service {
	return &Service{
		signer: signer,
		store:  store,
		now:    time.Now,
	}
}

// SetClock replaces the time source. Test-only.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Signer exposes the underlying JWT signer for the HTTP middleware.
func (s *Service) Signer() *Signer { return s.signer }

// LoginResult is the successful response from Login / Refresh.
type LoginResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	UserID       string
	Scopes       Scopes
}

// Login authenticates a principal and returns a fresh access +
// refresh pair. The caller is responsible for setting the right
// HTTP status (200) and writing the response body.
func (s *Service) Login(ctx context.Context, username, password string) (*LoginResult, error) {
	u, err := s.store.LookupUser(ctx, username)
	if err != nil {
		// Collapse not-found and wrong-password into one
		// error so attackers can't enumerate usernames.
		if errors.Is(err, ErrUnauthorised) {
			return nil, ErrUnauthorised
		}
		return nil, fmt.Errorf("lookup user: %w", err)
	}
	if err := u.VerifyPassword(password); err != nil {
		return nil, ErrUnauthorised
	}
	return s.issuePair(ctx, u)
}

// Refresh exchanges a valid refresh token for a new access+refresh
// pair. The presented refresh token is consumed (single-use) and a
// fresh one is returned. Reuse of a consumed token is rejected —
// and triggers chain revocation, on the assumption that a token
// being used twice is the most likely signal of theft.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*LoginResult, error) {
	if err := ValidateRefreshTokenFormat(refreshToken); err != nil {
		return nil, err
	}
	hash := HashRefreshToken(refreshToken)
	userID, err := s.store.ConsumeRefresh(ctx, hash)
	if err != nil {
		// ConsumeRefresh returns ErrInvalidToken in two cases:
		//   (a) the token is unknown — natural end-of-life
		//   (b) the token was already used — possible theft
		// We distinguish them by asking the store for the
		// userID without consuming. If we get a userID, the
		// token was already consumed and we revoke the whole
		// chain. If we get ErrInvalidToken, the row was never
		// there to begin with.
		if reuseUserID, lookupErr := s.store.FindRefreshUser(ctx, hash); lookupErr == nil {
			// Token reuse — revoke the chain and return
			// ErrInvalidToken. We log the revocation at
			// warn level so a real incident is visible in
			// the audit trail.
			log.Warn().
				Str("user_id", reuseUserID).
				Msg("refresh token reuse detected — revoking chain")
			_ = s.store.RevokeChain(ctx, reuseUserID)
		}
		return nil, err
	}
	u, err := s.lookupByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.issuePair(ctx, u)
}

// Me returns the resolved user for an already-verified access token.
// Used by the /me endpoint.
func (s *Service) Me(ctx context.Context, claims *Claims) (*User, error) {
	return s.lookupByID(ctx, claims.Subject)
}

// issuePair mints a new (access, refresh) pair for the given user.
func (s *Service) issuePair(ctx context.Context, u *User) (*LoginResult, error) {
	now := s.now().UTC()
	jti, err := randomJTI()
	if err != nil {
		return nil, fmt.Errorf("mint jti: %w", err)
	}
	access, err := s.signer.Issue(u.ID, u.Scopes, jti)
	if err != nil {
		return nil, err
	}
	refresh, hash, err := NewRefreshToken()
	if err != nil {
		return nil, err
	}
	expiresAt := now.Add(RefreshTokenTTL)
	if err := s.store.SaveRefresh(ctx, u.ID, hash, expiresAt); err != nil {
		return nil, fmt.Errorf("save refresh: %w", err)
	}
	return &LoginResult{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    now.Add(AccessTokenTTL),
		UserID:       u.ID,
		Scopes:       u.Scopes,
	}, nil
}

// lookupByID resolves a user by ID. In Phase 0 we walk the
// in-memory map; Phase 2 will replace this with a single-row
// SELECT.
func (s *Service) lookupByID(_ context.Context, id string) (*User, error) {
	// MemoryStore doesn't index by ID — we walk. Phase 0 only
	// has 1-3 users, this is fine.
	mem, ok := s.store.(*MemoryStore)
	if !ok {
		return nil, fmt.Errorf("auth: lookupByID only supported for MemoryStore in Phase 0")
	}
	mem.mu.RLock()
	defer mem.mu.RUnlock()
	for _, u := range mem.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, ErrUnauthorised
}

// CreateAdmin mints a new admin user with an argon2id-hashed
// password and persists it through the Store. The caller
// supplies every field on the input except PasswordHash
// (which is derived from plaintext) and ID (assigned by
// the Store).
//
// Returns ErrConflict on username or email collision (the
// Store maps the underlying UNIQUE-index violation onto a
// single error so the caller does not have to know which
// constraint fired).
func (s *Service) CreateAdmin(ctx context.Context, in CreateAdminInput) (*User, error) {
	if in.Username == "" {
		return nil, fmt.Errorf("auth: CreateAdmin: username is required")
	}
	if in.Email == "" {
		return nil, fmt.Errorf("auth: CreateAdmin: email is required")
	}
	if in.Plaintext == "" {
		return nil, fmt.Errorf("auth: CreateAdmin: plaintext is required")
	}
	if in.Role == "" {
		in.Role = "operator"
	}
	hash, err := HashPassword(in.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("auth: CreateAdmin: hash: %w", err)
	}
	u := &User{
		Username:     in.Username,
		Email:        in.Email,
		PasswordHash: hash,
		Role:         in.Role,
		Enabled:      true,
		Scopes:       scopesForRole(in.Role),
	}
	if err := s.store.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// ChangePassword rotates the user's argon2id password hash.
// The caller supplies the new plaintext; the Service
// hashes it. Returns ErrUnauthorised if the user is gone.
func (s *Service) ChangePassword(ctx context.Context, userID, newPlaintext string) error {
	if newPlaintext == "" {
		return fmt.Errorf("auth: ChangePassword: new plaintext is required")
	}
	hash, err := HashPassword(newPlaintext)
	if err != nil {
		return fmt.Errorf("auth: ChangePassword: hash: %w", err)
	}
	return s.store.UpdatePassword(ctx, userID, hash)
}

// CreateAdminInput is the operator-facing shape for creating
// a new admin. Plaintext is hashed by the Service; ID is
// assigned by the Store; Enabled defaults to true.
type CreateAdminInput struct {
	Username  string
	Email     string
	Plaintext string
	Role      string // 'super-admin' | 'operator' | 'viewer' (default: 'operator')
}

// LookupByUsername is the CLI's equivalent of Login
// without a password — the `aegis admin passwd` and
// `aegis admin list` subcommands use it to resolve
// the user row before mutating it. Mirrors the Store
// method so the pg / memory implementations are
// interchangeable.
func (s *Service) LookupByUsername(ctx context.Context, username string) (*User, error) {
	return s.store.LookupByUsername(ctx, username)
}

// ListUsers returns every user the Store knows about.
// The returned slice is freshly allocated; callers may
// mutate without affecting the store. Used by the
// `aegis admin list` CLI subcommand.
func (s *Service) ListUsers(ctx context.Context) ([]*User, error) {
	return s.store.ListUsers(ctx)
}

// randomJTI returns a 16-byte hex string used as the JWT "jti" claim.
func randomJTI() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
