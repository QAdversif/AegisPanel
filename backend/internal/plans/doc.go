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
// # Storage
//
// The Store interface has two implementations:
//
//   - MemoryStore — in-process, used by unit tests
//     and the dev docker-compose smoke (default when
//     AEGIS_PLANS_BACKEND is unset or =memory).
//   - PgStore — pgx-backed, used when
//     AEGIS_PLANS_BACKEND=pg. Backs onto the `plans`
//     table from migration 0001. The unique violation
//     on (name) is mapped to ErrDuplicate (Postgres
//     SQLSTATE 23505).
//
// # Service layer
//
// Service is the business-logic layer on top of
// Store. It owns input validation (Name format,
// Duration bounds, ResetPeriod enum, non-negative
// numbers) and ID / timestamp generation on Create.
// The HTTP handler (v0.6.0 PR #132) goes through
// Service rather than Store directly so the rules
// stay in one place and a future pgx migration can
// swap the Store without touching validation.
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
