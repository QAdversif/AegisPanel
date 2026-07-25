// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package subscription — PostgreSQL-backed implementation
// of Store. As of d-refactor.2 the user-CRUD surface
// has moved to `internal/users` (with its own
// pg_store.go); this PgStore only reads the plan / pool
// / member join tables that the render orchestrator
// walks.
//
// # Shape
//
// Three tables, all read-only from the Store's
// point of view (MemoryStore has With* helpers for
// tests / dev seeding; the Store interface itself only
// has ListPoolsForUser / ListPoolsAll /
// ListPoolMembers):
//
//   - plans (read-only)
//   - host_pools (read-only)
//   - host_pool_members (read-only)
//   - plan_pool (read-only)
//
// # Cross-entity
//
// `users.plan_id` has no FK constraint in migration 0001
// (the relationship is documented but the FK is left
// for a later migration). The Store treats the column
// as a free-floating UUID; the Service's
// `ListPoolsForUser` walks `users.plan_id` → `plan_pool`
// → `host_pools` and is null-safe (a user with no
// plan_id returns an empty list, not an error).
//
// # Concurrency
//
// pgxpool handles connection pooling. The Store is
// safe for concurrent use; each call uses its own
// connection. There is no write path here — the
// subscription package is read-only on the DB side
// after d-refactor.2; mutations live in `internal/users`.
package subscription

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the PostgreSQL-backed Store for
// subscription. As of d-refactor.2 it implements only
// the plan / pool / member read surface; user CRUD
// lives in `internal/users/pg_store.go`.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore wires a PgStore from an open pgxpool.
// The pool is owned by the caller — close it when
// the application shuts down.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// --- reads on plans / pools / members ----------------------------------

// ListPoolsForUser returns every pool that the
// user's plan is associated with via the
// `plan_pool` join table. A user with no `plan_id`
// (NULL) returns an empty list, not an error — this
// matches the MemoryStore behaviour and the Service's
// "no plan = no hosts" semantic.
//
// Phase 0 in the MemoryStore had a documented
// shortcut: "every pool that has at least one member
// is considered attached to every plan". The PgStore
// uses the actual `plan_pool` join — this is the
// production-correct path. The MemoryStore's shortcut
// is dev-only; tests of the Service that depend on
// the dev semantics should use the MemoryStore, not
// the PgStore.
func (s *PgStore) ListPoolsForUser(ctx context.Context, u *User) ([]*Pool, error) {
	if u == nil || u.PlanID == nil {
		return []*Pool{}, nil
	}
	return s.listPoolsBy(ctx, `
		INNER JOIN plan_pool pp ON pp.pool_id = hp.id
		WHERE pp.plan_id = $1
		ORDER BY hp.id`, *u.PlanID)
}

// ListPoolsAll returns every pool in the system,
// sorted by id. Used by the dev seed path in main.go
// (the MemoryStore is preferred for the seed flow;
// this is a convenience for tooling).
func (s *PgStore) ListPoolsAll(ctx context.Context) ([]*Pool, error) {
	return s.listPoolsBy(ctx, `ORDER BY id`)
}

// listPoolsBy is the shared helper for ListPoolsForUser
// and ListPoolsAll. The caller supplies the WHERE /
// ORDER BY suffix; the FROM / SELECT lists are the
// host_pools columns only. The optional arg is the
// plan_id for ListPoolsForUser; pass nil to skip.
func (s *PgStore) listPoolsBy(ctx context.Context, where string, args ...any) ([]*Pool, error) {
	q := poolSelect + " " + where
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query pools: %w", err)
	}
	defer rows.Close()
	return scanPoolRows(rows)
}

// ListPoolMembers returns every member of the
// given pool, ordered by `host_id` ascending. The
// slice is freshly allocated and safe for the caller
// to mutate.
func (s *PgStore) ListPoolMembers(ctx context.Context, poolID uuid.UUID) ([]PoolMember, error) {
	const q = `
		SELECT pool_id, host_id, weight
		FROM host_pool_members
		WHERE pool_id = $1
		ORDER BY host_id`
	rows, err := s.pool.Query(ctx, q, poolID)
	if err != nil {
		return nil, fmt.Errorf("query pool members: %w", err)
	}
	defer rows.Close()
	out := make([]PoolMember, 0)
	for rows.Next() {
		var m PoolMember
		if err := rows.Scan(&m.PoolID, &m.HostID, &m.Weight); err != nil {
			return nil, fmt.Errorf("scan pool member: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return out, nil
}

// --- internal: scan helpers --------------------------------------------

// poolSelect is the SELECT clause used by every
// host_pools read. The order matches scanPoolRow.
// The `hp` alias is mandatory because the
// ListPoolsForUser helper joins against the
// `plan_pool` table using `pp.pool_id = hp.id` — the
// alias is the only stable name that lets both
// statements (the FROM and the JOIN) agree.
const poolSelect = `
	SELECT
		hp.id, hp.name, hp.strategy, hp.antiaffinity,
		hp.created_at, hp.updated_at
	FROM host_pools hp`

// scanPoolRows reads the rows from a host_pools
// query. An empty result returns an empty (non-nil)
// slice so callers can range without a nil check.
// Results are sorted by id (the query's ORDER BY
// guarantees this; the explicit sort here is a
// defensive measure for tests that pass a different
// ORDER BY).
func scanPoolRows(rows pgx.Rows) ([]*Pool, error) {
	out := make([]*Pool, 0)
	for rows.Next() {
		var (
			id           uuid.UUID
			name         string
			strategy     string
			antiaffinity bool
			createdAt    time.Time
			updatedAt    time.Time
		)
		if err := rows.Scan(&id, &name, &strategy, &antiaffinity, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan pool row: %w", err)
		}
		out = append(out, &Pool{
			ID:           id,
			Name:         name,
			Strategy:     PoolStrategy(strategy),
			Antiaffinity: antiaffinity,
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	// Defensive sort. The queries already pass
	// `ORDER BY id`; this is here for the ListPoolsAll
	// path, which is a public read and might be
	// called with a future query that omits the
	// ORDER BY (e.g. an admin UI filter pass).
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out, nil
}
