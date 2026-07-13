// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// User is a panel principal. Phase 0 stores credentials in memory;
// the same struct will be persisted in Phase 2 with bcrypt-hashed
// passwords and pgx-backed scans.
type User struct {
	ID           string
	Username     string
	PasswordHash []byte // bcrypt hash
	Scopes       Scopes
	CreatedAt    time.Time
}

// VerifyPassword reports whether the given plaintext matches the
// user's bcrypt hash. The work factor is whatever the hash was
// generated with (default 10); the cost is paid only on the slow
// path of a login attempt.
func (u *User) VerifyPassword(plaintext string) error {
	return bcrypt.CompareHashAndPassword(u.PasswordHash, []byte(plaintext))
}

// HashPassword bcrypt-hashes a plaintext password at the default
// cost (10). Helper for tests and the seed-data path; the canonical
// place to mint a user is via Store.CreateUser (Phase 2).
func HashPassword(plaintext string) ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
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
		// Reuse of a consumed token is treated as a possible
		// theft: the safer behaviour is to revoke the whole
		// chain. For Phase 0 we just reject.
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
