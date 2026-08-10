// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Endpoint is the operator-configured HTTP target
// for the outgoing-webhook surface. The Service
// delivers events to every enabled endpoint whose
// `Events` list contains the dispatched event type
// (or whose `Events` list is empty, the wildcard).
//
// # Secret handling
//
// The `secret` column is the HMAC-SHA256 key the
// receiver uses to verify the `X-Aegis-Signature`
// header. v0.7.0 stores it in plaintext in
// `webhook_endpoints.secret`; v0.7.x will move
// the column under the sops envelope. The admin
// handler returns the secret in the response only
// on Create — subsequent GETs return a redacted
// placeholder so the secret does not leak through
// repeated list-page refreshes.
//
// # Last delivery snapshot
//
// `LastDeliveryAt` and `LastStatusCode` are the
// operator-facing "is this endpoint healthy"
// fields. The dispatcher updates them on every
// attempt; the UI uses them to surface a red/green
// dot next to the endpoint in the list view.
// `LastStatusCode` is nil when the last attempt
// failed at the transport layer (no HTTP response).

package webhooks

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// EventType is the typed event the panel publishes.
// The set is closed — values outside the list are
// rejected by the Service so the dispatcher does
// not fan out to endpoints subscribed to nothing.
//
// The naming convention is `<resource>.<verb>`,
// matching the audit log's `action` field. Future
// event types land here as new packages come
// online; v0.7.x will wire every mutating handler
// to dispatch a typed event.
type EventType string

// Event types supported by v0.7.0. The full list
// is the union of every resource the audit log
// already tracks; v0.7.x extends the list as new
// resources come online (nodes, hosts, inbounds).
const (
	// User lifecycle.
	EventUserCreated EventType = "user.created"
	EventUserUpdated EventType = "user.updated"
	EventUserDeleted EventType = "user.deleted"

	// Plan lifecycle.
	EventPlanCreated EventType = "plan.created"
	EventPlanUpdated EventType = "plan.updated"
	EventPlanDeleted EventType = "plan.deleted"

	// Node lifecycle.
	EventNodeCreated EventType = "node.created"
	EventNodeUpdated EventType = "node.updated"
	EventNodeDeleted EventType = "node.deleted"

	// Host lifecycle.
	EventHostCreated EventType = "host.created"
	EventHostUpdated EventType = "host.updated"
	EventHostDeleted EventType = "host.deleted"

	// Backup lifecycle.
	EventBackupCreated   EventType = "backup.created"
	EventBackupCompleted EventType = "backup.completed"
	EventBackupFailed    EventType = "backup.failed"

	// Inbound lifecycle.
	EventInboundCreated EventType = "inbound.created"
	EventInboundUpdated EventType = "inbound.updated"
	EventInboundDeleted EventType = "inbound.deleted"

	// v0.8.x: Inbound template lifecycle.
	EventInboundTemplateCreated EventType = "inbound_template.created"
	EventInboundTemplateUpdated EventType = "inbound_template.updated"
	EventInboundTemplateDeleted EventType = "inbound_template.deleted"
)

// AllowedEventTypes is the closed set the Service
// accepts. Exported so the handler tests can use
// the same list when asserting error messages.
var AllowedEventTypes = []EventType{
	EventUserCreated, EventUserUpdated, EventUserDeleted,
	EventPlanCreated, EventPlanUpdated, EventPlanDeleted,
	EventNodeCreated, EventNodeUpdated, EventNodeDeleted,
	EventHostCreated, EventHostUpdated, EventHostDeleted,
	EventBackupCreated, EventBackupCompleted, EventBackupFailed,
	EventInboundCreated, EventInboundUpdated, EventInboundDeleted,
	EventInboundTemplateCreated, EventInboundTemplateUpdated, EventInboundTemplateDeleted,
}

// IsValid reports whether e is in the closed set.
func (e EventType) IsValid() bool {
	for _, v := range AllowedEventTypes {
		if v == e {
			return true
		}
	}
	return false
}

// String implements fmt.Stringer so EventType can
// be passed to %s and similar.
func (e EventType) String() string {
	return string(e)
}

// SecretRedacted is the placeholder the Service
// returns in the JSON wire format instead of the
// real secret (the secret is shown once on Create
// and never again). Matches the convention used
// by other admin surfaces (e.g. the API key view).
const SecretRedacted = "***"

// Endpoint is the panel-side view of a single
// operator-configured webhook target.
//
// # JSON wire format
//
// The JSON tags are snake_case to match the
// pre-existing admin surface (users.User,
// plans.Plan, etc.). The `events` field is
// rendered as a JSON array of strings; an empty
// slice (or null) means "all events".
//
// The `secret` field is the special case: it
// appears verbatim on Create (so the operator can
// copy it to their receiver) and as SecretRedacted
// on every subsequent read. The Service enforces
// the redaction so the Store and the handler do
// not have to.
type Endpoint struct {
	ID             uuid.UUID   `json:"id"`
	URL            string      `json:"url"`
	Secret         string      `json:"secret"`
	Events         []EventType `json:"events"`
	Enabled        bool        `json:"enabled"`
	LastDeliveryAt *time.Time  `json:"last_delivery_at,omitempty"`
	LastStatusCode *int        `json:"last_status_code,omitempty"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// IsValid reports whether e carries the minimum
// data the Store requires to accept a Create or
// Update. Heavy validation (URL format, event set
// closed-enum check) lives in the Service so it
// can return rich per-field errors via
// *ValidationError.
func (e *Endpoint) IsValid() bool {
	if e == nil {
		return false
	}
	if e.URL == "" {
		return false
	}
	if e.Secret == "" {
		return false
	}
	return true
}

// MatchesEvent reports whether the endpoint wants
// to receive an event of the given type. An empty
// Events slice is the wildcard.
func (e *Endpoint) MatchesEvent(t EventType) bool {
	if len(e.Events) == 0 {
		return true
	}
	for _, want := range e.Events {
		if want == t {
			return true
		}
	}
	return false
}

// String is a debug helper. The URL is included so
// the operator can spot the wrong endpoint in a
// flood of logs; the secret is redacted.
func (e *Endpoint) String() string {
	if e == nil {
		return "<nil endpoint>"
	}
	return fmt.Sprintf("Endpoint{id=%s url=%q events=%d enabled=%t}",
		e.ID, e.URL, len(e.Events), e.Enabled)
}

// validateURL is the cheap pre-flight the Service
// uses. We allow http and https; anything else
// (file://, ftp://, javascript:, etc.) is
// rejected. The check is on the parsed URL's
// Scheme field, not the raw string, so a URL like
// `https://evil.com\@safe.com/` is correctly
// rejected.
func validateURL(raw string) error {
	if raw == "" {
		return &ValidationError{Field: "url", Message: "must be non-empty"}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return &ValidationError{Field: "url", Message: "invalid URL: " + err.Error()}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return &ValidationError{Field: "url", Message: "must be http or https"}
	}
	if u.Host == "" {
		return &ValidationError{Field: "url", Message: "must have a host"}
	}
	return nil
}

// validateSecret enforces the length and character
// set. The secret is used as the HMAC key, so
// 32-256 printable ASCII characters is the sweet
// spot (matches the existing panel-api-key
// generation in the bootstrap package). Shorter
// values are accepted (the operator may have
// imported a pre-existing key) but below 16 chars
// the dispatcher emits a one-shot WARN log.
func validateSecret(s string) error {
	if s == "" {
		return &ValidationError{Field: "secret", Message: "must be non-empty"}
	}
	trimmed := strings.TrimSpace(s)
	if trimmed != s {
		return &ValidationError{Field: "secret", Message: "must not have leading or trailing whitespace"}
	}
	if len(s) < 16 {
		return &ValidationError{Field: "secret", Message: "must be at least 16 characters"}
	}
	if len(s) > 256 {
		return &ValidationError{Field: "secret", Message: "must be at most 256 characters"}
	}
	return nil
}
