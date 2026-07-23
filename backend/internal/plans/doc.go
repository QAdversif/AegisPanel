// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package plans is reserved for the user-plan model described in
// ARCHITECTURE.md §12 and the v9 roadmap §21 Phase 2 entry for
// v1.2.0 ("Реальный users CRUD + plans + traffic limits +
// Cabinet API"). The `plans` table already exists in the
// initial migration (0001) — it just has no Go-side wiring yet.
//
// The model will own plan tiers, traffic caps, the per-plan
// rotation of node selection, and the rate of plan-bump alerts
// that flow into `internal/notifications`.
//
// Intentionally empty in v0.3.0. Adding types or imports here
// without a corresponding ADR is a smell — flag it in code
// review.
package plans
