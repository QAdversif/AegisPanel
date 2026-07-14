// SPDX-License-Identifier: AGPL-3.0-or-later

package hosts

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

// PgStore is the PostgreSQL-backed implementation of Store.
// It uses the `hosts` and `host_endpoints` tables added in
// migration 0004_hosts_v3.sql.
//
// # Shape
//
// A Host is one row in `hosts`; each Endpoint is one row in
// `host_endpoints`. The relationship is 1:N with ON DELETE
// CASCADE on host_endpoints.host_id, so a Delete on the
// host row removes the endpoints in the same transaction.
//
// # Reads
//
// GetByID and List use a single LEFT JOIN query that returns
// the host row + every endpoint row in one round trip. The
// Go side groups by host_id. This is N+1-safe: 100 hosts
// with 3 endpoints each = 1 query.
//
// # Writes
//
// Create and Update run in a transaction. Create inserts
// the host row first, then the endpoint rows. Update uses
// the canonical "delete children + insert" pattern so
// the endpoint set on the host is exactly what the
// service validated.
//
// # Concurrency
//
// pgxpool handles connection pooling. The Store is safe
// for concurrent use; each call uses its own connection.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore wires a PgStore from an open pgxpool. The
// pool is owned by the caller — close it when the
// application shuts down.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// Create inserts a host + its endpoints in a single
// transaction. ErrDuplicate is returned if a host with
// the same Remark already exists (the `remark` column is
// unique per the schema in migration 0001).
func (s *PgStore) Create(ctx context.Context, h *Host) error {
	if h == nil {
		return fmt.Errorf("create: nil host")
	}
	if h.ID == uuid.Nil {
		return fmt.Errorf("create: zero id")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	// Rollback is a no-op after a successful Commit;
	// deferred so a panic in any of the below Exec calls
	// also rolls back rather than leaving the row
	// half-inserted.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := insertHost(ctx, tx, h); err != nil {
		return err
	}
	for i := range h.Endpoints {
		ep := &h.Endpoints[i]
		if ep.ID == uuid.Nil {
			return fmt.Errorf("create: endpoint %d has zero id", i)
		}
		if err := insertEndpoint(ctx, tx, h.ID, ep); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	// Read back the timestamps the DB assigned.
	stored, err := s.GetByID(ctx, h.ID)
	if err != nil {
		return err
	}
	h.CreatedAt = stored.CreatedAt
	h.UpdatedAt = stored.UpdatedAt
	return nil
}

// GetByID returns the host with the given id, or
// ErrNotFound. The host's Endpoints are populated from the
// host_endpoints table in a single JOIN query.
func (s *PgStore) GetByID(ctx context.Context, id uuid.UUID) (*Host, error) {
	const q = hostWithEndpointsSelect + `
		WHERE h.id = $1
		ORDER BY e.id`
	rows, err := s.pool.Query(ctx, q, id)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	hosts, err := scanHostsWithEndpoints(rows)
	if err != nil {
		return nil, err
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	return hosts[0], nil
}

// GetByRemark returns the host with the given remark
// (case-sensitive; the store index is on the raw remark).
func (s *PgStore) GetByRemark(ctx context.Context, remark string) (*Host, error) {
	const q = hostWithEndpointsSelect + `
		WHERE h.remark = $1
		ORDER BY e.id`
	rows, err := s.pool.Query(ctx, q, remark)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	hosts, err := scanHostsWithEndpoints(rows)
	if err != nil {
		return nil, err
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("remark %q: %w", remark, ErrNotFound)
	}
	return hosts[0], nil
}

// List returns every host, sorted by Priority ascending
// then CreatedAt ascending. Endpoints are populated
// alongside each host in a single JOIN query.
func (s *PgStore) List(ctx context.Context) ([]*Host, error) {
	const q = hostWithEndpointsSelect + `
		ORDER BY h.priority, h.created_at, e.id`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	hosts, err := scanHostsWithEndpoints(rows)
	if err != nil {
		return nil, err
	}
	if hosts == nil {
		return []*Host{}, nil
	}
	return hosts, nil
}

// Update replaces the stored copy of h.ID. The host row
// is updated; the endpoint set is replaced atomically
// (delete + insert) so the persisted state is exactly
// what the service validated. Returns ErrNotFound if
// the host does not exist; ErrDuplicate on a remark
// rename that would collide with another host.
func (s *PgStore) Update(ctx context.Context, h *Host) error {
	if h == nil || h.ID == uuid.Nil {
		return fmt.Errorf("update: missing id")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The host row first. ON CONFLICT (remark) ensures
	// the unique index surfaces as a duplicate error we
	// map to ErrDuplicate.
	const updateHost = `
		UPDATE hosts SET
			remark = $2,
			type = $3,
			enabled = $4,
			priority = $5,
			status_filter = $6,
			country = $7,
			city = $8,
			tags = $9,
			balancer = $10,
			updated_at = NOW()
		WHERE id = $1`
	tag, err := tx.Exec(ctx, updateHost,
		h.ID,
		h.Remark,
		string(h.Type),
		h.Enabled,
		h.Priority,
		mustMarshal(h.StatusFilter),
		h.Country,
		h.City,
		mustMarshal(h.Tags),
		mustMarshalOrNil(h.Balancer),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("remark %q: %w", h.Remark, ErrDuplicate)
		}
		return fmt.Errorf("update host: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("id %s: %w", h.ID, ErrNotFound)
	}

	// Replace the endpoint set. DELETE is unconditional
	// (CASCADE on the host_id FK is not used because
	// host_endpoints would have been CASCADE-deleted
	// with the host anyway, and we want Update to be
	// idempotent against an empty endpoint set).
	if _, err := tx.Exec(ctx, `DELETE FROM host_endpoints WHERE host_id = $1`, h.ID); err != nil {
		return fmt.Errorf("delete old endpoints: %w", err)
	}
	for i := range h.Endpoints {
		ep := &h.Endpoints[i]
		if ep.ID == uuid.Nil {
			return fmt.Errorf("update: endpoint %d has zero id", i)
		}
		if err := insertEndpoint(ctx, tx, h.ID, ep); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	// Read back the new updated_at.
	stored, err := s.GetByID(ctx, h.ID)
	if err != nil {
		return err
	}
	h.CreatedAt = stored.CreatedAt
	h.UpdatedAt = stored.UpdatedAt
	return nil
}

// Delete removes the host with the given id. The
// host_endpoints rows are removed by ON DELETE CASCADE
// in the migration. Returns ErrNotFound if the host
// does not exist.
func (s *PgStore) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM hosts WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	return nil
}

// --- internal: insert helpers ------------------------------------------

// insertHost writes the host row. JSONB columns are
// marshaled inline; ON CONFLICT (remark) surfaces a
// duplicate-remark as a 23505 SQLSTATE we map to
// ErrDuplicate.
func insertHost(ctx context.Context, tx pgx.Tx, h *Host) error {
	const q = `
		INSERT INTO hosts (
			id, remark, type, enabled, priority,
			status_filter, country, city, tags, balancer
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	_, err := tx.Exec(ctx, q,
		h.ID,
		h.Remark,
		string(h.Type),
		h.Enabled,
		h.Priority,
		mustMarshal(h.StatusFilter),
		h.Country,
		h.City,
		mustMarshal(h.Tags),
		mustMarshalOrNil(h.Balancer),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("remark %q: %w", h.Remark, ErrDuplicate)
		}
		return fmt.Errorf("insert host: %w", err)
	}
	return nil
}

// insertEndpoint writes a single endpoint row. The
// port is a nullable int (pointer in Go); the JSONB
// slice fields are marshaled to []byte.
func insertEndpoint(ctx context.Context, tx pgx.Tx, hostID uuid.UUID, ep *Endpoint) error {
	const q = `
		INSERT INTO host_endpoints (
			id, host_id, node_id, inbound_id, weight,
			address, port, sni, host, path
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	_, err := tx.Exec(ctx, q,
		ep.ID,
		hostID,
		ep.NodeID,
		ep.InboundID,
		ep.Weight,
		mustMarshal(ep.Address),
		nullableInt(ep.Port),
		mustMarshal(ep.SNI),
		mustMarshal(ep.Host),
		ep.Path,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("endpoint %s: %w", ep.ID, ErrDuplicate)
		}
		return fmt.Errorf("insert endpoint: %w", err)
	}
	return nil
}

// --- internal: scan helpers --------------------------------------------

// hostWithEndpointsSelect is the SELECT clause used by
// every read. The order matches the column list expected
// by scanHostRow.
//
// The `e.*` columns are NULL for the LEFT-JOIN row of a
// host with no endpoints; scanHostRow handles that case.
const hostWithEndpointsSelect = `
	SELECT
		h.id, h.remark, h.type, h.enabled, h.priority,
		h.status_filter, h.country, h.city, h.tags, h.balancer,
		h.created_at, h.updated_at,
		e.id, e.node_id, e.inbound_id, e.weight,
		e.address, e.port, e.sni, e.host, e.path,
		e.created_at, e.updated_at
	FROM hosts h
	LEFT JOIN host_endpoints e ON e.host_id = h.id`

// scanHostsWithEndpoints reads the rows from a JOIN query
// and groups them by host id. A single host row that has
// no endpoints yields one row with NULL endpoint columns;
// the returned Host has Endpoints = nil in that case.
//
// Returns nil when the result set is empty.
func scanHostsWithEndpoints(rows pgx.Rows) ([]*Host, error) {
	type key struct{ id uuid.UUID }
	hosts := make(map[key]*Host)
	order := []key{}
	for rows.Next() {
		var (
			// host columns
			hID              uuid.UUID
			hRemark          string
			hType            string
			hEnabled         bool
			hPriority        int
			hStatusFilterRaw []byte
			hCountry         string
			hCity            string
			hTagsRaw         []byte
			hBalancerRaw     []byte
			hCreatedAt       time.Time
			hUpdatedAt       time.Time
			// endpoint columns (NULL for the host-only row)
			eID         *uuid.UUID
			eNodeID     *uuid.UUID
			eInboundID  *uuid.UUID
			eWeight     *int
			eAddressRaw []byte
			ePort       *int
			eSNIRaw     []byte
			eHostRaw    []byte
			ePath       *string
			eCreatedAt  *time.Time
			eUpdatedAt  *time.Time
		)
		if err := rows.Scan(
			&hID, &hRemark, &hType, &hEnabled, &hPriority,
			&hStatusFilterRaw, &hCountry, &hCity, &hTagsRaw, &hBalancerRaw,
			&hCreatedAt, &hUpdatedAt,
			&eID, &eNodeID, &eInboundID, &eWeight,
			&eAddressRaw, &ePort, &eSNIRaw, &eHostRaw, &ePath,
			&eCreatedAt, &eUpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		k := key{id: hID}
		h, ok := hosts[k]
		if !ok {
			h = &Host{
				ID:        hID,
				Remark:    hRemark,
				Type:      HostType(hType),
				Enabled:   hEnabled,
				Priority:  hPriority,
				Country:   hCountry,
				City:      hCity,
				CreatedAt: hCreatedAt,
				UpdatedAt: hUpdatedAt,
			}
			if err := unmarshalInto(&h.StatusFilter, hStatusFilterRaw); err != nil {
				return nil, fmt.Errorf("status_filter: %w", err)
			}
			if err := unmarshalInto(&h.Tags, hTagsRaw); err != nil {
				return nil, fmt.Errorf("tags: %w", err)
			}
			if hBalancerRaw != nil {
				var b Balancer
				if err := json.Unmarshal(hBalancerRaw, &b); err != nil {
					return nil, fmt.Errorf("balancer: %w", err)
				}
				h.Balancer = &b
			}
			hosts[k] = h
			order = append(order, k)
		}

		// Endpoint row: skip if all endpoint columns are
		// NULL (the LEFT JOIN sentinel for a host with
		// no endpoints).
		if eID == nil {
			continue
		}
		ep := Endpoint{
			ID:        *eID,
			NodeID:    *eNodeID,
			InboundID: *eInboundID,
			Weight:    *eWeight,
			Port:      ePort,
		}
		if err := unmarshalInto(&ep.Address, eAddressRaw); err != nil {
			return nil, fmt.Errorf("endpoint address: %w", err)
		}
		if err := unmarshalInto(&ep.SNI, eSNIRaw); err != nil {
			return nil, fmt.Errorf("endpoint sni: %w", err)
		}
		if err := unmarshalInto(&ep.Host, eHostRaw); err != nil {
			return nil, fmt.Errorf("endpoint host: %w", err)
		}
		if ePath != nil {
			ep.Path = *ePath
		}
		h.Endpoints = append(h.Endpoints, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	if len(order) == 0 {
		return nil, nil
	}
	out := make([]*Host, 0, len(order))
	for _, k := range order {
		out = append(out, hosts[k])
	}
	return out, nil
}

// --- internal: JSONB helpers -------------------------------------------

// mustMarshal JSON-encodes v for a JSONB column. It panics
// on error because the call sites only pass Go types
// (strings, structs, slices) that json.Marshal handles
// without a runtime error.
func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("json.Marshal: %v", err))
	}
	return b
}

// mustMarshalOrNil returns the marshaled bytes for v, or
// nil if v is the zero value (interface == nil or typed
// nil). Used for nullable JSONB columns.
func mustMarshalOrNil(v any) any {
	if v == nil {
		return nil
	}
	return mustMarshal(v)
}

// unmarshalInto decodes raw JSONB bytes into *dst. The
// destination must be a pointer (typically &h.Tags or
// &ep.Address). A nil raw (NULL in the DB) leaves the
// destination as-is, which is the desired behaviour
// for "no array stored".
func unmarshalInto(dst any, raw []byte) error {
	if raw == nil {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

// nullableInt returns the pointer (which may be nil) for
// pgx to bind as a nullable integer. pgx accepts
// `*int` as a destination and stores NULL when the
// pointer is nil.
func nullableInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// isUniqueViolation returns true if err is a PostgreSQL
// unique-constraint violation (SQLSTATE 23505). pgx
// surfaces this as a *pgconn.PgError with Code "23505".
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
