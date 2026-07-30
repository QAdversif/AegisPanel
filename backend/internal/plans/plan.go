// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package plans is the panel-side CRUD owner for the
// `plans` table (migration 0001_initial.sql). The
// table already existed in the schema for Phase 0
// (the `subscription` package used a read-only view
// of it for plan → pool resolution); v0.6.0
// promotes the writes into a dedicated package with
// a pgx-backed store, the same shape as
// `internal/users` / `internal/nodes` / `internal/hosts`.
//
// # Model
//
// A Plan is a tariff the operator sells. The Go
// struct mirrors the `plans` table one-to-one; the
// closed enums (ResetPeriod) are pinned in the
// Service layer so a fat-fingered value surfaces as
// a 400, not a Postgres CHECK violation.
//
//   - Name — the human-readable label ("Starter",
//     "Pro", "Unlimited"). UNIQUE in the DB; the
//     Service layer enforces the same uniqueness via
//     ErrDuplicate.
//   - TrafficLimitBytes — the per-user traffic cap
//     applied to any user on this plan. 0 means
//     "no cap" (the sing-box render layer treats 0 as
//     unlimited).
//   - Duration — the validity period of a
//     subscription issued on this plan, in nanoseconds.
//     Stored as Postgres INTERVAL in the DB. The
//     pgx layer encodes / decodes this as a
//     day-precision `pgtype.Interval` (months are
//     supported on encode but truncated to days; see
//     service.validateDuration for the rationale).
//   - DeviceLimit — max concurrent VPN connections
//     per user on this plan. 0 = unlimited.
//   - ResetPeriod — when the per-user
//     `traffic_used_bytes` counter resets to zero
//     (daily / weekly / monthly / never). The
//     cabinet UI's "remaining GB this month" widget
//     reads this.
//   - PriceCents — the list price in cents (any
//     currency; the panel does not model FX). 0 =
//     free / invite-only.
//
// # What is NOT here (v0.6.0)
//
// The `plan_pool` join table (linking a Plan to one
// or more HostPools) is intentionally NOT touched by
// this package in v0.6.0. The subscription package
// continues to own the read path (ListPoolsForUser
// walks `users.plan_id` → `plan_pool.pool_id`). The
// v0.6.x follow-up will move plan_pool writes here
// and let the subscription package delegate the read
// path to this Service.
//
// # Relationship to internal/subscription
//
// `internal/subscription` has its own read-only
// `Plan` struct (subscription.Plan) and Store for
// the render orchestrator. The two types are
// distinct in v0.6.0: the read path keeps using
// subscription.Plan, the write path uses plans.Plan.
// A future v0.6.x will collapse the two and have
// `subscription` consume `plans.Plan` directly.
//
// # Relationship to internal/users
//
// `users.plan_id` references `plans.id` (NOT NULL
// only on the column, no FK constraint in migration
// 0001). When the user-CRUD service unlinks a plan
// or assigns a new one, the `plans` row is
// unaffected — the FK is logical, not physical, and
// the cascade-on-delete behaviour is the operator's
// responsibility (the UI shows a confirm dialog when
// a delete would orphan users).

package plans

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ResetPeriod is the cadence at which a user's
// `traffic_used_bytes` counter resets to zero. The
// closed set is pinned by `plans.reset_period` CHECK
// in migration 0001 (any value outside the set is
// rejected by the DB). The Service layer is the
// authoritative gate so the handler can return 400
// with a useful message rather than 500 with a
// Postgres constraint violation.
type ResetPeriod string

// Reset period values. The set is closed — any
// value outside this list is rejected by the
// Service.
const (
	ResetDaily   ResetPeriod = "daily"
	ResetWeekly  ResetPeriod = "weekly"
	ResetMonthly ResetPeriod = "monthly"
	ResetNever   ResetPeriod = "never"
)

// AllowedResetPeriods is the closed set the Service
// accepts. Exported so the handler tests can use
// the same list when asserting error messages.
var AllowedResetPeriods = []ResetPeriod{
	ResetDaily, ResetWeekly, ResetMonthly, ResetNever,
}

// IsValid reports whether r is in the closed set. It
// is the cheap pre-flight the Service uses before
// hitting the Store.
func (r ResetPeriod) IsValid() bool {
	for _, v := range AllowedResetPeriods {
		if v == r {
			return true
		}
	}
	return false
}

// String implements fmt.Stringer so ResetPeriod can
// be passed to %s and similar. The closed-set values
// are already human-readable, so this is a no-op.
func (r ResetPeriod) String() string {
	return string(r)
}

// Plan is the panel-side view of a single tariff.
// The fields mirror the `plans` table one-to-one;
// the JSON tags match the wire format used by the
// admin REST API.
//
// # JSON wire format
//
// The JSON tags are snake_case to match the
// pre-existing `users.User` API contract and the
// pre-existing `subscription.Plan` struct (which is
// read-only). The Duration field is exposed as
// `duration_ns` (nanoseconds) for symmetry with the
// pre-existing subscription.Plan; the front-end
// can convert to a human-readable "30 days" string
// at the rendering layer.
//
// # UpdatedAt
//
// The DB column is `updated_at TIMESTAMPTZ NOT NULL
// DEFAULT NOW()`. The pgx layer relies on the
// column DEFAULT on INSERT and stamps NOW() in the
// UPDATE statement; the Service layer never sets
// UpdatedAt explicitly on Create. The MemoryStore
// uses its own clock for both.
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

// IsValid reports whether p carries the minimum data
// the Store requires to accept a Create or Update. It
// is intentionally permissive — heavy validation
// (Name format, Duration bounds, ResetPeriod enum)
// lives in the Service so it can return rich
// per-field errors via *ValidationError. IsValid is
// the cheap pre-flight the store uses to reject
// obviously broken inserts.
func (p *Plan) IsValid() bool {
	if p == nil {
		return false
	}
	if p.Name == "" {
		return false
	}
	if p.TrafficLimitBytes < 0 {
		return false
	}
	if p.Duration <= 0 {
		return false
	}
	if p.DeviceLimit < 0 {
		return false
	}
	if p.PriceCents < 0 {
		return false
	}
	if !p.ResetPeriod.IsValid() {
		return false
	}
	return true
}

// String is a debug helper. The output is intentionally
// redacted — we do NOT log the per-plan details in
// production (the audit log is the system of record
// for who changed what).
func (p *Plan) String() string {
	if p == nil {
		return "<nil plan>"
	}
	return fmt.Sprintf("Plan{id=%s name=%q reset=%s duration=%s}", p.ID, p.Name, p.ResetPeriod, p.Duration)
}
