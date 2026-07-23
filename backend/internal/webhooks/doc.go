// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package webhooks is reserved for the outgoing webhook delivery
// described in the v9 roadmap §21 Phase 2 entry for v1.3.0
// ("Webhooks (HMAC-SHA256 + anti-replay + exponential backoff +
// DLQ) — 1-2 нед").
//
// The contract will mirror what `internal/audits` records but
// delivered to operator-configured HTTP endpoints with:
//
//   - HMAC-SHA256 over the canonical JSON body, signature in
//     `X-Aegis-Signature`, timestamp in `X-Aegis-Timestamp`.
//   - Anti-replay: receiver rejects events older than 5 minutes.
//   - Exponential backoff (1s, 5s, 25s, 2m15s, 11m15s) with a
//     hard ceiling of 24h.
//   - Dead-letter queue: events that exhaust the retry budget
//     are kept in a `webhook_dlq` table for manual replay.
//
// The webhook is the *outgoing* surface. The reverse — external
// callers pinging the panel — is handled in the existing
// `internal/router` mount and does not need a new package.
//
// Intentionally empty in v0.3.0. Adding types or imports here
// without a corresponding ADR is a smell — flag it in code
// review.
package webhooks
