// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Delivery is the record of a single POST attempt
// the dispatcher made to an endpoint. Every retry
// is its own Delivery row (so the operator can see
// the full attempt history) and the DLQ row
// references the same payload for replay.
//
// # Snapshot fields
//
// The `endpoint_id` column has an ON DELETE CASCADE
// FK so deleting an endpoint removes its delivery
// history. The `request_url` column is a snapshot
// of the URL at dispatch time — the operator can
// edit the endpoint URL afterwards without
// rewriting the delivery history.
//
// The `request_body` is the exact bytes the panel
// sent (post-canonicalisation). Storing the raw
// bytes (not a re-serialised struct) means a future
// operator who needs to replay manually can
// reproduce the exact request the receiver saw.
//
// `signature` is the value of the
// `X-Aegis-Signature` header (already in the
// `sha256=<hex>` form). The DLQ row carries the
// same signature for replay, so the receiver
// cannot distinguish a replayed delivery from a
// fresh one without checking the timestamp.
//
// # Response snapshot
//
// `status_code` is the HTTP response code (nil if
// the request never got a response — DNS failure,
// TCP RST, timeout, etc.). `response_body` is the
// first 4 KiB of the response body (truncated),
// enough to surface "the receiver is complaining
// about X" without bloating the row. `error` is
// the transport error for the no-response case.

package webhooks

import (
	"time"

	"github.com/google/uuid"
)

// MaxResponseBodyLen caps the stored response body
// snapshot. The dispatcher stores the first N bytes
// of the receiver's response so the operator can
// diagnose receiver-side errors without bloating
// the deliveries table. 4 KiB matches the size of a
// typical 4xx page (HTML or JSON).
const MaxResponseBodyLen = 4096

// MaxPayloadLen caps the stored event payload. The
// canonical JSON body is bounded by the event
// type's typical size; user/plan/host payloads
// are well under 1 KiB. 64 KiB leaves headroom for
// future "rich" event types (e.g. node dump on
// disconnect) without letting a single delivery
// bloat a row.
const MaxPayloadLen = 64 * 1024

// Delivery is one POST attempt the dispatcher
// made. The dispatcher writes one Delivery per
// attempt; the DLQ row carries the failed
// delivery's payload forward for replay.
type Delivery struct {
	ID           uuid.UUID `json:"id"`
	EndpointID   uuid.UUID `json:"endpoint_id"`
	EventType    EventType `json:"event_type"`
	Payload      []byte    `json:"-"` // body bytes; not re-serialised on read
	RequestURL   string    `json:"request_url"`
	RequestBody  []byte    `json:"-"` // canonical body bytes
	Signature    string    `json:"signature"`
	Timestamp    time.Time `json:"timestamp"`
	StatusCode   *int      `json:"status_code,omitempty"`
	ResponseBody string    `json:"response_body,omitempty"`
	Error        string    `json:"error,omitempty"`
	Attempt      int       `json:"attempt"`
	DurationMs   *int      `json:"duration_ms,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// DeliveryStatus reports the high-level outcome
// of a delivery attempt. The admin UI maps this
// to a colour-coded chip (green / yellow / red).
type DeliveryStatus string

// Delivery status values.
const (
	// DeliveryStatusSuccess is a 2xx response.
	DeliveryStatusSuccess DeliveryStatus = "success"
	// DeliveryStatusRetry is a non-2xx response
	// or a transport error that triggered a
	// retry. The next Delivery row is the retry
	// attempt; the operator can follow the chain
	// by the (endpoint_id, event_type, created_at)
	// tuple.
	DeliveryStatusRetry DeliveryStatus = "retry"
	// DeliveryStatusFailed is a delivery that
	// exhausted the retry budget and was moved
	// to the DLQ. The matching DLQEntry is
	// linked by (endpoint_id, event_type,
	// enqueued_at).
	DeliveryStatusFailed DeliveryStatus = "failed"
)

// DLQEntry is a failed delivery the operator can
// replay manually. The DLQ row is a snapshot of
// the delivery at the moment it was moved out of
// the retry path; replaying it creates a fresh
// Delivery (with a new ID, a new signature, and a
// new timestamp) and starts the retry chain over.
//
// # Why no FK to webhook_endpoints
//
// The DLQ row is the operator's safety net for a
// receiver that was down. The operator may have
// deleted the endpoint in the meantime (e.g. they
// gave up and rebuilt the receiver config). We
// keep the DLQ row regardless and snapshot the URL
// at enqueue time. The admin GET endpoint joins
// the live endpoint by id when both still exist
// and falls back to the snapshot URL when the
// endpoint was deleted.
type DLQEntry struct {
	ID            uuid.UUID `json:"id"`
	EndpointID    uuid.UUID `json:"endpoint_id"`
	EndpointURL   string    `json:"endpoint_url"`
	EventType     EventType `json:"event_type"`
	Payload       []byte    `json:"-"` // raw JSON body, identical to Delivery.Payload
	LastError     string    `json:"last_error"`
	Attempts      int       `json:"attempts"`
	LastAttemptAt time.Time `json:"last_attempt_at"`
	EnqueuedAt    time.Time `json:"enqueued_at"`
}
