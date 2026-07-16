// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package subscription is the panel-side view of users,
// plans, host-pools, and the user-facing subscription
// URLs (sing-box / Clash / base64, …).
//
// The package is Phase 0 / Phase 1:
//   - MemoryStore for the in-memory dev backend;
//   - Service that resolves "which hosts should this user
//     see" given a User;
//   - Render that turns the resolved hosts into one of
//     the supported wire formats (base64 only, for now).
//
// Future PRs layer in:
//   - PgStore (mirrors nodes / hosts / inbounds);
//   - sing-box and Clash renderers;
//   - format variables, wildcard `*` with random salt,
//     multi-port random selection, XHTTP
//     download_settings;
//   - sub-token rotation and the `/s3cr3t-sub-<hex>`
//     URL prefix rotation (see internal/config).
//
// See ARCHITECTURE.md §2.4 and §10 for the long-term
// design.
package subscription

import (
	"time"

	"github.com/google/uuid"
)

// UserStatus is the lifecycle state of a User. The
// closed set is pinned by the `users.status` CHECK
// constraint in migration 0001.
type UserStatus string

// User status values. Any value outside this set is
// rejected at the Service boundary.
const (
	UserStatusActive   UserStatus = "active"
	UserStatusGrace    UserStatus = "grace"
	UserStatusDisabled UserStatus = "disabled"
	UserStatusExpired  UserStatus = "expired"
	UserStatusDeleted  UserStatus = "deleted"
)

// IsLive reports whether the user is allowed to fetch a
// subscription in this state. `grace` and `active` are
// live (the operator may have set grace to give a user a
// few days after expiry to renew); the rest are not.
func (s UserStatus) IsLive() bool {
	return s == UserStatusActive || s == UserStatusGrace
}

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

// User is the panel-side view of a single end-user
// account. The fields mirror the `users` table
// one-to-one; we keep them as a Go struct rather than
// `map[string]any` so handlers can rely on the type at
// compile time.
//
// The host allowlist / blocklist are stored as a slice
// of host UUIDs; an empty list means "no restriction".
// Phase 0 ignores both — the Service returns every host
// the user is entitled to without filtering. The slice
// fields are still populated by the Store so a future
// filter pass can read them without a migration.
type User struct {
	ID                uuid.UUID
	Username          string
	Status            UserStatus
	PlanID            *uuid.UUID
	ExpireAt          *time.Time
	TrafficLimitBytes int64
	TrafficUsedBytes  int64
	DeviceLimit       int
	HostsAllowlist    []uuid.UUID
	HostsBlocklist    []uuid.UUID
	SubToken          string
	SubTokenRotatedAt *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Plan is the panel-side view of a tariff. The fields
// mirror the `plans` table one-to-one.
type Plan struct {
	ID                uuid.UUID
	Name              string
	TrafficLimitBytes int64
	Duration          time.Duration
	DeviceLimit       int
	ResetPeriod       ResetPeriod
	PriceCents        int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Pool is the panel-side view of a host pool. A pool
// groups a set of hosts and exposes a strategy for
// selecting which ones to hand to a user.
type Pool struct {
	ID           uuid.UUID
	Name         string
	Strategy     PoolStrategy
	Antiaffinity bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// PoolMember is the join between a Pool and a Host.
// `Weight` defaults to 1; strategies that respect
// weight (round_robin, weighted) read it directly.
type PoolMember struct {
	PoolID uuid.UUID
	HostID uuid.UUID
	Weight int
}
