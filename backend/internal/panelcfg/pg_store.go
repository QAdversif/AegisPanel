// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package panelcfg — PostgreSQL-backed implementation of
// Store. Uses the `panel_path_config` table added in
// migration 0010_panel_path_config.sql.
//
// # Shape
//
// One row per rotation history entry. The "active" row
// is the one with `is_active = TRUE` AND
// (`expires_at IS NULL OR expires_at > now()`). The
// migration enforces a CHECK constraint that pins the
// row id to the sentinel value
// `00000000-0000-0000-0000-000000000001` for the seeded
// default row; subsequent rotations generate fresh
// UUIDs (the only constraint is the UNIQUE on
// `sub_path`).
//
// # Atomicity
//
// `SetActive` and `Reset` are both atomic. The
// "deactivate all currently-active rows" step runs in
// the same transaction as the "insert the new row"
// step, so a crash mid-rotation cannot leave the
// panel with two simultaneously-active rows. The
// `IsActive` predicate is the same as the
// `GetActive` filter, so the two halves of the
// transaction are consistent with the read path.
//
// # Concurrency
//
// pgxpool handles connection pooling. SetActive /
// Reset run in a transaction; GetActive / GetByID
// use a single statement each. The Store is safe for
// concurrent use; each call uses its own connection
// from the pool.
package panelcfg

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the PostgreSQL-backed Store for
// panel-wide configuration. It implements every
// method of the Store interface defined in store.go.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore wires a PgStore from an open pgxpool.
// The pool is owned by the caller — close it when
// the application shuts down.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// GetActive returns the unique active row. The
// "active" predicate is `is_active = TRUE` AND
// (`expires_at IS NULL OR expires_at > now()`). If
// multiple rows match (a stale rotation that was
// never properly deactivated, say), the most
// recently created wins. Returns ErrNotFound if no
// row is active.
//
// The single statement is a single round trip; pgx
// acquires its own connection from the pool.
func (s *PgStore) GetActive(ctx context.Context) (*SubPathConfig, error) {
	const q = `
		SELECT id, sub_path, is_active, created_at, expires_at
		FROM panel_path_config
		WHERE is_active = TRUE
		  AND (expires_at IS NULL OR expires_at > $1)
		ORDER BY created_at DESC
		LIMIT 1`
	now := time.Now().UTC()
	rows, err := s.pool.Query(ctx, q, now)
	if err != nil {
		return nil, fmt.Errorf("query active sub_path: %w", err)
	}
	defer rows.Close()
	return scanSubPathRow(rows)
}

// GetByID returns the row with the given id.
// ErrNotFound if the row does not exist.
func (s *PgStore) GetByID(ctx context.Context, id uuid.UUID) (*SubPathConfig, error) {
	const q = `
		SELECT id, sub_path, is_active, created_at, expires_at
		FROM panel_path_config
		WHERE id = $1`
	rows, err := s.pool.Query(ctx, q, id)
	if err != nil {
		return nil, fmt.Errorf("query sub_path by id: %w", err)
	}
	defer rows.Close()
	return scanSubPathRow(rows)
}

// SetActive rotates the active row. The operation is
// atomic: every currently-active row is marked
// inactive (and given an optional grace expiry),
// then a new row is inserted with `is_active = TRUE`.
// A concurrent SetActive call would see the
// pre-transaction state (the unique constraint on
// `sub_path` would also catch a racing insertion of
// the same path).
//
// The new row's id is a fresh UUID. The sentinel
// `SentinelID` is reserved for the seeded default
// row and is only re-used by Reset.
func (s *PgStore) SetActive(
	ctx context.Context,
	newPath string,
	graceWindow time.Duration,
) (*SubPathConfig, error) {
	if err := ValidatePath(newPath); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	// Rollback is a no-op after a successful Commit;
	// deferred so a panic in any of the below Exec
	// calls also rolls back rather than leaving a
	// half-deactivated set of rows.
	defer func() { _ = tx.Rollback(ctx) }()

	// Deactivate every currently-active row. The grace
	// window sets `expires_at`; without grace the old
	// row stops being "active" the moment we commit.
	// Use `clock_timestamp()` rather than `NOW()` so
	// the two statements in this transaction get
	// distinct timestamps (NOW() returns the
	// transaction start time, which would collapse
	// them).
	//
	// The single placeholder is the grace window as
	// an INTERVAL. A zero/negative duration sets
	// `expires_at = NULL` (immediate cut-over).
	const deactivate = `
		UPDATE panel_path_config
		SET is_active  = FALSE,
		    expires_at = CASE WHEN $1 > INTERVAL '0 seconds'
		                     THEN clock_timestamp() + $1::INTERVAL
		                     ELSE NULL
		                END
		WHERE is_active = TRUE`
	if _, err := tx.Exec(ctx, deactivate, graceWindow); err != nil {
		return nil, fmt.Errorf("deactivate old sub_paths: %w", err)
	}

	// Insert the new active row. The `sub_path`
	// column is NOT unique (migration 0012 dropped
	// the column-level UNIQUE so the operator can
	// re-rotate to a path they used before — each
	// rotation gets a fresh id; the "at most one
	// active row" invariant is held by the
	// SetActive transaction, not by the schema).
	// The RETURNING clause is mandatory: without it
	// `QueryRow` sees no rows and Scan returns
	// `pgx.ErrNoRows`. The caller needs the new id
	// to re-read the row.
	const insert = `
		INSERT INTO panel_path_config (id, sub_path, is_active, created_at, expires_at)
		VALUES (gen_random_uuid(), $1, TRUE, clock_timestamp(), NULL)
		RETURNING id`
	var newID uuid.UUID
	if err := tx.QueryRow(ctx, insert, newPath).Scan(&newID); err != nil {
		return nil, fmt.Errorf("insert new sub_path: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	// Read back the canonical row. The INSERT above
	// generated the id and the timestamp; reading back
	// avoids the caller having to fill those in by
	// hand and gives us a defensive check that the
	// commit actually persisted.
	return s.GetByID(ctx, newID)
}

// Reset deactivates the active row and re-activates
// the default empty sub_path at the sentinel id. The
// default row is identified by `id = SentinelID`;
// we use `INSERT ... ON CONFLICT (id) DO UPDATE` so
// the migration-time seed and a Reset-time
// re-activation both converge on the same row.
func (s *PgStore) Reset(ctx context.Context) (*SubPathConfig, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Deactivate everything currently active.
	if _, err := tx.Exec(ctx, `
		UPDATE panel_path_config
		SET is_active = FALSE, expires_at = NULL
		WHERE is_active = TRUE`); err != nil {
		return nil, fmt.Errorf("deactivate old sub_paths: %w", err)
	}

	// Re-activate the default row at the sentinel id.
	// `ON CONFLICT (id) DO UPDATE` is the idiomatic
	// upsert: if the seed row from migration 0010
	// still exists, we update it; if a previous
	// operator-driven path has somehow corrupted it,
	// we re-create it.
	const upsert = `
		INSERT INTO panel_path_config (id, sub_path, is_active, created_at, expires_at)
		VALUES ($1, $2, TRUE, clock_timestamp(), NULL)
		ON CONFLICT (id) DO UPDATE
		SET sub_path   = EXCLUDED.sub_path,
		    is_active  = TRUE,
		    created_at = clock_timestamp(),
		    expires_at = NULL`
	if _, err := tx.Exec(ctx, upsert, SentinelID, DefaultSubPath); err != nil {
		return nil, fmt.Errorf("upsert default sub_path: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return s.GetByID(ctx, SentinelID)
}

// --- internal: scan helpers --------------------------------------------

// scanSubPathRow reads the columns of a single-row
// query. An empty result returns ErrNotFound; a
// successful read returns a fresh SubPathConfig copy
// (caller may mutate).
//
// The signature accepts pgx.Rows rather than
// *pgxpool.Conn so we use a single helper for both
// the GetActive and GetByID read paths.
func scanSubPathRow(rows pgx.Rows) (*SubPathConfig, error) {
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("rows: %w", err)
		}
		return nil, fmt.Errorf("%w", ErrNotFound)
	}
	var (
		id        uuid.UUID
		subPath   string
		isActive  bool
		createdAt time.Time
		expiresAt *time.Time
	)
	if err := rows.Scan(&id, &subPath, &isActive, &createdAt, &expiresAt); err != nil {
		return nil, fmt.Errorf("scan sub_path row: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return &SubPathConfig{
		ID:        id,
		SubPath:   subPath,
		IsActive:  isActive,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	}, nil
}
