// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package audits — PostgreSQL-backed implementation
// of Store. Uses the `audit_log` table added in
// migration 0001_initial.sql.
//
// # Schema recap
//
//	CREATE TABLE audit_log (
//	    id              BIGSERIAL PRIMARY KEY,
//	    actor_id        UUID REFERENCES admins(id) ON DELETE SET NULL,
//	    actor_username  TEXT,
//	    action          TEXT NOT NULL,
//	    resource_type   TEXT NOT NULL,
//	    resource_id     TEXT,
//	    before          JSONB,
//	    after           JSONB,
//	    ip              INET,
//	    user_agent      TEXT,
//	    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
//	);
//
// # Why a hand-rolled WHERE, not a query builder
//
// The filter has at most six predicates (actor_id,
// action, resource_type, resource_id, since, until)
// and the SQL is a single SELECT. A query builder
// would be more lines of code than the predicate
// assembly. The values themselves are bound
// parameters — the only interpolation is the
// `WHERE` clause fragments, which are constants
// in this file.
//
// # Why bigserial IDs are stringified on the wire
//
// The pgx column is BIGINT; on the wire we render
// as a string so JavaScript clients can compare
// without losing precision past 2^53. The list
// path is paginated by created_at (not by id), so
// the value's type is not load-bearing for the
// UI.
package audits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the PostgreSQL-backed Store. It is
// safe for concurrent use; pgxpool handles
// connection pooling.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore wires a PgStore from an open
// pgxpool. The pool is owned by the caller.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// Insert appends a new entry. The ID is assigned
// by the bigserial DEFAULT; CreatedAt is filled
// by the schema's NOW() default when the caller
// passes a zero time.
func (s *PgStore) Insert(ctx context.Context, e Entry) (*AuditEntry, error) {
	if e.Action == "" {
		return nil, fmt.Errorf("audits: Insert: action is required")
	}
	if e.ResourceType == "" {
		return nil, fmt.Errorf("audits: Insert: resource_type is required")
	}

	// Optional actor id. The schema has ON DELETE
	// SET NULL on the FK so an actor that is
	// later deleted does not break the row.
	var actorID *uuid.UUID
	if e.ActorID != "" {
		id, err := uuid.Parse(e.ActorID)
		if err != nil {
			return nil, fmt.Errorf("audits: Insert: parse actor_id: %w", err)
		}
		actorID = &id
	}

	// Trim the User-Agent to 512 chars. The
	// column is TEXT, but a malicious UA can
	// bloat the row and bloat the table over
	// time. The cap is generous — real UAs are
	// 100-200 chars; 512 leaves room for
	// pathological but legitimate cases.
	ua := e.UserAgent
	if len(ua) > 512 {
		ua = ua[:512]
	}

	// Optional IP. INET accepts the empty string
	// as the literal "" (zero address); we want
	// NULL for "no client IP recorded" so the
	// column is nullable-friendly.
	var ip any
	if e.IP != "" {
		if parsed := net.ParseIP(e.IP); parsed != nil {
			ip = parsed.String()
		}
	}

	// Optional explicit CreatedAt. The schema's
	// default is NOW() if NULL.
	var createdAt any
	if !e.CreatedAt.IsZero() {
		createdAt = e.CreatedAt.UTC()
	}

	const q = `
		INSERT INTO audit_log (
			actor_id, actor_username, action, resource_type, resource_id,
			before, after, ip, user_agent, created_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, COALESCE($10::TIMESTAMPTZ, NOW())
		)
		RETURNING id, created_at`

	var (
		newID int64
		newTS time.Time
	)
	if err := s.pool.QueryRow(ctx, q,
		actorID,
		nullString(e.ActorUsername),
		e.Action,
		e.ResourceType,
		nullString(e.ResourceID),
		e.Before,
		e.After,
		ip,
		nullString(ua),
		createdAt,
	).Scan(&newID, &newTS); err != nil {
		return nil, fmt.Errorf("audits: Insert: %w", err)
	}

	// Build the response row. We re-shape the
	// input through the AuditEntry type so the
	// MemoryStore and PgStore return the same
	// shape (caller-side interchangeability).
	return &AuditEntry{
		ID:            fmt.Sprintf("%d", newID),
		ActorID:       e.ActorID,
		ActorUsername: e.ActorUsername,
		Action:        e.Action,
		ResourceType:  e.ResourceType,
		ResourceID:    e.ResourceID,
		Before:        e.Before,
		After:         e.After,
		IP:            e.IP,
		UserAgent:     ua,
		CreatedAt:     newTS.UTC(),
	}, nil
}

// List returns entries matching the filter,
// ordered by created_at DESC. The /{id} path
// returns Before / After in full; the list path
// elides them.
func (s *PgStore) List(ctx context.Context, f ListFilter) ([]*AuditEntry, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}

	// Build the WHERE clause. Each predicate is
	// ANDed; the only interpolation is the
	// predicate text (constants) — the values
	// are bound parameters.
	var (
		conds []string
		args  []any
	)
	if f.ActorID != "" {
		args = append(args, f.ActorID)
		conds = append(conds, fmt.Sprintf("actor_id = $%d", len(args)))
	}
	if f.Action != "" {
		args = append(args, f.Action)
		conds = append(conds, fmt.Sprintf("action = $%d", len(args)))
	}
	if f.ResourceType != "" {
		args = append(args, f.ResourceType)
		conds = append(conds, fmt.Sprintf("resource_type = $%d", len(args)))
	}
	if f.ResourceID != "" {
		args = append(args, f.ResourceID)
		conds = append(conds, fmt.Sprintf("resource_id = $%d", len(args)))
	}
	if !f.Since.IsZero() {
		args = append(args, f.Since.UTC())
		conds = append(conds, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if !f.Until.IsZero() {
		args = append(args, f.Until.UTC())
		conds = append(conds, fmt.Sprintf("created_at <= $%d", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, limit)
	q := fmt.Sprintf(`
		SELECT id, actor_id, actor_username, action, resource_type,
		       resource_id, ip, user_agent, created_at
		FROM audit_log
		%s
		ORDER BY created_at DESC, id DESC
		LIMIT $%d`, where, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("audits: List: %w", err)
	}
	defer rows.Close()
	out := make([]*AuditEntry, 0)
	for rows.Next() {
		row, err := scanAuditRow(rows, true /* elideBeforeAfter */)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audits: List rows: %w", err)
	}
	return out, nil
}

// GetByID returns the full row. ErrNotFound if no
// such row.
func (s *PgStore) GetByID(ctx context.Context, id string) (*AuditEntry, error) {
	const q = `
		SELECT id, actor_id, actor_username, action, resource_type,
		       resource_id, ip, user_agent, created_at, before, after
		FROM audit_log
		WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	out, err := scanAuditRowFull(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("id %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("audits: GetByID: %w", err)
	}
	return out, nil
}

// scanAuditRow reads the list-path columns. The
// Before / After blobs are NOT selected here (the
// list path is bandwidth-conscious). The shape
// argument keeps the two scan helpers parallel.
func scanAuditRow(rows pgx.Rows, elideBeforeAfter bool) (*AuditEntry, error) {
	var (
		id            int64
		actorID       *uuid.UUID
		actorUsername *string
		action        string
		resourceType  string
		resourceID    *string
		ip            *netip.Addr
		userAgent     *string
		createdAt     time.Time
	)
	if err := rows.Scan(
		&id, &actorID, &actorUsername, &action, &resourceType,
		&resourceID, &ip, &userAgent, &createdAt,
	); err != nil {
		return nil, fmt.Errorf("audits: scan list row: %w", err)
	}
	out := &AuditEntry{
		ID:           fmt.Sprintf("%d", id),
		Action:       action,
		ResourceType: resourceType,
		CreatedAt:    createdAt.UTC(),
	}
	if actorID != nil {
		out.ActorID = actorID.String()
	}
	if actorUsername != nil {
		out.ActorUsername = *actorUsername
	}
	if resourceID != nil {
		out.ResourceID = *resourceID
	}
	if ip != nil {
		out.IP = ip.String()
	}
	if userAgent != nil {
		out.UserAgent = *userAgent
	}
	if elideBeforeAfter {
		out.Before = nil
		out.After = nil
	}
	return out, nil
}

// scanAuditRowFull is the /{id}-path variant that
// reads Before / After as well. We use a
// *pgxpool.Conn-shaped interface (anything with
// Scan) so the helper is testable.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanAuditRowFull(row rowScanner) (*AuditEntry, error) {
	var (
		id            int64
		actorID       *uuid.UUID
		actorUsername *string
		action        string
		resourceType  string
		resourceID    *string
		ip            *netip.Addr
		userAgent     *string
		createdAt     time.Time
		before        []byte
		after         []byte
	)
	if err := row.Scan(
		&id, &actorID, &actorUsername, &action, &resourceType,
		&resourceID, &ip, &userAgent, &createdAt, &before, &after,
	); err != nil {
		return nil, err
	}
	out := &AuditEntry{
		ID:           fmt.Sprintf("%d", id),
		Action:       action,
		ResourceType: resourceType,
		CreatedAt:    createdAt.UTC(),
	}
	if actorID != nil {
		out.ActorID = actorID.String()
	}
	if actorUsername != nil {
		out.ActorUsername = *actorUsername
	}
	if resourceID != nil {
		out.ResourceID = *resourceID
	}
	if ip != nil {
		out.IP = ip.String()
	}
	if userAgent != nil {
		out.UserAgent = *userAgent
	}
	if len(before) > 0 {
		out.Before = jsonRawMessage(before)
	}
	if len(after) > 0 {
		out.After = jsonRawMessage(after)
	}
	return out, nil
}

// nullString is a tiny helper to keep the Insert
// call readable. Empty string -> nil -> NULL.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// jsonRawMessage wraps the JSONB bytes in a
// json.RawMessage so the JSON encoder emits them
// verbatim (rather than base64-encoding the
// []byte). The pgx scan already returns the raw
// bytes from the JSONB column; we just relabel.
func jsonRawMessage(b []byte) any {
	return json.RawMessage(b)
}

// Verify pgconn is used in this file (the Insert
// error path references it for completeness even
// though the current schema does not have any
// constraint that can fire). Keeping the import
// here means a future constraint (e.g. CHECK on
// action) does not need to add the import again.
var _ = (*pgconn.PgError)(nil)
