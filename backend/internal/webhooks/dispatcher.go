// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Dispatcher is the v0.7.x helper every
// mutating handler calls after a successful
// write. It wraps the low-level Service.Dispatch
// with three properties the raw method does not
// have:
//
//  1. Non-blocking: dispatch errors are LOGGED
//     and DROPPED, never returned. The HTTP
//     handler has already committed the write
//     (the user/plan/node/host is in the DB);
//     a webhook failure must not turn a 2xx
//     into a 5xx and cause a client retry that
//     re-applies the mutation.
//
//  2. Nil-safe: the dispatcher accepts a nil
//     *Service and silently no-ops. This lets
//     unit tests construct a package's Service
//     (users, plans, …) without wiring a real
//     webhooks.Service — the existing test
//     suites stay untouched. Production wiring
//     in cmd/aegis/main.go always passes a
//     real service.
//
//  3. Bounded context: a sub-context derived
//     from the request context with a 5s
//     deadline so a hung receiver cannot block
//     the HTTP handler indefinitely. The
//     request context is propagated so a
//     client-side disconnect cancels the
//     dispatch (no point finishing a webhook
//     POST after the client has gone away).
//
// # Why log + drop, not retry
//
// The Service.Dispatch already records a
// Delivery row for every attempt; the
// background retry worker (PR #146) handles
// transient failures on the schedule
// 1s/5s/25s/2m15s/11m15s. So MustDispatch does
// not need its own retry loop. A failed
// dispatch at the call site (a transport
// error from the synchronous POST) is already
// in the retry queue; a context-cancelled
// dispatch (the client gave up) is not
// interesting to the receiver and we drop it.

package webhooks

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// dispatchTimeout caps the time a single
// MustDispatch call may spend in the
// synchronous Service.Dispatch (which POSTs to
// every matching endpoint). 5s matches the
// dispatcher's per-attempt DefaultHTTPTimeout
// (10s) with a comfortable headroom for N
// endpoints. A slower receiver triggers a
// delivery-status row that the background
// retry worker can pick up.
const dispatchTimeout = 5 * time.Second

// MustDispatch fans an event out to every
// matching webhook endpoint. See the package
// doc comment for the nil / context / non-
// blocking semantics.
//
// `eventType` MUST be one of the closed
// EventType constants (webhooks.EventUserCreated,
// webhooks.EventBackupFailed, …). The
// dispatcher does not validate the value —
// `Service.Dispatch` already does — but a
// typo would otherwise fail silently.
//
// `payload` is any JSON-marshalable value. The
// handler typically passes the just-written
// entity struct (users.User, plans.Plan, …)
// so the wire shape is the entity's own JSON
// tags. For events where the entity no longer
// exists (a delete), the handler passes a
// small map[string]string{"id": "..."}.
func MustDispatch(ctx context.Context, svc *Service, eventType EventType, payload any) {
	if svc == nil {
		// webhooks is not wired (unit test, or
		// a future mode that disables the
		// outbound surface). No-op.
		return
	}
	// Bounded sub-context. The parent context
	// propagates so a client-side cancel
	// short-circuits the POST; the deadline
	// caps the synchronous wait if the
	// receiver is just slow.
	dispatchCtx, cancel := context.WithTimeout(ctx, dispatchTimeout)
	defer cancel()
	results, err := svc.Dispatch(dispatchCtx, eventType, payload)
	if err != nil {
		// Service.Dispatch only returns a
		// ValidationError on a malformed
		// event type or nil payload — both
		// are programming bugs. Log and
		// drop; do not crash the HTTP
		// handler.
		log.Warn().
			Err(err).
			Str("event", eventType.String()).
			Msg("webhooks: dispatch failed (programmer error, not a delivery failure)")
		return
	}
	// The Service writes a Delivery row per
	// attempt; the background retry worker
	// handles transient failures. We log the
	// per-endpoint summary at INFO so an
	// operator can grep the panel log for
	// "webhook delivered to N endpoints" in
	// the post-mortem of a real-world event.
	delivered, retried, failed := 0, 0, 0
	for _, r := range results {
		switch r.Status {
		case DeliveryStatusSuccess:
			delivered++
		case DeliveryStatusRetry:
			retried++
		case DeliveryStatusFailed:
			failed++
		default:
			// "" = skipped (disabled or
			// event-type mismatch). Counted
			// separately.
		}
	}
	if delivered+retried+failed > 0 {
		log.Info().
			Str("event", eventType.String()).
			Int("delivered", delivered).
			Int("retried", retried).
			Int("failed", failed).
			Msg("webhooks: event dispatched")
	}
}
