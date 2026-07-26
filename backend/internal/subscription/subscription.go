// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package subscription is the panel-side view of users,
// plans, host-pools, and the user-facing subscription
// URLs (sing-box / Clash / base64, …).
//
// As of d-refactor.2 the user-CRUD surface has moved
// out of this package into `internal/users` (it was
// duplicated in d.1; d-refactor.1 aligned the wire
// format, this PR drops the subscription-side
// implementation). What stays in this package:
//
//   - The render orchestrator (Service.ResolveHostsForUser
//     / ResolveEndpointsForUser / RenderBase64 / Singbox /
//     Clash) — these are the public subscription URL
//     endpoints, and they live here because they walk
//     the plan → pool → host graph, which is a
//     subscription-domain concern (not a user-domain
//     concern).
//   - The plan / pool / member data layer (Store +
//     MemoryStore + PgStore) — the join tables are read
//     by the resolver and never owned by the user
//     package, so they stay in subscription.
//   - A thin user-CRUD facade on Service for the
//     public render path (GetUserBySubToken +
//     RotateSubToken + CreateUser + ListUsers) and the
//     admin path (the same four + Get / Update in
//     admin_handler.go). The facade is removed in
//     d-refactor.3 when admin_handler.go moves to
//     `users/admin_handler.go`.
//
// The type aliases `User` and `UserStatus` here are
// aliases for `users.User` / `users.Status` (Go type
// alias, not a new type). A `*User` in this package
// is exactly a `*users.User` — the d-refactor.1 wire
// format alignment is what makes this possible: the
// two Go types have identical JSON shape, so callers
// see the same wire bytes regardless of which package
// the value was constructed in. The superset
// (users.User) has additional fields the subscription
// package does not consume (ExternalID, LastResetAt,
// TelegramID, Email) — they are `omitempty` so the
// JSON output is identical for the fields the
// subscription endpoints do use.
//
// See ARCHITECTURE.md §2.4 and §10 for the long-term
// design.
package subscription

import (
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/users"
)

// UserStatus is the lifecycle state of a User. The
// closed set is pinned by the `users.status` CHECK
// constraint in migration 0001. It is a Go type alias
// for `users.Status` so a `*User` in this package is
// exactly a `*users.User`; values are interchangeable
// without conversion.
type UserStatus = users.Status

// User status values. Re-exported from the users
// package so the rest of the subscription code (and
// the public render response, which embeds Status in
// the Subscription-Userinfo header) can keep the old
// `UserStatusActive` / `UserStatusGrace` spelling.
const (
	UserStatusActive   = users.StatusActive
	UserStatusGrace    = users.StatusGrace
	UserStatusDisabled = users.StatusDisabled
	UserStatusExpired  = users.StatusExpired
	UserStatusDeleted  = users.StatusDeleted
)

// User is the panel-side view of a single end-user
// account. It is a Go type alias for `users.User` —
// see the package doc comment for the rationale.
type User = users.User

// CreateUserInput is the payload the admin handler
// passes to Service.CreateUser. It is an alias for
// `users.CreateInput` (the canonical d.1 type) so the
// admin handler does not have to translate between
// two parallel input shapes.
type CreateUserInput = users.CreateInput

// ResetPeriod is the cadence at which `users.traffic_used_bytes`
// is reset to zero. The closed set is pinned by
// `plans.reset_period` CHECK in migration 0001.
type ResetPeriod string

// Reset period values.
const (
	ResetDaily   ResetPeriod = "daily"
	ResetWeekly  ResetPeriod = "weekly"
	ResetMonthly ResetPeriod = "monthly"
	ResetNever   ResetPeriod = "never"
)

// PoolStrategy is how a pool selects which of its
// member hosts to hand to a user. The closed set is
// pinned by `host_pools.strategy` CHECK in migration
// 0001. Phase 0 only implements `all`; the rest are
// documented but the round_robin / least_loaded /
// geo_aware paths land with the Phase 1 strategy work.
type PoolStrategy string

// Pool strategy values.
const (
	PoolStrategyAll         PoolStrategy = "all"
	PoolStrategyRoundRobin  PoolStrategy = "round_robin"
	PoolStrategyLeastLoaded PoolStrategy = "least_loaded"
	PoolStrategyGeoAware    PoolStrategy = "geo_aware"
)

// Plan is the panel-side view of a tariff. The fields
// mirror the `plans` table one-to-one.
type Plan struct {
	ID                uuid.UUID     `json:"id"`
	Name              string        `json:"name"`
	TrafficLimitBytes int64         `json:"traffic_limit_bytes"`
	Duration          time.Duration `json:"duration_ns"`
	DeviceLimit       int           `json:"device_limit"`
	ResetPeriod       ResetPeriod   `json:"reset_period"`
	PriceCents        int64         `json:"price_cents"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

// Pool is the panel-side view of a host pool. A pool
// groups a set of hosts and exposes a strategy for
// selecting which ones to hand to a user.
type Pool struct {
	ID           uuid.UUID    `json:"id"`
	Name         string       `json:"name"`
	Strategy     PoolStrategy `json:"strategy"`
	Antiaffinity bool         `json:"antiaffinity"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

// PoolMember is the join between a Pool and a Host.
// `Weight` defaults to 1; strategies that respect
// weight (round_robin, weighted) read it directly.
type PoolMember struct {
	PoolID uuid.UUID `json:"pool_id"`
	HostID uuid.UUID `json:"host_id"`
	Weight int       `json:"weight"`
}
