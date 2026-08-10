// SPDX-License-Identifier: AGPL-3.0-or-later

package inboundtemplates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the PostgreSQL-backed implementation of
// Store. It uses the `inbound_templates` table
// (migration 0021_inbound_templates.sql). The Params
// JSONB column mirrors the Go model one-to-one.
//
// # Shape
//
// A single InboundTemplate is one row in
// `inbound_templates`. The UNIQUE (name) constraint
// is mapped to ErrDuplicate; the protocol CHECK
// constraint matches the Go allowedProtocols set.
// There is no separate params table; Params is a
// JSONB blob on the row.
//
// # Cross-entity
//
// The `inbounds.template_id` FK (added in the same
// migration) has ON DELETE SET NULL, so deleting a
// template drops the FK on referencing inbounds to
// NULL — the inbounds fall back to the inline-params
// path (the v0.8.0-v0.8.12 default).
//
// # Concurrency
//
// pgxpool handles connection pooling. Each call uses
// its own connection from the pool. Create and Update
// are single-statement operations (no children to
// atomically insert), so no explicit transaction is
// needed.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore wires a PgStore from an open pgxpool. The
// pool is owned by the caller — close it when the
// application shuts down.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// Create inserts a new template row. ErrDuplicate is
// returned on (name) collisions.
func (s *PgStore) Create(ctx context.Context, t *InboundTemplate) error {
	if t == nil {
		return fmt.Errorf("create: nil template")
	}
	if t.ID == uuid.Nil {
		return fmt.Errorf("create: zero id")
	}
	const q = `
		INSERT INTO inbound_templates (
			id, name, protocol, params, description
		) VALUES ($1, $2, $3, $4, $5)`
	_, err := s.pool.Exec(ctx, q,
		t.ID,
		t.Name,
		string(t.Protocol),
		mustMarshal(t.Params),
		nullableText(t.Description),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("name %q: %w", t.Name, ErrDuplicate)
		}
		return fmt.Errorf("insert template: %w", err)
	}
	return nil
}

// GetByID returns the template with the given id, or
// ErrNotFound.
func (s *PgStore) GetByID(ctx context.Context, id uuid.UUID) (*InboundTemplate, error) {
	return s.scanOne(ctx, `WHERE id = $1`, id)
}

// GetByName returns the template with the given name,
// or ErrNotFound. The name is unique per the
// migration's UNIQUE (name) constraint.
func (s *PgStore) GetByName(ctx context.Context, name string) (*InboundTemplate, error) {
	return s.scanOne(ctx, `WHERE name = $1`, name)
}

// GetManyByID returns a map keyed by template id with
// the matching *InboundTemplate. The lookup is a
// single batch query (`WHERE id = ANY($1)`); ids that
// don't resolve are omitted from the result. An empty
// `ids` slice returns an empty (not nil) map.
func (s *PgStore) GetManyByID(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*InboundTemplate, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]*InboundTemplate{}, nil
	}
	const q = baseSelect + `
		WHERE id = ANY($1)`
	rows, err := s.pool.Query(ctx, q, ids)
	if err != nil {
		return nil, fmt.Errorf("query templates by id batch: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID]*InboundTemplate, len(ids))
	for rows.Next() {
		var (
			id          uuid.UUID
			name        string
			protocol    string
			paramsRaw   []byte
			description *string
			createdAt   time.Time
			updatedAt   time.Time
		)
		if err := rows.Scan(
			&id, &name, &protocol, &paramsRaw, &description,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan template: %w", err)
		}
		t := &InboundTemplate{
			ID:        id,
			Name:      name,
			Protocol:  Protocol(protocol),
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		}
		if description != nil {
			t.Description = *description
		}
		if len(paramsRaw) > 0 {
			if err := json.Unmarshal(paramsRaw, &t.Params); err != nil {
				return nil, fmt.Errorf("template params: %w", err)
			}
		}
		if t.Params == nil {
			t.Params = map[string]any{}
		}
		out[id] = t
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return out, nil
}

// List returns every template, sorted by Name
// ascending.
func (s *PgStore) List(ctx context.Context) ([]*InboundTemplate, error) {
	const q = baseSelect + `
		ORDER BY name`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query templates: %w", err)
	}
	defer rows.Close()
	return scanTemplates(rows)
}

// ListByProtocol returns every template with the
// given protocol, sorted by Name ascending.
func (s *PgStore) ListByProtocol(ctx context.Context, p Protocol) ([]*InboundTemplate, error) {
	const q = baseSelect + `
		WHERE protocol = $1
		ORDER BY name`
	rows, err := s.pool.Query(ctx, q, string(p))
	if err != nil {
		return nil, fmt.Errorf("query templates: %w", err)
	}
	defer rows.Close()
	return scanTemplates(rows)
}

// Update replaces the stored copy of t.ID. ErrNotFound
// if the id is unknown; ErrDuplicate if the rename
// would collide with an existing row.
func (s *PgStore) Update(ctx context.Context, t *InboundTemplate) error {
	if t == nil || t.ID == uuid.Nil {
		return fmt.Errorf("update: missing id")
	}
	const q = `
		UPDATE inbound_templates SET
			name = $2,
			protocol = $3,
			params = $4,
			description = $5,
			updated_at = NOW()
		WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q,
		t.ID,
		t.Name,
		string(t.Protocol),
		mustMarshal(t.Params),
		nullableText(t.Description),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("name %q: %w", t.Name, ErrDuplicate)
		}
		return fmt.Errorf("update template: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("id %s: %w", t.ID, ErrNotFound)
	}
	return nil
}

// Delete removes the template with the given id.
// Returns ErrNotFound if no such template exists.
// Note: the `inbounds.template_id` FK has ON DELETE
// SET NULL, so referencing inbounds fall back to the
// inline-params path (no cascade).
func (s *PgStore) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM inbound_templates WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete template: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	return nil
}

// --- internal: scan helpers --------------------------------------------

// baseSelect is the SELECT clause used by every read.
// The order matches the column list expected by
// scanTemplate.
const baseSelect = `
	SELECT
		id, name, protocol, params, description,
		created_at, updated_at
	FROM inbound_templates`

// scanOne runs a single-row query (the caller
// supplies the WHERE clause and args) and returns
// the template. ErrNotFound when the result set is
// empty.
func (s *PgStore) scanOne(ctx context.Context, where string, args ...any) (*InboundTemplate, error) {
	rows, err := s.pool.Query(ctx, baseSelect+" "+where, args...)
	if err != nil {
		return nil, fmt.Errorf("query template: %w", err)
	}
	defer rows.Close()
	out, err := scanTemplates(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w", ErrNotFound)
	}
	return out[0], nil
}

// scanTemplates reads the rows into a slice. An
// empty result returns (nil, nil) — the caller
// distinguishes "empty" from "error" and wraps it
// in ErrNotFound when applicable.
func scanTemplates(rows pgx.Rows) ([]*InboundTemplate, error) {
	out := make([]*InboundTemplate, 0)
	for rows.Next() {
		var (
			id          uuid.UUID
			name        string
			protocol    string
			paramsRaw   []byte
			description *string
			createdAt   time.Time
			updatedAt   time.Time
		)
		if err := rows.Scan(
			&id, &name, &protocol, &paramsRaw, &description,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan template: %w", err)
		}
		t := &InboundTemplate{
			ID:        id,
			Name:      name,
			Protocol:  Protocol(protocol),
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		}
		if description != nil {
			t.Description = *description
		}
		// Params: stored as JSONB object. NULL in the
		// column round-trips as an empty map; the
		// migration's `NOT NULL DEFAULT '{}'::JSONB`
		// ensures the column is never NULL in
		// practice, but we handle the nil case for
		// defence-in-depth.
		if len(paramsRaw) > 0 {
			if err := json.Unmarshal(paramsRaw, &t.Params); err != nil {
				return nil, fmt.Errorf("template params: %w", err)
			}
		}
		if t.Params == nil {
			t.Params = map[string]any{}
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return out, nil
}

// --- internal: JSONB helpers -------------------------------------------

// mustMarshal JSON-encodes v for a JSONB column. It
// panics on error because the call sites only pass
// Go types (strings, structs, slices, maps) that
// json.Marshal handles without a runtime error.
func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("json.Marshal: %v", err))
	}
	return b
}

// nullableText returns the description string for
// pgx to bind as a TEXT column. An empty string is
// returned as nil (SQL NULL) — the column is NULL-
// able; the Go model treats empty and absent the
// same way (omitempty in JSON).
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// isUniqueViolation returns true if err is a
// PostgreSQL unique-constraint violation (SQLSTATE
// 23505). pgx surfaces this as a *pgconn.PgError
// with Code "23505".
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
