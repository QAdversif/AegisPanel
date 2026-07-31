// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package webhooks is the outgoing-webhook surface
// for the panel. The operator configures a list of
// HTTP endpoints; the panel signs every event with
// HMAC-SHA256 and POSTs it to the matching
// endpoints, with exponential-backoff retry and a
// dead-letter queue (DLQ) for deliveries that
// exhaust the retry budget.
//
// # Why this package
//
// Phase 0/1 used audit-log + manual SQL queries as
// the only "tell me what changed" surface. The v9
// roadmap §21 called for an outgoing webhook with
// HMAC signing so external services (n8n, custom
// automation, Telegram bridges) can react in real
// time. v0.7.0 is that work.
//
// # Wire format
//
// Every event the panel wants to publish is
// represented as a (EventType, payload) pair. The
// service.Dispatch method serialises the payload to
// canonical JSON, computes the signature, and POSTs
// the result. The HTTP request shape is:
//
//	POST <endpoint.URL>
//	Content-Type: application/json
//	User-Agent: aegispanel-webhooks/0.7
//	X-Aegis-Event: <event type, e.g. user.created>
//	X-Aegis-Delivery: <delivery UUID>
//	X-Aegis-Timestamp: <RFC 3339 timestamp>
//	X-Aegis-Signature: sha256=<hex hmac>
//
// The signature is `HMAC-SHA256(secret, body)`,
// where `body` is the exact bytes the panel sent
// (post-canonicalisation; the canonical form is
// `json.Marshal` of the payload — keys sorted
// alphabetically, no trailing whitespace). The
// receiver MUST recompute the signature over the
// raw body and use `crypto/hmac.Equal` for the
// comparison (constant-time, see signature.go).
//
// # Anti-replay
//
// The receiver MUST reject any event whose
// `X-Aegis-Timestamp` is more than 5 minutes from
// the receiver's wall clock. The panel honours the
// same window on its own side: a delivery that
// arrives back at the panel more than 5 minutes
// after the panel's clock is treated as "stale"
// (this matters for the manual-replay path: the
// panel signs a fresh delivery on replay, so a
// replayed event always has a current timestamp).
//
// # Retry and DLQ
//
// The retry schedule is exponential (base 5x) with
// five retry intervals: 1s, 5s, 25s, 2m15s, 11m15s.
// After the 5th retry fails (6 attempts total, ~14
// minutes of wall clock), the delivery is moved to
// `webhook_dlq` for manual replay. The operator
// sees DLQ entries on the /webhooks/{id}/dlq admin
// page and can replay them individually
// (POST /webhooks/dlq/{dlq_id}/replay).
//
// # Scope and event subscription
//
// Every endpoint carries an `Events []EventType`
// list. An empty list is the wildcard: the endpoint
// receives every event type. A non-empty list
// narrows the subscription to just the named
// events. The Service filters at Dispatch time, so
// an endpoint subscribed to `user.created` never
// sees a `plan.updated` delivery.
//
// # What is NOT here
//
// v0.7.0 ships the package, the admin CRUD, and a
// test endpoint that publishes a synthetic event.
// The wiring that calls Service.Dispatch from every
// mutating handler (user create/update/delete,
// plan create/update/delete, etc.) lands in a
// follow-up batch alongside the v0.6.x audit-log
// call-site work. v0.7.0 does NOT wire the package
// into the production event flow — the operator
// uses the test endpoint (POST /webhooks/{id}/test)
// to verify their setup, and the v0.7.x follow-up
// batch will turn on the dispatcher.
//
// The HMAC secret is stored in plaintext in
// `webhook_endpoints.secret` for v0.7.0. v0.7.x
// will plug the column through the same sops
// envelope as `panel_path_config` so a panel DB
// dump does not leak the secrets. The Service
// exposes the secret to the admin GET response
// (one-time on create, never again) so the
// operator can copy it to their receiver config.
package webhooks
