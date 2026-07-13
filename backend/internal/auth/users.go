// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"sync"
	"time"

	"github.com/alexedwards/argon2id"
)

// User is a panel principal. Phase 0 stores credentials in memory;
// the same struct is persisted by the pgx Store from Phase 1.1.
// PasswordHash is the PHC-formatted argon2id string
// (`$argon2id$v=19$m=...,t=...,p=...$salt$hash`) produced by
// argon2id.CreateHash.
type User struct {
	ID           string
	Username     string
	PasswordHash string // argon2id PHC string
	Scopes       Scopes
	CreatedAt    time.Time
}

// VerifyPassword reports whether the given plaintext matches the
// user's argon2id hash. The work factors (memory, iterations,
// parallelism) are baked into the hash itself, so this call is
// slow by design — the cost is paid only on the slow path of a
// login attempt.
//
// Note: ComparePasswordAndHash returns (match bool, err error).
// We map "match=false, err=nil" — the most common path on a wrong
// password — to ErrUnauthorised, so the caller does not have to
// remember to check both return values.
func (u *User) VerifyPassword(plaintext string) error {
	match, err := argon2id.ComparePasswordAndHash(plaintext, u.PasswordHash)
	if err != nil {
		return err
	}
	if !match {
		return ErrUnauthorised
	}
	return nil
}

// HashPassword returns an argon2id-encoded PHC string for the
// plaintext. Uses DefaultParams: m=64 MiB, t=1, p=4. Helper for
// tests and the seed-data path; the canonical place to mint a
// user is via Store.CreateUser (Phase 2).
func HashPassword(plaintext string) (string, error) {
	return argon2id.CreateHash(plaintext, argon2id.DefaultParams)
}

// MemoryStore is an in-memory implementation of Store for Phase 0.
// It is safe for concurrent use.
//
// The zero value is NOT ready — call NewMemoryStore or initialise
// the mutex explicitly. Seed users via WithUser.
type MemoryStore struct {
	mu       sync.RWMutex
	users    map[string]*User // keyed by username
	refresh  map[string]refreshEntry
}

// refreshEntry binds a refresh-token hash to its owner and expiry.
type refreshEntry struct {
	userID    string
	expiresAt time.Time
	used      bool // rotation: refresh consumes-and-replaces
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users:   make(map[string]*User),
		refresh: make(map[string]refreshEntry),
	}
}

// WithUser seeds a user. Intended for tests and the Phase 0 dev
// bootstrap. Re-adding a username overwrites the previous entry
// (useful for re-seeding).
func (m *MemoryStore) WithUser(u *User) *MemoryStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	m.users[u.Username] = u
	return m
}

// LookupUser implements Store.
func (m *MemoryStore) LookupUser(_ context.Context, username string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[username]
	if !ok {
		return nil, ErrUnauthorised
	}
	return u, nil
}

// SaveRefresh implements Store.
func (m *MemoryStore) SaveRefresh(_ context.Context, userID, tokenHash string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refresh[tokenHash] = refreshEntry{
		userID:    userID,
		expiresAt: expiresAt,
		used:      false,
	}
	return nil
}

// ConsumeRefresh implements Store. It atomically marks the token as
// used so the next call with the same hash returns ErrInvalidToken.
// This is rotation: the client must use the new refresh token we
// hand back, not the old one.
func (m *MemoryStore) ConsumeRefresh(_ context.Context, tokenHash string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.refresh[tokenHash]
	if !ok {
		return "", ErrInvalidToken
	}
	if entry.used {
		// Reuse of a consumed token is treated as possible
		// theft. The caller (Service.Refresh) is expected to
		// call RevokeChain on the user when this happens.
		// For Phase 0 we just reject the consume.
		return "", ErrInvalidToken
	}
	if time.Now().UTC().After(entry.expiresAt) {
		delete(m.refresh, tokenHash)
		return "", ErrInvalidToken
	}
	entry.used = true
	m.refresh[tokenHash] = entry
	return entry.userID, nil
}

// RevokeChain implements Store. It marks every still-valid refresh
// token for the user as used, so a stolen token (already used)
// cannot be replayed, and any sibling token from the same chain
// is also invalidated. Idempotent: revoking an already-revoked
// chain is a no-op.
func (m *MemoryStore) RevokeChain(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for hash, entry := range m.refresh {
		if entry.userID != userID {
			continue
		}
		if entry.used {
			continue
		}
		if now.After(entry.expiresAt) {
			continue
		}
		entry.used = true
		m.refresh[hash] = entry
	}
	return nil
}

// FindRefreshUser implements Store. Returns the userID bound to
// a refresh token hash WITHOUT changing the row. Returns
// ErrInvalidToken if the hash is unknown.
func (m *MemoryStore) FindRefreshUser(_ context.Context, tokenHash string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.refresh[tokenHash]
	if !ok {
		return "", ErrInvalidToken
	}
	return entry.userID, nil
}
