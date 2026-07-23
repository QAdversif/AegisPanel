// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package subscriptions is reserved for the External Squads /
// Network Map UI described in the v9 roadmap §21 Phase 3 entry
// for v2.3.0 ("Network Map UI для cascade + Subscription
// Profile (External Squads-стиль)").
//
// IMPORTANT: do not confuse this with the existing
// `internal/subscription` (singular) package, which is the
// user-facing subscription URL renderer that powers
// `/api/v1/sub/{token}` and the `SubscriptionView`. That
// package is the v0.1.0 / v0.2.0 surface and is fully wired.
//
// This `subscriptions` (plural) package is a different concept:
// it models subscription profiles that aggregate multiple
// remote nodes (External Squads — the v2ray/remnawave pattern
// where the panel re-sells a curated set of outbounds that
// the operator does not control). It only becomes useful
// after Cascade Topology (v2.2.0) lands.
//
// Intentionally empty in v0.3.0. Adding types or imports here
// without a corresponding ADR is a smell — flag it in code
// review.
package subscriptions
