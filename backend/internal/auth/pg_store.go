// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the PostgreSQL-backed implementation of Store. It uses
// the existing `admins` table from migration 0001 and
// `admin_refresh_tokens` from migration 0002. The store is safe
// for concurrent use; pgxpool handles connection pooling.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore wires a PgStore from an open pgxpool. The pool is
// owned by the caller — close it when the application shuts down.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// LookupUser returns the admin user with the given username, or
// ErrUnauthorised if not found / disabled. We collapse "not found"
// and "disabled" into the same error to avoid username enumeration.
func (s *PgStore) LookupUser(ctx context.Context, username string) (*User, error) {
	const q = `
		SELECT id, username, password_hash, role, enabled
		FROM admins
		WHERE username = $1
		LIMIT 1`

	row := s.pool.QueryRow(ctx, q, username)

	var (
		id           uuid.UUID
		uname        string
		passwordHash string
		role         string
		enabled      bool
	)
	if err := row.Scan(&id, &uname, &passwordHash, &role, &enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUnauthorised
		}
		return nil, fmt.Errorf("query admin: %w", err)
	}
	if !enabled {
		return nil, ErrUnauthorised
	}

	return &User{
		ID:           id.String(),
		Username:     uname,
		PasswordHash: passwordHash,
		Scopes:       scopesForRole(role),
		CreatedAt:    time.Now().UTC(), // best-effort; not material for the in-memory Store
	}, nil
}

// SaveRefresh persists a refresh-token hash bound to userID. The
// token itself is never stored.
func (s *PgStore) SaveRefresh(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	const q = `
		INSERT INTO admin_refresh_tokens (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)`

	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	hashBytes, err := hexDecode(tokenHash)
	if err != nil {
		return fmt.Errorf("decode token hash: %w", err)
	}

	if _, err := s.pool.Exec(ctx, q, uuid.New(), uid, hashBytes, expiresAt); err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

// ConsumeRefresh atomically marks a refresh token as used and
// returns the owning userID. Returns ErrInvalidToken if the
// token is unknown, already consumed, or expired.
//
// Concurrency: the UPDATE ... WHERE used_at IS NULL pattern is
// the canonical atomic "claim" — two concurrent callers race,
// exactly one wins, the other sees zero rows and gets
// ErrInvalidToken.
func (s *PgStore) ConsumeRefresh(ctx context.Context, tokenHash string) (string, error) {
	hashBytes, err := hexDecode(tokenHash)
	if err != nil {
		return "", ErrInvalidToken
	}

	const claim = `
		UPDATE admin_refresh_tokens
		SET used_at = NOW()
		WHERE token_hash = $1
		  AND used_at IS NULL
		  AND expires_at > NOW()
		RETURNING user_id`

	row := s.pool.QueryRow(ctx, claim, hashBytes)
	var userID uuid.UUID
	if err := row.Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrInvalidToken
		}
		return "", fmt.Errorf("claim refresh token: %w", err)
	}
	return userID.String(), nil
}

// RevokeChain marks every still-valid refresh token belonging
// to userID as used. Idempotent. Called when reuse of a
// consumed token is detected — the most likely cause is
// token theft, in which case the safest response is to
// invalidate every outstanding refresh for that user.
func (s *PgStore) RevokeChain(ctx context.Context, userID string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	const q = `
		UPDATE admin_refresh_tokens
		SET used_at = NOW()
		WHERE user_id = $1
		  AND used_at IS NULL
		  AND expires_at > NOW()`
	if _, err := s.pool.Exec(ctx, q, uid); err != nil {
		return fmt.Errorf("revoke chain: %w", err)
	}
	return nil
}

// FindRefreshUser returns the userID bound to a refresh token
// hash WITHOUT marking it consumed. Returns ErrInvalidToken
// if the hash is unknown.
func (s *PgStore) FindRefreshUser(ctx context.Context, tokenHash string) (string, error) {
	hashBytes, err := hexDecode(tokenHash)
	if err != nil {
		return "", ErrInvalidToken
	}
	const q = `SELECT user_id FROM admin_refresh_tokens WHERE token_hash = $1 LIMIT 1`
	row := s.pool.QueryRow(ctx, q, hashBytes)
	var userID uuid.UUID
	if err := row.Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrInvalidToken
		}
		return "", fmt.Errorf("find refresh user: %w", err)
	}
	return userID.String(), nil
}

// scopesForRole maps the `admins.role` column to a set of Scopes.
// This is the only place where the wire format of the role enum
// (from migration 0001) meets our internal Scope vocabulary.
//
//	super-admin -> admin, read, write
//	operator    -> read, write
//	viewer      -> read
//
// Unknown roles get only `read` — fail-closed.
func scopesForRole(role string) Scopes {
	switch role {
	case "super-admin":
		return Scopes{ScopeAdmin, ScopeRead, ScopeWrite, ScopeNodes, ScopeUsers, ScopeSubscriptions}
	case "operator":
		return Scopes{ScopeRead, ScopeWrite, ScopeNodes, ScopeUsers, ScopeSubscriptions}
	case "viewer":
		return Scopes{ScopeRead}
	default:
		return Scopes{ScopeRead}
	}
}
