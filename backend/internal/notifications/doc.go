// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package notifications is reserved for outgoing notifications
// (Telegram via n8n, generic webhook) described in the v9 roadmap
// §21 Phase 2 entry for v1.4.0 ("Outgoing notifications (Telegram
// через n8n / generic webhook) — 1 нед").
//
// The package will deliver panel events (node down, reload rate
// alarm, audit-log threshold trip, etc.) to operator-configured
// channels. It deliberately sits next to `internal/audits` so
// the same event sources can drive both the persistent log and
// the real-time alert path.
//
// Intentionally empty in v0.3.0. Adding types or imports here
// without a corresponding ADR is a smell — flag it in code
// review.
package notifications
