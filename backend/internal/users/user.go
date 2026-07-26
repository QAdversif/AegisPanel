// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package users implements the panel-side CRUD for the
// `users` table (migration 0001_initial.sql + 0011
// sub_token rotation). The users here are the
// end-users of the VPN service (the people who pay for
// access and use the sing-box config). They are
// distinct from the `admins` table — admins are the
// panel operators in `internal/auth`, with their own
// auth flow (argon2id + JWT + refresh tokens). The two
// packages must not be confused; this file is only
// about the end-user table.
//
// # Model
//
// A User is a single end-user account. The Go struct
// mirrors the `users` table one-to-one, with a few
// additions for the d.1 column additions:
//
//   - PlanID  — the FK to `plans.id`; nullable
//     (a free-tier user has no plan).
//   - Status  — closed enum: active | grace | disabled
//     | expired | deleted. The DB CHECK constraint
//     enforces the same set; the Service layer is the
//     authoritative gate that rejects writes outside
//     the set.
//   - TrafficLimitBytes / TrafficUsedBytes — the user's
//     data cap and current usage. sing-box's per-user
//     routing rules read the limit at render time.
//   - DeviceLimit — max concurrent connections per user.
//   - HostsAllowlist / HostsBlocklist — JSON arrays of
//     host IDs the user may / may not connect to. The
//     sing-box provider's RenderConfig reads them
//     when it builds the per-user routing rules.
//   - SubToken / SubTokenPrev / SubTokenPrevExpiresAt —
//     subscription-token rotation chain (see migration
//     0011). The Subscription package looks users up
//     by these tokens; the Service generates a fresh
//     token on Create.
//   - TelegramID — optional 1:1 mapping to a Telegram
//     user (Phase 1.1 cabinet integration). Nullable.
//   - Email — optional 1:1 mapping for the
//     password-reset / notifications flow. Nullable.
//
// # Relationship to internal/auth
//
// The `auth` package is for panel admins. The
// `users` package is for VPN end-users. The two have
// nothing in common at the data-model level. The
// end-user does NOT authenticate to the panel via the
// same JWT flow as an admin — they authenticate to
// sing-box with the per-user credentials rendered into
// the sing-box config (VLESS UUID, Shadowsocks
// password, etc.). Phase 1.1 will add a cabinet UI
// where end-users can rotate their own sub-token; that
// lives in `internal/cabinet/`, not here.
//
// # Why Service-layer validation
//
// The DB has CHECK constraints (status, traffic_limit
// non-negative, …) and UNIQUE constraints (username,
// sub_token). The Service layer adds input validation
// that is awkward to express in SQL (username format,
// JSONB array shape, telegram_id range, plan_id
// cross-entity) so the handler can return 400 with a
// useful error rather than 500 with a Postgres error
// string.

package users

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Status is the closed set of user states. The string
// values match the DB CHECK constraint in migration
// 0001_initial.sql; the Service layer is the
// authoritative gate.
type Status string

// Status values. The set is closed — any value
// outside this list is rejected by the Service.
const (
	StatusActive   Status = "active"
	StatusGrace    Status = "grace"
	StatusDisabled Status = "disabled"
	StatusExpired  Status = "expired"
	StatusDeleted  Status = "deleted"
)

// AllowedStatuses is the closed set the Service
// accepts. Used by the validation code in
// service.go; exported so the handler tests can use
// the same list.
var AllowedStatuses = []Status{
	StatusActive, StatusGrace, StatusDisabled, StatusExpired, StatusDeleted,
}

// IsValid reports whether s is in the closed set. It
// is the cheap pre-flight check; the Service uses it
// before the more expensive cross-entity work.
func (s Status) IsValid() bool {
	for _, v := range AllowedStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// IsLive reports whether the user is allowed to
// fetch a subscription in this state. `active` and
// `grace` are live; the rest are not. The
// subscription package's ResolveHostsForUser uses
// this as the first filter pass: a user that is not
// live is rejected before any plan / pool lookup.
//
// The method lives on the users package's Status
// because the user state machine is a user-domain
// concern; the subscription package consumes it
// through the type alias `UserStatus = users.Status`
// and never redefines the rule. The 3X-UI / X-UI
// convention is "active | grace are live; expired |
// disabled | deleted are not", which is what this
// returns.
func (s Status) IsLive() bool {
	return s == StatusActive || s == StatusGrace
}

// User is the panel-side view of a single end-user
// account. The fields mirror the `users` table
// one-to-one; the JSON tags match the wire format
// used by the admin REST API.
//
// The struct does NOT carry the per-protocol
// credentials (VLESS UUID, Shadowsocks password, etc.)
// that sing-box needs to accept a connection — those
// live in the sing-box provider's render layer and
// are derived from the User's ID + a server-side
// secret. This keeps the User model thin and the
// credential-generation logic in one place.
//
// # JSON wire format
//
// The JSON tags are snake_case (NOT camelCase) so
// the wire format matches the pre-existing
// `subscription.User` API contract. This is the
// canonical user type across the panel; the
// `internal/subscription` package consumes it for
// the /sub/{token} render endpoint and the
// `internal/users` package owns its CRUD surface.
//
// # Hosts allowlist / blocklist
//
// `HostsAllowlist` and `HostsBlocklist` are
// `[]uuid.UUID` slices. The DB column is JSONB, so
// the pgx driver marshals / unmarshals the slice
// transparently. Empty list (not nil) means "no
// restriction" — the field is always emitted as a
// non-null JSON array, never null.
type User struct {
	ID                    uuid.UUID   `json:"id"`
	ExternalID            string      `json:"external_id,omitempty"` // ID from external Cabinet (Phase 1.1)
	Username              string      `json:"username"`
	Status                Status      `json:"status"`
	PlanID                *uuid.UUID  `json:"plan_id,omitempty"` // nil = free tier
	ExpireAt              *time.Time  `json:"expire_at,omitempty"`
	TrafficLimitBytes     int64       `json:"traffic_limit_bytes"`
	TrafficUsedBytes      int64       `json:"traffic_used_bytes"`
	LastResetAt           *time.Time  `json:"last_reset_at,omitempty"`
	DeviceLimit           int         `json:"device_limit"`
	HostsAllowlist        []uuid.UUID `json:"hosts_allowlist"`
	HostsBlocklist        []uuid.UUID `json:"hosts_blocklist"`
	SubToken              string      `json:"sub_token"`
	SubTokenRotatedAt     *time.Time  `json:"sub_token_rotated_at,omitempty"`
	SubTokenPrev          string      `json:"sub_token_prev,omitempty"`
	SubTokenPrevExpiresAt *time.Time  `json:"sub_token_prev_expires_at,omitempty"`
	TelegramID            *int64      `json:"telegram_id,omitempty"`
	Email                 string      `json:"email,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// IsValid reports whether u carries the minimum data
// the Store requires to accept a Create or Update. It
// is intentionally permissive — heavy validation
// (username format, telegram range, JSONB array shape)
// lives in the Service so it can return rich
// per-field errors via *ValidationError. IsValid is
// the cheap pre-flight the store uses to reject
// obviously broken inserts.
func (u *User) IsValid() bool {
	if u == nil {
		return false
	}
	if u.Username == "" {
		return false
	}
	if !u.Status.IsValid() {
		return false
	}
	if u.TrafficLimitBytes < 0 {
		return false
	}
	if u.TrafficUsedBytes < 0 {
		return false
	}
	if u.DeviceLimit < 0 {
		return false
	}
	return true
}

// String is a debug helper. The output is intentionally
// redacted — we do NOT log the sub_token, even in
// debug mode.
func (u *User) String() string {
	if u == nil {
		return "<nil user>"
	}
	return fmt.Sprintf("User{id=%s username=%q status=%s}", u.ID, u.Username, u.Status)
}
