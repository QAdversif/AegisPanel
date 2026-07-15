// SPDX-License-Identifier: AGPL-3.0-or-later

package nodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the PostgreSQL-backed implementation of Store.
// It uses the `nodes` table (migration 0001) and the
// `node_tags` table (also migration 0001). The `capacity_hint`
// column is added in migration 0005 to match the Go model.
//
// # Shape
//
// A Node is one row in `nodes`; each tag is one row in
// `node_tags`. The relationship is 1:N with ON DELETE CASCADE
// on node_tags.node_id, so a Delete on the node row removes
// the tag rows in the same transaction.
//
// # Reads
//
// GetByID, GetByName, and List use a single LEFT JOIN query
// that returns the node row + every tag in one round trip.
// The Go side groups by node_id. This is N+1-safe: 100 nodes
// with 3 tags each = 1 query.
//
// # Writes
//
// Create and Update run in a transaction. Create inserts the
// node row first, then the tag rows. Update uses the canonical
// "delete children + insert" pattern for the tags so the tag
// set on the node is exactly what the service validated.
//
// # Concurrency
//
// pgxpool handles connection pooling. The Store is safe for
// concurrent use; each call uses its own connection.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore wires a PgStore from an open pgxpool. The pool
// is owned by the caller — close it when the application
// shuts down.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// Create inserts a node + its tags in a single transaction.
// ErrDuplicate is returned if a node with the same Name
// already exists (the `name` column is unique per the schema
// in migration 0001).
func (s *PgStore) Create(ctx context.Context, n *Node) error {
	if n == nil {
		return fmt.Errorf("create: nil node")
	}
	if n.ID == uuid.Nil {
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

	if err := insertNode(ctx, tx, n); err != nil {
		return err
	}
	for _, tag := range n.Tags {
		if err := insertNodeTag(ctx, tx, n.ID, tag); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	// Re-fetch to pick up the timestamps the DB assigned.
	stored, err := s.GetByID(ctx, n.ID)
	if err != nil {
		return err
	}
	n.CreatedAt = stored.CreatedAt
	n.UpdatedAt = stored.UpdatedAt
	return nil
}

// GetByID returns the node with the given id, or ErrNotFound.
// The node's Tags are populated from the node_tags table in
// a single JOIN query.
func (s *PgStore) GetByID(ctx context.Context, id uuid.UUID) (*Node, error) {
	const q = nodeWithTagsSelect + `
		WHERE n.id = $1
		ORDER BY t.tag`
	rows, err := s.pool.Query(ctx, q, id)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	nodes, err := scanNodesWithTags(rows)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("id %s: %w", id, ErrNotFound)
	}
	return nodes[0], nil
}

// GetByName returns the node with the given name, or
// ErrNotFound. The store index is on the raw name.
func (s *PgStore) GetByName(ctx context.Context, name string) (*Node, error) {
	const q = nodeWithTagsSelect + `
		WHERE n.name = $1
		ORDER BY t.tag`
	rows, err := s.pool.Query(ctx, q, name)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	nodes, err := scanNodesWithTags(rows)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("name %q: %w", name, ErrNotFound)
	}
	return nodes[0], nil
}

// List returns every node, sorted by CreatedAt ascending. Tags
// are populated alongside each node in a single JOIN query.
func (s *PgStore) List(ctx context.Context) ([]*Node, error) {
	const q = nodeWithTagsSelect + `
		ORDER BY n.created_at, t.tag`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	nodes, err := scanNodesWithTags(rows)
	if err != nil {
		return nil, err
	}
	if nodes == nil {
		return []*Node{}, nil
	}
	return nodes, nil
}

// Update replaces the stored copy of n.ID. The host row is
// updated; the tag set is replaced atomically (delete +
// insert) so the persisted state is exactly what the service
// validated. Returns ErrNotFound if the node does not exist;
// ErrDuplicate on a name rename that would collide with
// another node.
func (s *PgStore) Update(ctx context.Context, n *Node) error {
	if n == nil || n.ID == uuid.Nil {
		return fmt.Errorf("update: missing id")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The node row first. The unique index on name surfaces
	// as a duplicate error we map to ErrDuplicate.
	const updateNode = `
		UPDATE nodes SET
			name = $2,
			region = $3,
			state = $4,
			address = $5,
			capacity_hint = $6,
			updated_at = NOW()
		WHERE id = $1`
	tag, err := tx.Exec(ctx, updateNode,
		n.ID,
		n.Name,
		n.Region,
		string(n.State),
		n.Address,
		n.CapacityHint,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("name %q: %w", n.Name, ErrDuplicate)
		}
		return fmt.Errorf("update node: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("id %s: %w", n.ID, ErrNotFound)
	}

	// Replace the tag set. DELETE is unconditional (CASCADE
	// on the node_id FK is not used because node_tags would
	// have been CASCADE-deleted with the node anyway, and we
	// want Update to be idempotent against an empty tag set).
	if _, err := tx.Exec(ctx, `DELETE FROM node_tags WHERE node_id = $1`, n.ID); err != nil {
		return fmt.Errorf("delete old tags: %w", err)
	}
	for _, t := range n.Tags {
		if err := insertNodeTag(ctx, tx, n.ID, t); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	// Re-fetch to pick up the new updated_at.
	stored, err := s.GetByID(ctx, n.ID)
	if err != nil {
		return err
	}
	n.CreatedAt = stored.CreatedAt
	n.UpdatedAt = stored.UpdatedAt
	return nil
}

// Delete removes the node with the given id. The node_tags
// rows are removed by ON DELETE CASCADE in migration 0001.
// Returns ErrNotFound if the node does not exist.
func (s *PgStore) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM nodes WHERE id = $1`
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

// insertNode writes the node row. ON CONFLICT (name) surfaces
// a duplicate-name as a 23505 SQLSTATE we map to ErrDuplicate.
func insertNode(ctx context.Context, tx pgx.Tx, n *Node) error {
	const q = `
		INSERT INTO nodes (
			id, name, region, state, address, capacity_hint
		) VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := tx.Exec(ctx, q,
		n.ID,
		n.Name,
		n.Region,
		string(n.State),
		n.Address,
		n.CapacityHint,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("name %q: %w", n.Name, ErrDuplicate)
		}
		return fmt.Errorf("insert node: %w", err)
	}
	return nil
}

// insertNodeTag writes a single (node, tag) row. The pair is
// the table's primary key, so a duplicate is a 23505.
func insertNodeTag(ctx context.Context, tx pgx.Tx, nodeID uuid.UUID, tag string) error {
	const q = `INSERT INTO node_tags (node_id, tag) VALUES ($1, $2)`
	_, err := tx.Exec(ctx, q, nodeID, tag)
	if err != nil {
		if isUniqueViolation(err) {
			// Tag already present (Update path can hit this
			// if the service feeds a duplicate). Treat as a
			// no-op rather than an error so the caller's
			// Create / Update can stay simple.
			return nil
		}
		return fmt.Errorf("insert node tag: %w", err)
	}
	return nil
}

// --- internal: scan helpers --------------------------------------------

// nodeWithTagsSelect is the SELECT clause used by every read.
// The order matches the column list expected by scanNodeRow.
//
// The `t.tag` column is NULL for the LEFT-JOIN row of a
// node with no tags; scanNodeRow handles that case.
const nodeWithTagsSelect = `
	SELECT
		n.id, n.name, n.region, n.state, n.address, n.capacity_hint,
		n.created_at, n.updated_at,
		t.tag
	FROM nodes n
	LEFT JOIN node_tags t ON t.node_id = n.id`

// scanNodesWithTags reads the rows from a JOIN query and
// groups them by node id. A single node row that has no tags
// yields one row with NULL tag column; the returned Node has
// Tags = nil in that case.
//
// Returns nil when the result set is empty.
func scanNodesWithTags(rows pgx.Rows) ([]*Node, error) {
	type key struct{ id uuid.UUID }
	byID := make(map[key]*Node)
	order := []key{}
	for rows.Next() {
		var (
			nID           uuid.UUID
			nName         string
			nRegion       string
			nState        string
			nAddress      string
			nCapacityHint string
			nCreatedAt    time.Time
			nUpdatedAt    time.Time
			tTag          *string
		)
		if err := rows.Scan(
			&nID, &nName, &nRegion, &nState, &nAddress, &nCapacityHint,
			&nCreatedAt, &nUpdatedAt,
			&tTag,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		k := key{id: nID}
		node, ok := byID[k]
		if !ok {
			node = &Node{
				ID:           nID,
				Name:         nName,
				Region:       nRegion,
				State:        State(nState),
				Address:      nAddress,
				CapacityHint: nCapacityHint,
				CreatedAt:    nCreatedAt,
				UpdatedAt:    nUpdatedAt,
			}
			byID[k] = node
			order = append(order, k)
		}

		// Tag column: skip if NULL (the LEFT JOIN sentinel
		// for a node with no tags).
		if tTag == nil {
			continue
		}
		node.Tags = append(node.Tags, *tTag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	if len(order) == 0 {
		return nil, nil
	}
	out := make([]*Node, 0, len(order))
	for _, k := range order {
		out = append(out, byID[k])
	}
	return out, nil
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
