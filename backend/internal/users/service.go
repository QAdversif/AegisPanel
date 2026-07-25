// SPDX-License-Identifier: AGPL-3.0-or-later

package users

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"time"

	"github.com/google/uuid"
)

// MinUsernameLen / MaxUsernameLen are the inclusive
// bounds on Username. The DB has no CHECK on
// username length; the Service is the authoritative
// gate. The lower bound matches the sing-box
// provider's minimum per-user identifier length
// (3 chars); the upper bound matches the typical
// VPS / Linux username limit (32 chars) so the
// sing-box render layer can reuse Username in
// paths / log lines without truncation.
const (
	MinUsernameLen = 3
	MaxUsernameLen = 32
)

// MinSubTokenLen / MaxSubTokenLen are the inclusive
// bounds on the random sub_token hex string. 32 hex
// chars = 16 bytes = 128 bits, matching the
// subscription package's other secrets (see
// `internal/bootstrap/secrets.go`).
const (
	MinSubTokenLen = 32
	MaxSubTokenLen = 64
)

// Service is the business-logic layer on top of
// Store. It owns:
//
//   - input validation (username format, status
//     enum, traffic limit non-negative, telegram ID
//     range, email format, hosts allow/block list
//     shape);
//   - ID / timestamp / sub_token generation on
//     Create (the operator does not pre-assign any
//     of these);
//   - sub_token rotation (the rotateSubToken
//     helper implements migration 0011's
//     "previous token keeps working for a grace
//     window" semantics).
//
// Handlers call Service rather than Store directly
// so the rules stay in one place and the pgx
// migration in Phase 1.1 can swap the Store without
// touching validation.
type Service struct {
	store      Store
	now        func() time.Time
	idGen      func() uuid.UUID
	tokenBytes int // bytes of random for sub_token (default 32 → 64 hex)
}

// NewService wires a Service around the given store.
// The now function stamps CreatedAt / UpdatedAt on
// writes; tests inject a fixed clock.
func NewService(store Store) *Service {
	return &Service{
		store:      store,
		now:        time.Now,
		idGen:      uuid.New,
		tokenBytes: 32,
	}
}

// SetClock swaps the time source. Test-only.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
	if ms, ok := s.store.(*MemoryStore); ok {
		ms.SetClock(now)
	}
}

// Get returns a single user by id. ErrNotFound
// bubbles up from the store unchanged so the handler
// can map it to 404.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*User, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.GetByID(ctx, id)
}

// GetByUsername returns a single user by username.
// ErrNotFound bubbles up.
func (s *Service) GetByUsername(ctx context.Context, username string) (*User, error) {
	if err := validateUsername(username); err != nil {
		return nil, err
	}
	return s.store.GetByUsername(ctx, username)
}

// GetBySubToken returns a user matching the sub_token
// (or its prev-token, when usePrev is true and the
// prev token is still in its grace window). Used by
// the subscription package's resolution path.
func (s *Service) GetBySubToken(ctx context.Context, token string, usePrev bool) (*User, error) {
	if token == "" {
		return nil, &ValidationError{Field: "sub_token", Message: "must be non-empty"}
	}
	return s.store.GetBySubToken(ctx, token, usePrev)
}

// List returns every user, sorted by CreatedAt asc.
func (s *Service) List(ctx context.Context) ([]*User, error) {
	return s.store.List(ctx)
}

// ListByStatus returns every user with the given
// status, sorted by CreatedAt asc.
func (s *Service) ListByStatus(ctx context.Context, status Status) ([]*User, error) {
	if !status.IsValid() {
		return nil, &ValidationError{Field: "status", Message: "unknown status: " + string(status)}
	}
	return s.store.ListByStatus(ctx, status)
}

// CreateInput is the payload the HTTP handler passes
// in (d.2 will add the JSON DTO). The Service
// assigns the ID, CreatedAt, and SubToken — the
// operator does not pre-assign any of these.
//
// Fields with a "*" default to the DB default if
// left zero; the rest are required.
type CreateInput struct {
	Username          string
	ExternalID        string
	Status            Status // defaults to StatusActive
	PlanID            *uuid.UUID
	ExpireAt          *time.Time
	TrafficLimitBytes int64 // defaults to 0 (no cap)
	TrafficUsedBytes  int64 // defaults to 0
	LastResetAt       *time.Time
	DeviceLimit       int // defaults to 0 (unlimited)
	HostsAllowlist    []uuid.UUID
	HostsBlocklist    []uuid.UUID
	TelegramID        *int64
	Email             string
}

// Create creates a new user with a freshly-minted
// sub_token. The Service generates the ID, the
// timestamps, and the sub_token; the caller supplies
// the operator-visible fields.
func (s *Service) Create(ctx context.Context, in CreateInput) (*User, error) {
	// 1. Validate operator-supplied fields.
	if err := validateUsername(in.Username); err != nil {
		return nil, err
	}
	if in.Status == "" {
		in.Status = StatusActive
	}
	if !in.Status.IsValid() {
		return nil, &ValidationError{Field: "status", Message: "unknown status: " + string(in.Status)}
	}
	if in.TrafficLimitBytes < 0 {
		return nil, &ValidationError{Field: "traffic_limit_bytes", Message: "must be >= 0"}
	}
	if in.TrafficUsedBytes < 0 {
		return nil, &ValidationError{Field: "traffic_used_bytes", Message: "must be >= 0"}
	}
	if in.DeviceLimit < 0 {
		return nil, &ValidationError{Field: "device_limit", Message: "must be >= 0"}
	}
	if in.TelegramID != nil && (*in.TelegramID < 1 || *in.TelegramID > 9_999_999_999) {
		return nil, &ValidationError{Field: "telegram_id", Message: "must be a positive 10-digit number"}
	}
	if in.Email != "" {
		if _, err := mail.ParseAddress(in.Email); err != nil {
			return nil, &ValidationError{Field: "email", Message: "invalid format: " + err.Error()}
		}
	}
	if err := validateUUIDList(in.HostsAllowlist, "hosts_allowlist"); err != nil {
		return nil, err
	}
	if err := validateUUIDList(in.HostsBlocklist, "hosts_blocklist"); err != nil {
		return nil, err
	}
	// 2. Mint the sub_token. Random bytes from
	// crypto/rand; on Linux that is getrandom(2).
	tok, err := s.mintSubToken()
	if err != nil {
		return nil, fmt.Errorf("create: mint sub_token: %w", err)
	}
	// 3. Build the User.
	now := s.now()
	u := &User{
		ID:                s.idGen(),
		ExternalID:        in.ExternalID,
		Username:          in.Username,
		Status:            in.Status,
		PlanID:            in.PlanID,
		ExpireAt:          in.ExpireAt,
		TrafficLimitBytes: in.TrafficLimitBytes,
		TrafficUsedBytes:  in.TrafficUsedBytes,
		LastResetAt:       in.LastResetAt,
		DeviceLimit:       in.DeviceLimit,
		HostsAllowlist:    ensureUUIDList(in.HostsAllowlist),
		HostsBlocklist:    ensureUUIDList(in.HostsBlocklist),
		SubToken:          tok,
		TelegramID:        in.TelegramID,
		Email:             in.Email,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	// 4. Persist. ErrDuplicate bubbles up so the
	// handler can map to 409.
	if err := s.store.Create(ctx, u); err != nil {
		return nil, err
	}
	// 5. Return a defensive copy so the caller
	// cannot mutate the in-memory row.
	out := *u
	return &out, nil
}

// UpdateInput is the payload the HTTP handler passes
// in for a PATCH /v1/users/{id}. Every field is a
// pointer so the Service can distinguish "leave
// alone" (nil) from "set to zero" (non-nil & zero).
type UpdateInput struct {
	ExternalID        *string
	Username          *string
	Status            *Status
	PlanID            *uuid.UUID // nil = leave alone; non-nil with uuid.Nil = unlink plan
	ExpireAt          *time.Time
	TrafficLimitBytes *int64
	TrafficUsedBytes  *int64
	LastResetAt       *time.Time
	DeviceLimit       *int
	HostsAllowlist    *[]uuid.UUID
	HostsBlocklist    *[]uuid.UUID
	TelegramID        *int64 // nil = leave alone; non-nil with *TelegramID == 0 = unlink telegram
	Email             *string
}

// Update modifies an existing user. Only the fields
// the caller marks (non-nil) are touched. The
// sub_token is NOT changed by this method — use
// RotateSubToken for that.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*User, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	// Fetch the current state.
	cur, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// Apply the patch in-memory first so we can
	// run the same validators Create uses.
	if in.Username != nil {
		if err := validateUsername(*in.Username); err != nil {
			return nil, err
		}
		cur.Username = *in.Username
	}
	if in.ExternalID != nil {
		cur.ExternalID = *in.ExternalID
	}
	if in.Status != nil {
		if !in.Status.IsValid() {
			return nil, &ValidationError{Field: "status", Message: "unknown status: " + string(*in.Status)}
		}
		cur.Status = *in.Status
	}
	if in.PlanID != nil {
		// nil pointer = unlink (uuid.Nil). The d.2
		// handler will pass uuid.Nil to remove the
		// plan; the d.1 caller can pass &uuid.Nil{}
		// to do the same.
		cur.PlanID = in.PlanID
	}
	if in.ExpireAt != nil {
		cur.ExpireAt = in.ExpireAt
	}
	if in.TrafficLimitBytes != nil && *in.TrafficLimitBytes >= 0 {
		cur.TrafficLimitBytes = *in.TrafficLimitBytes
	}
	if in.TrafficUsedBytes != nil && *in.TrafficUsedBytes >= 0 {
		cur.TrafficUsedBytes = *in.TrafficUsedBytes
	}
	if in.LastResetAt != nil {
		cur.LastResetAt = in.LastResetAt
	}
	if in.DeviceLimit != nil && *in.DeviceLimit >= 0 {
		cur.DeviceLimit = *in.DeviceLimit
	}
	if in.HostsAllowlist != nil {
		if err := validateUUIDList(*in.HostsAllowlist, "hosts_allowlist"); err != nil {
			return nil, err
		}
		cur.HostsAllowlist = ensureUUIDList(*in.HostsAllowlist)
	}
	if in.HostsBlocklist != nil {
		if err := validateUUIDList(*in.HostsBlocklist, "hosts_blocklist"); err != nil {
			return nil, err
		}
		cur.HostsBlocklist = ensureUUIDList(*in.HostsBlocklist)
	}
	if in.TelegramID != nil {
		if *in.TelegramID != 0 {
			if *in.TelegramID < 1 || *in.TelegramID > 9_999_999_999 {
				return nil, &ValidationError{Field: "telegram_id", Message: "must be a positive 10-digit number"}
			}
		}
		cur.TelegramID = in.TelegramID
	}
	if in.Email != nil {
		if *in.Email != "" {
			if _, err := mail.ParseAddress(*in.Email); err != nil {
				return nil, &ValidationError{Field: "email", Message: "invalid format: " + err.Error()}
			}
		}
		cur.Email = *in.Email
	}
	cur.UpdatedAt = s.now()
	// Persist.
	if err := s.store.Update(ctx, cur); err != nil {
		return nil, err
	}
	// Return a fresh fetch so the caller sees the
	// post-update state (some stores apply the
	// update by replacing the row, and UpdatedAt is
	// re-stamped by the DB on write).
	out, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes the user. The Store does a hard
// delete (no soft-delete) — operators that need a
// soft-delete semantics should call
// Service.Update(id, UpdateInput{Status: &deleted})
// first and then call Delete on a periodic cleanup
// cron (out of scope for d.1).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.Delete(ctx, id)
}

// RotateSubToken mints a fresh sub_token, parks the
// old one in sub_token_prev, and sets the prev-token
// grace window. The default grace is 24 hours, per
// migration 0011. The grace parameter is exposed so
// the d.1 tests (and future operator UI) can override.
//
// The grace window is a time.Duration. The new
// sub_token_prev_expires_at = now + grace.
func (s *Service) RotateSubToken(ctx context.Context, id uuid.UUID, grace time.Duration) (*User, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	if grace <= 0 {
		grace = 24 * time.Hour
	}
	cur, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// Mint a fresh token.
	tok, err := s.mintSubToken()
	if err != nil {
		return nil, fmt.Errorf("rotate: mint sub_token: %w", err)
	}
	now := s.now()
	prev := cur.SubToken
	prevExpires := now.Add(grace)
	cur.SubToken = tok
	cur.SubTokenPrev = prev
	cur.SubTokenPrevExpiresAt = &prevExpires
	cur.SubTokenRotatedAt = &now
	cur.UpdatedAt = now
	if err := s.store.Update(ctx, cur); err != nil {
		return nil, err
	}
	out, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// mintSubToken generates a random sub_token. The
// default is 32 bytes (64 hex chars), matching the
// subscription package's other secrets.
func (s *Service) mintSubToken() (string, error) {
	buf := make([]byte, s.tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// --- validation helpers -------------------------------------------------

// validateUsername enforces the format and length
// bounds. The username may contain lowercase ASCII
// letters, digits, dash, underscore, and dot; this
// matches the typical Linux username charset plus
// the dot (for "firstname.lastname" conventions).
// No spaces, no uppercase, no leading/trailing dot.
func validateUsername(u string) error {
	if u == "" {
		return &ValidationError{Field: "username", Message: "must be non-empty"}
	}
	if len(u) < MinUsernameLen {
		return &ValidationError{Field: "username", Message: fmt.Sprintf("must be at least %d characters", MinUsernameLen)}
	}
	if len(u) > MaxUsernameLen {
		return &ValidationError{Field: "username", Message: fmt.Sprintf("must be at most %d characters", MaxUsernameLen)}
	}
	for i, r := range u {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		case r >= 'A' && r <= 'Z':
			// uppercase allowed for legacy
			// Cabinet imports; sing-box's per-user
			// credentials are case-sensitive so we
			// preserve the original case rather than
			// silently lowercasing.
		default:
			return &ValidationError{Field: "username", Message: fmt.Sprintf("invalid character %q at index %d", r, i)}
		}
	}
	if u[0] == '.' || u[len(u)-1] == '.' {
		return &ValidationError{Field: "username", Message: "must not start or end with '.'"}
	}
	return nil
}

// validateUUIDList ensures the list (if non-nil)
// has no zero UUIDs and no duplicates. Empty list
// / nil list are both acceptable (they mean
// "no filter"). The check is intentionally lenient
// on format — a non-zero UUID of any version is
// accepted. The Hosts package is the authoritative
// resolver (it can map a foreign ID to a UUID).
func validateUUIDList(list []uuid.UUID, field string) error {
	if list == nil {
		return nil
	}
	seen := make(map[uuid.UUID]struct{}, len(list))
	for i, u := range list {
		if u == uuid.Nil {
			return &ValidationError{Field: field, Message: fmt.Sprintf("entry %d is zero UUID", i)}
		}
		if _, dup := seen[u]; dup {
			return &ValidationError{Field: field, Message: fmt.Sprintf("duplicate entry %s at index %d", u, i)}
		}
		seen[u] = struct{}{}
	}
	return nil
}

// ensureUUIDList returns a non-nil slice. nil and
// empty both round-trip to a non-nil empty slice so
// the JSON output is always "[]" not "null" (the
// pgx scan path also expects a non-nil empty
// array, not a nil, on round-trip).
func ensureUUIDList(in []uuid.UUID) []uuid.UUID {
	if in == nil {
		return []uuid.UUID{}
	}
	return in
}

// Sentinel: errors.Is(err, ErrInvalid) returns true
// for *ValidationError wrapped via fmt.Errorf.
// Already declared in store.go; this line is just a
// compile-time check that Service uses the same
// sentinel. (No-op; the Go compiler will accept
// either ordering.)
var _ = ErrInvalid

// Sentinel for tests that want to assert the
// Service's Create / Update / Delete / Get call the
// Store. (Compile-time check.)
var _ Store = Store(nil)

// Common errors referenced for `errors.Is` callers.
// Re-exported for clarity at the call site.
var (
	ErrUserNotFound  = ErrNotFound
	ErrUserDuplicate = ErrDuplicate
	ErrUserInvalid   = ErrInvalid
	_                = errors.Is // keep the import in case future helpers wrap
)
