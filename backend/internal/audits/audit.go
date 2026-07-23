// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package audits implements the v0.2.0 audit-log
// surface: a read API for the operator UI plus a
// Record(...) helper the rest of the panel calls
// after a mutating handler returns success.
//
// # Wire format
//
// AuditEntry is the on-the-wire shape the v0.2.0
// GET /api/v1/audits endpoint returns. Field names
// are camelCase to match the rest of the panel
// (per the PR-G / PR-H / PR-I / PR-F normalisation
// that landed across v0.2.0). The `before` and
// `after` fields are JSONB blobs and are rendered
// to the client as `unknown | object`. A separate
// endpoint (`GET /api/v1/audits/{id}`) returns the
// same shape with the `before` / `after` fields
// filled in.
//
// # Storage
//
// MemoryStore is the Phase 0 default. PgStore
// (backed by the `audit_log` table from migration
// 0001) is selected by AEGIS_AUDITS_BACKEND=pg. The
// table already has the right shape — id, actor_id,
// actor_username, action, resource_type, resource_id,
// before, after, ip, user_agent, created_at — and
// the v0.2.0 work does not need a new migration.
//
// # What is NOT here
//
// The mutating handler call-sites that produce audit
// entries land in v0.3 alongside the v0.3 multi-
// replica work. v0.2.0 ships the read API + the
// Record(...) helper, so the v0.3 wiring is a
// mechanical `audits.Record(ctx, audits.Entry{...})`
// after every successful PATCH / POST / DELETE in
// the nodes / hosts / inbounds / users / panelcfg
// handlers. The change-password handler added in
// the same PR writes its own entry as a smoke test
// for the write path.

package audits

import (
	"time"

	"github.com/google/uuid"
)

// AuditEntry is the canonical audit-log row. The
// wire shape (camelCase json tags) matches the v0.2.0
// normalisation; the on-disk columns are snake_case
// (the pgx scan step below maps the two).
type AuditEntry struct {
	// ID is a bigserial in the pg schema; on the
	// wire it renders as a string for stable JSON
	// parsing across languages. The MemoryStore
	// mints a fresh uint64 on every Insert; the
	// PgStore delegates to the bigserial DEFAULT.
	ID string `json:"id"`

	// ActorID is the admin UUID that triggered the
	// action. Empty for system-driven entries (e.g.
	// the noop-CoreProvider's "agent timeout" log
	// in v0.3+).
	ActorID string `json:"actorId,omitempty"`

	// ActorUsername is denormalised from admins.username
	// at write time so the read path does not need
	// to join. The pgx scan reads it as a nullable
	// string; the MemoryStore sets it from the
	// input.
	ActorUsername string `json:"actorUsername,omitempty"`

	// Action is a short, dotted, machine-friendly
	// verb. The convention is `<resource>.<verb>`
	// (e.g. `node.create`, `user.rotate_token`,
	// `panelcfg.rotate`). Free-form for now; v0.3
	// will pin the closed set in the OpenAPI spec
	// so the UI's filter dropdown can render the
	// full list without scraping the data.
	Action string `json:"action"`

	// ResourceType is the noun side of the action
	// (`node`, `user`, `host`, `inbound`, `panelcfg`,
	// `auth`, `core`). The pgx schema stores it as
	// TEXT NOT NULL; the convention is the singular
	// snake_case of the URL path's first segment.
	ResourceType string `json:"resourceType"`

	// ResourceID is the target's primary key as a
	// string. UUIDs render as their canonical
	// hyphenated form. Empty for collection-level
	// actions (`nodes.bulk_import`).
	ResourceID string `json:"resourceId,omitempty"`

	// Before is the pre-mutation state. JSONB in
	// pgx, opaque `[]byte` in MemoryStore. The
	// wire shape is `unknown | object` — the
	// v0.2.0 GET / endpoint emits a placeholder
	// (`null` for the list path, full content for
	// the /{id} path).
	Before any `json:"before,omitempty"`

	// After is the post-mutation state. Same
	// caveats as Before.
	After any `json:"after,omitempty"`

	// IP is the client IP, copied from
	// `middleware.GetClientIP(r.Context())` in
	// `RecordFromRequest`. The chi v5.3
	// ClientIPFrom* middlewares (mounted in
	// router.go) set the IP — we never read
	// `r.RemoteAddr` directly because the
	// deprecated `middleware.RealIP` used to
	// mutate it and the new middlewares do not.
	// The pgx column is INET; empty string
	// serialises to NULL.
	IP string `json:"ip,omitempty"`

	// UserAgent is the raw User-Agent header value.
	// pgx stores as TEXT; empty string serialises
	// to NULL. Capped at 512 chars on the
	// pgx-INSERT path so a malicious UA cannot
	// bloat the row.
	UserAgent string `json:"userAgent,omitempty"`

	// CreatedAt is the wall-clock time the entry
	// was minted. UTC on the wire; the pgx column
	// is TIMESTAMPTZ.
	CreatedAt time.Time `json:"createdAt"`
}

// Entry is the input shape to Service.Record. Most
// fields are optional; the only required ones are
// Action and ResourceType. ID + CreatedAt are
// assigned by the Store. The wire-shape AuditEntry
// and the input Entry share most fields, but
// separating them lets us tighten the input contract
// without breaking the read path.
type Entry struct {
	ActorID       string
	ActorUsername string
	Action        string
	ResourceType  string
	ResourceID    string
	Before        any
	After         any
	IP            string
	UserAgent     string
	// Optional override for the timestamp. Tests
	// use this to mint deterministic entries;
	// production code leaves it zero and the
	// Service fills it with time.Now().UTC().
	CreatedAt time.Time
}

// ListFilter is the input to Service.List. Every
// field is optional; an empty filter returns every
// entry in descending created_at order. The pgx
// implementation builds the WHERE clause
// dynamically (a string-built query is safe here
// because the only interpolation point is a
// deterministic number of ANDed predicates; the
// values themselves are bound parameters).
type ListFilter struct {
	// ActorID filters by actor_id. Empty means no
	// filter.
	ActorID string

	// Action is an exact match on the action
	// column. Empty means no filter.
	Action string

	// ResourceType is an exact match. Empty means
	// no filter.
	ResourceType string

	// ResourceID is an exact match. Empty means
	// no filter.
	ResourceID string

	// Since / Until are inclusive time bounds.
	// Zero means no bound. Both must be UTC; the
	// caller is expected to pass UTC times.
	Since time.Time
	Until time.Time

	// Limit caps the result. 0 means default
	// (DefaultListLimit, currently 100). The
	// hard cap is MaxListLimit (currently 1000)
	// — values above that are clamped down.
	Limit int
}

// Default + max bounds for List. Pinned here so the
// HTTP handler does not need to know about the
// limits; the Service clamps input to these.
const (
	DefaultListLimit = 100
	MaxListLimit     = 1000
)

// SystemActorUsername is the actor identifier for
// system-driven entries (no authenticated user is
// on the line). Used by the v0.3+ agent-timeout and
// apply-failure log paths.
const SystemActorUsername = "system"

// ParseUUID is a tiny helper so handler code reads
// cleanly. Returns (uuid.Nil, false) on parse error
// so the caller can early-return with a 400.
func ParseUUID(raw string) (uuid.UUID, bool) {
	if raw == "" {
		return uuid.Nil, true // empty is valid
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}
