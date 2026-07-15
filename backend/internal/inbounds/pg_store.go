// SPDX-License-Identifier: AGPL-3.0-or-later

package inbounds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the PostgreSQL-backed implementation of Store. It uses
// the `inbounds` table (migration 0003_node_inbounds.sql). The Tags
// and Params JSONB columns mirror the Go model one-to-one.
//
// # Shape
//
// A single Inbound is one row in `inbounds`. The (node_id, name)
// and (node_id, listen_port) UNIQUE constraints are mapped to
// ErrDuplicate; the protocol CHECK constraint matches the Go
// allowedProtocols set. There is no separate tags table; Tags is
// a JSONB array on the row.
//
// # Cross-entity
//
// The `node_id` column has `ON DELETE CASCADE`, so deleting a
// node also removes its inbounds — the MemoryStore has no
// equivalent and the integration tests do not exercise this path
// (deleting a node is currently not in the Service interface).
//
// # Concurrency
//
// pgxpool handles connection pooling. Each call uses its own
// connection from the pool. Create and Update are single-statement
// operations (no children to atomically insert), so no explicit
// transaction is needed.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore wires a PgStore from an open pgxpool. The pool is
// owned by the caller — close it when the application shuts
// down.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// Create inserts a new inbound row. ErrDuplicate is returned on
// (node_id, name) or (node_id, listen_port) collisions.
func (s *PgStore) Create(ctx context.Context, i *Inbound) error {
	if i == nil {
		return fmt.Errorf("create: nil inbound")
	}
	if i.ID == uuid.Nil {
		return fmt.Errorf("create: zero id")
	}
	const q = `
		INSERT INTO inbounds (
			id, node_id, name, protocol, listen, listen_port, enabled,
			tags, params
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := s.pool.Exec(ctx, q,
		i.ID,
		i.NodeID,
		i.Name,
		string(i.Protocol),
		i.Listen,
		i.ListenPort,
		i.Enabled,
		mustMarshal(i.Tags),
		mustMarshalOrNil(i.Params),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf(
				"node %s name %q or port %d: %w",
				i.NodeID, i.Name, i.ListenPort, ErrDuplicate,
			)
		}
		return fmt.Errorf("insert inbound: %w", err)
	}
	return nil
}

// GetByID returns the inbound with the given id, or ErrNotFound.
func (s *PgStore) GetByID(ctx context.Context, id uuid.UUID) (*Inbound, error) {
	return s.scanOne(ctx, `WHERE id = $1`, id)
}

// GetByNodeAndName returns the inbound with the given
// (NodeID, Name), or ErrNotFound. The pair is unique per
// migration 0003.
func (s *PgStore) GetByNodeAndName(ctx context.Context, nodeID uuid.UUID, name string) (*Inbound, error) {
	return s.scanOne(ctx, `WHERE node_id = $1 AND name = $2`, nodeID, name)
}

// GetByNodeAndPort returns the inbound with the given
// (NodeID, ListenPort), or ErrNotFound. The pair is unique per
// migration 0003.
func (s *PgStore) GetByNodeAndPort(ctx context.Context, nodeID uuid.UUID, port int) (*Inbound, error) {
	return s.scanOne(ctx, `WHERE node_id = $1 AND listen_port = $2`, nodeID, port)
}

// ListByNode returns every inbound belonging to the given node,
// sorted by ListenPort ascending. The slice is freshly allocated
// and safe for the caller to mutate.
func (s *PgStore) ListByNode(ctx context.Context, nodeID uuid.UUID) ([]*Inbound, error) {
	const q = baseSelect + `
		WHERE node_id = $1
		ORDER BY listen_port, name`
	rows, err := s.pool.Query(ctx, q, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query inbounds: %w", err)
	}
	defer rows.Close()
	return scanInbounds(rows)
}

// ListByProtocol returns every inbound with the given protocol
// across all nodes, sorted by NodeID then ListenPort ascending.
// Used by the admin UI's "show me all VLESS inbounds" view.
func (s *PgStore) ListByProtocol(ctx context.Context, p Protocol) ([]*Inbound, error) {
	const q = baseSelect + `
		WHERE protocol = $1
		ORDER BY node_id, listen_port, name`
	rows, err := s.pool.Query(ctx, q, string(p))
	if err != nil {
		return nil, fmt.Errorf("query inbounds: %w", err)
	}
	defer rows.Close()
	return scanInbounds(rows)
}

// Update replaces the stored copy of i.ID. ErrNotFound if the
// id is unknown; ErrDuplicate on a name or port change that
// would collide with an existing row.
func (s *PgStore) Update(ctx context.Context, i *Inbound) error {
	if i == nil || i.ID == uuid.Nil {
		return fmt.Errorf("update: missing id")
	}
	const q = `
		UPDATE inbounds SET
			name = $2,
			protocol = $3,
			listen = $4,
			listen_port = $5,
			enabled = $6,
			tags = $7,
			params = $8,
			updated_at = NOW()
		WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q,
		i.ID,
		i.Name,
		string(i.Protocol),
		i.Listen,
		i.ListenPort,
		i.Enabled,
		mustMarshal(i.Tags),
		mustMarshalOrNil(i.Params),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf(
				"node %s name %q or port %d: %w",
				i.NodeID, i.Name, i.ListenPort, ErrDuplicate,
			)
		}
		return fmt.Errorf("update inbound: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("id %s: %w", i.ID, ErrNotFound)
	}
	return nil
}

// Delete removes the inbound with the given id. Returns
// ErrNotFound if no such inbound exists. The `node_id` FK has
// ON DELETE CASCADE, so the CASCADE path is owned by the nodes
// store, not by this one.
func (s *PgStore) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM inbounds WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete inbound: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	return nil
}

// --- internal: scan helpers --------------------------------------------

// baseSelect is the SELECT clause used by every read. The order
// matches the column list expected by scanInbound.
const baseSelect = `
	SELECT
		id, node_id, name, protocol, listen, listen_port, enabled,
		tags, params,
		created_at, updated_at
	FROM inbounds`

// scanOne runs a single-row query (the caller supplies the
// WHERE clause and args) and returns the inbound. ErrNotFound
// when the result set is empty.
func (s *PgStore) scanOne(ctx context.Context, where string, args ...any) (*Inbound, error) {
	rows, err := s.pool.Query(ctx, baseSelect+" "+where, args...)
	if err != nil {
		return nil, fmt.Errorf("query inbound: %w", err)
	}
	defer rows.Close()
	out, err := scanInbounds(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w", ErrNotFound)
	}
	return out[0], nil
}

// scanInbounds reads the rows into a slice. An empty result
// returns (nil, nil) — the caller distinguishes "empty" from
// "error" and wraps it in ErrNotFound when applicable.
func scanInbounds(rows pgx.Rows) ([]*Inbound, error) {
	out := make([]*Inbound, 0)
	for rows.Next() {
		var (
			id         uuid.UUID
			nodeID     uuid.UUID
			name       string
			protocol   string
			listen     string
			listenPort int
			enabled    bool
			tagsRaw    []byte
			paramsRaw  []byte
			createdAt  time.Time
			updatedAt  time.Time
		)
		if err := rows.Scan(
			&id, &nodeID, &name, &protocol, &listen, &listenPort, &enabled,
			&tagsRaw, &paramsRaw,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan inbound: %w", err)
		}
		ib := &Inbound{
			ID:         id,
			NodeID:     nodeID,
			Name:       name,
			Protocol:   Protocol(protocol),
			Listen:     listen,
			ListenPort: listenPort,
			Enabled:    enabled,
			CreatedAt:  createdAt,
			UpdatedAt:  updatedAt,
		}
		// Tags: stored as JSONB array; round-trip via the same
		// unmarshalInto helper the hosts store uses.
		if err := unmarshalInto(&ib.Tags, tagsRaw); err != nil {
			return nil, fmt.Errorf("inbound tags: %w", err)
		}
		// Params: stored as JSONB object. NULL in the column
		// round-trips as a nil map; we preserve the distinction
		// so callers can serialise `null` vs `{}` correctly.
		if len(paramsRaw) > 0 {
			if err := json.Unmarshal(paramsRaw, &ib.Params); err != nil {
				return nil, fmt.Errorf("inbound params: %w", err)
			}
		}
		out = append(out, ib)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return out, nil
}

// --- internal: JSONB helpers -------------------------------------------

// mustMarshal JSON-encodes v for a JSONB column. It panics on
// error because the call sites only pass Go types (strings,
// structs, slices) that json.Marshal handles without a runtime
// error.
func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("json.Marshal: %v", err))
	}
	return b
}

// mustMarshalOrNil returns the marshaled bytes for v, or nil
// if v is a typed nil pointer / slice / map / chan / func.
//
// The `v == nil` check alone is not enough for typed nil
// pointers: `var p *Balancer; var v any = p; v == nil` is
// false because v carries the type *Balancer. Without the
// reflect check, a nil `*Balancer` would marshal as JSON
// `null` (4 bytes) and round-trip as a non-nil empty struct,
// which would surprise callers. The reflect branch unboxes
// typed nils to a true nil so the column stores SQL NULL.
//
// The kind list matches the one the govet inline analyzer
// (Go 1.26+) uses for `reflect.Ptr`'s `//go:fix inline`
// replacement. We use `reflect.Pointer` (the canonical name)
// to satisfy that check; `reflect.Ptr` would trip the
// analyzer.
func mustMarshalOrNil(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	if isNilableKind(rv.Kind()) && rv.IsNil() {
		return nil
	}
	return mustMarshal(v)
}

// isNilableKind reports whether values of kind k can be nil.
// The set is what json.Unmarshal treats as "absent when nil":
// pointer, interface, slice, map, chan, func.
func isNilableKind(k reflect.Kind) bool {
	switch k {
	case reflect.Pointer,
		reflect.Interface,
		reflect.Slice,
		reflect.Map,
		reflect.Chan,
		reflect.Func:
		return true
	}
	return false
}

// unmarshalInto decodes raw JSONB bytes into *dst. A nil raw
// (NULL in the DB) leaves the destination as-is.
func unmarshalInto(dst any, raw []byte) error {
	if raw == nil {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

// isUniqueViolation returns true if err is a PostgreSQL
// unique-constraint violation (SQLSTATE 23505). pgx surfaces
// this as a *pgconn.PgError with Code "23505".
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
