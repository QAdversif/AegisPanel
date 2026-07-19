// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"
)

// User is a panel principal. Phase 0 stores credentials in memory;
// the same struct is persisted by the pgx Store from Phase 1.1.
// PasswordHash is the PHC-formatted argon2id string
// (`$argon2id$v=19$m=...,t=...,p=...$salt$hash`) produced by
// argon2id.CreateHash.
type User struct {
	ID           string
	Username     string
	Email        string // empty in dev; required when persisted to the pgx store
	PasswordHash string // argon2id PHC string
	Role         string // 'super-admin' | 'operator' | 'viewer' (matches the `admins.role` DB enum)
	Enabled      bool
	Scopes       Scopes
	CreatedAt    time.Time
	UpdatedAt    time.Time
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
	mu      sync.RWMutex
	users   map[string]*User  // keyed by username
	emails  map[string]string // email -> username (for the Email UNIQUE constraint)
	refresh map[string]refreshEntry
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
		emails:  make(map[string]string),
		refresh: make(map[string]refreshEntry),
	}
}

// WithUser seeds a user. Intended for tests and the Phase 0 dev
// bootstrap. Re-adding a username overwrites the previous entry
// (useful for re-seeding). The Email UNIQUE constraint is NOT
// enforced by WithUser — it is the dev/test seed helper, and
// tests can override existing entries at will. The production
// path goes through CreateUser.
func (m *MemoryStore) WithUser(u *User) *MemoryStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = u.CreatedAt
	}
	if u.Email != "" {
		m.emails[u.Email] = u.Username
	}
	m.users[u.Username] = u
	return m
}

// CreateUser inserts a new admin. The caller fills every
// field on the passed User (ID, Username, Email, PasswordHash,
// Role) and is responsible for hashing the password with
// HashPassword beforehand. A zero ID is replaced with a
// fresh uuid.New() before the insert.
//
// Returns ErrConflict on username or email collision. The
// production pgx path also returns ErrConflict on the
// underlying UNIQUE-index violation (a 23505); the
// MemoryStore mirrors that behaviour so the HTTP layer
// has one error to handle.
func (m *MemoryStore) CreateUser(_ context.Context, u *User) error {
	if u == nil {
		return fmt.Errorf("auth: CreateUser: nil user")
	}
	if u.Username == "" {
		return fmt.Errorf("auth: CreateUser: username is required")
	}
	if u.PasswordHash == "" {
		return fmt.Errorf("auth: CreateUser: password hash is required (call HashPassword first)")
	}
	if u.ID == "" {
		u.ID = uuid.NewString()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.users[u.Username]; exists {
		return fmt.Errorf("%w: username %q", ErrConflict, u.Username)
	}
	if u.Email != "" {
		if _, exists := m.emails[u.Email]; exists {
			return fmt.Errorf("%w: email %q", ErrConflict, u.Email)
		}
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	u.UpdatedAt = u.CreatedAt
	if !u.Enabled {
		// Default to enabled when the caller does not
		// explicitly set the flag. CLI subcommands
		// building the User from input flags leave
		// Enabled at the zero value (false); that
		// would block the very first login. Override.
		u.Enabled = true
	}
	cp := *u
	m.users[u.Username] = &cp
	if u.Email != "" {
		m.emails[u.Email] = u.Username
	}
	return nil
}

// UpdatePassword rotates the user's argon2id password hash.
// Returns ErrUnauthorised if the user is gone. The caller
// is responsible for hashing the new password with
// HashPassword beforehand — the Store only persists.
func (m *MemoryStore) UpdatePassword(_ context.Context, userID, newHash string) error {
	if newHash == "" {
		return fmt.Errorf("auth: UpdatePassword: new hash is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.ID == userID {
			u.PasswordHash = newHash
			u.UpdatedAt = time.Now().UTC()
			return nil
		}
	}
	return ErrUnauthorised
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

// LookupByUsername implements Store. Behaves identically
// to LookupUser (returns ErrUnauthorised on miss); the
// alias exists for the CLI subcommand's "user not found"
// UX so the Store interface carries both methods and a
// future split (404 vs 401) is a no-op call-site change.
func (m *MemoryStore) LookupByUsername(ctx context.Context, username string) (*User, error) {
	return m.LookupUser(ctx, username)
}

// ListUsers implements Store. Returns a copy of every
// User the store knows about, sorted by username for
// deterministic output.
func (m *MemoryStore) ListUsers(_ context.Context) ([]*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*User, 0, len(m.users))
	for _, u := range m.users {
		cp := *u
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out, nil
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
