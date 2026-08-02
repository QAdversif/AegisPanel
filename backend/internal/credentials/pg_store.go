// SPDX-License-Identifier: AGPL-3.0-or-later
//
// PgStore is the PostgreSQL-backed implementation
// of Store. It owns the `user_inbound_credentials`
// table (migration 0019).
//
// # Shape
//
// One row per (user_id, inbound_id) credential. The
// (user_id, inbound_id) UNIQUE constraint maps to
// ErrDuplicate (Postgres SQLSTATE 23505). Both FKs
// use ON DELETE CASCADE so a user removal or an
// inbound removal drops the credential row
// alongside it.
//
// # credential_value shape
//
// The `credential_value` column is TEXT. The panel
// stores whatever the admin provides; the
// sing-box renderer is authoritative for
// per-protocol shape validation (UUID format for
// VLESS, password length for HY2 / Trojan, etc.).
// See the package docstring for the design
// rationale.
//
// # Concurrency
//
// pgxpool handles connection pooling. Each call
// uses its own connection from the pool. Insert /
// Update / Delete are single-statement operations,
// so no explicit transaction is needed.

package credentials

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the PostgreSQL-backed Store.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore wires a PgStore from an open pgxpool.
// The pool is owned by the caller — close it
// when the application shuts down.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// Insert appends a new credential. Returns
// ErrDuplicate on a UNIQUE (user_id, inbound_id)
// violation (Postgres SQLSTATE 23505).
//
// The id is generated server-side via the
// `gen_random_uuid()` default on the `id` column
// (migration 0019). The created_at / updated_at
// fields are filled by the DB-side `DEFAULT NOW()`
// in the same migration.
//
// The pgx RETURNING clause reads the server-
// generated id + timestamps back so the Service
// gets the persisted copy (the input is treated
// as read-only by convention).
func (s *PgStore) Insert(ctx context.Context, c Credential) (*Credential, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO user_inbound_credentials (user_id, inbound_id, credential_value)
		VALUES ($1, $2, $3)
		RETURNING id, user_id, inbound_id, credential_value, created_at, updated_at
	`, c.UserID, c.InboundID, c.CredentialValue)
	var out Credential
	if err := row.Scan(
		&out.ID, &out.UserID, &out.InboundID,
		&out.CredentialValue, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("credentials: insert: %w", err)
	}
	return &out, nil
}

// Update changes the credential_value of an
// existing row, re-stamping updated_at via the
// DB-side `ON UPDATE NOW()` trigger from migration
// 0001 (carried over). Returns ErrNotFound if
// the id does not exist (Postgres SQLSTATE 02000
// from `rows.Scan` on a zero-row result).
func (s *PgStore) Update(ctx context.Context, id uuid.UUID, newValue string) (*Credential, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE user_inbound_credentials
		SET credential_value = $2
		WHERE id = $1
		RETURNING id, user_id, inbound_id, credential_value, created_at, updated_at
	`, id, newValue)
	var out Credential
	if err := row.Scan(
		&out.ID, &out.UserID, &out.InboundID,
		&out.CredentialValue, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("credentials: update: %w", err)
	}
	return &out, nil
}

// Delete removes a row by id. Returns ErrNotFound
// if the id does not exist.
func (s *PgStore) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM user_inbound_credentials WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("credentials: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetByID returns the full row. Returns ErrNotFound
// if the id does not exist.
func (s *PgStore) GetByID(ctx context.Context, id uuid.UUID) (*Credential, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, user_id, inbound_id, credential_value, created_at, updated_at
		FROM user_inbound_credentials
		WHERE id = $1
	`, id)
	var out Credential
	if err := row.Scan(
		&out.ID, &out.UserID, &out.InboundID,
		&out.CredentialValue, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("credentials: get: %w", err)
	}
	return &out, nil
}

// ListByUser returns every credential for the
// user, ordered by inbound_id ascending (stable,
// deterministic, friendly to the subscription
// resolver that calls this with a known user_id).
func (s *PgStore) ListByUser(ctx context.Context, userID uuid.UUID) ([]*Credential, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, inbound_id, credential_value, created_at, updated_at
		FROM user_inbound_credentials
		WHERE user_id = $1
		ORDER BY inbound_id ASC, id ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("credentials: list by user: %w", err)
	}
	defer rows.Close()
	out := make([]*Credential, 0)
	for rows.Next() {
		var c Credential
		if err := rows.Scan(
			&c.ID, &c.UserID, &c.InboundID,
			&c.CredentialValue, &c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("credentials: scan: %w", err)
		}
		out = append(out, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("credentials: list by user iterate: %w", err)
	}
	return out, nil
}

// ListByInbound returns every credential for the
// inbound, ordered by user_id ascending (the
// multi-user renderer's primary access pattern).
func (s *PgStore) ListByInbound(ctx context.Context, inboundID uuid.UUID) ([]*Credential, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, inbound_id, credential_value, created_at, updated_at
		FROM user_inbound_credentials
		WHERE inbound_id = $1
		ORDER BY user_id ASC, id ASC
	`, inboundID)
	if err != nil {
		return nil, fmt.Errorf("credentials: list by inbound: %w", err)
	}
	defer rows.Close()
	out := make([]*Credential, 0)
	for rows.Next() {
		var c Credential
		if err := rows.Scan(
			&c.ID, &c.UserID, &c.InboundID,
			&c.CredentialValue, &c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("credentials: scan: %w", err)
		}
		out = append(out, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("credentials: list by inbound iterate: %w", err)
	}
	return out, nil
}
