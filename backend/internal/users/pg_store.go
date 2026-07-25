// SPDX-License-Identifier: AGPL-3.0-or-later

package users

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
// Store. It uses the `users` table (migration
// 0001_initial.sql + 0011 sub-token rotation). The
// hosts_allowlist / hosts_blocklist JSONB arrays
// mirror the Go slice fields one-to-one.
//
// # Shape
//
// One row in `users` per User. The username +
// sub_token UNIQUE constraints map to ErrDuplicate
// (Postgres SQLSTATE 23505). The status CHECK
// constraint matches the Go AllowedStatuses set; the
// Service is the authoritative gate. The
// sub_token_prev partial UNIQUE index (migration
// 0011) is enforced by the database.
//
// # Concurrency
//
// pgxpool handles connection pooling. Each call
// uses its own connection from the pool. Create /
// Update / Delete are single-statement operations
// (no children to atomically insert), so no
// explicit transaction is needed.
//
// # Time handling
//
// All timestamps are TIMESTAMPTZ in UTC. pgx
// transparently converts to / from time.Time via
// its built-in scanner / encoder. The Go side
// keeps them in time.Time and never treats them as
// local.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore wires a PgStore from an open pgxpool.
// The pool is owned by the caller — close it when
// the application shuts down.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// Create inserts a new user row. Returns ErrDuplicate
// on (username) or (sub_token) collisions (Postgres
// SQLSTATE 23505 → unique_violation).
func (s *PgStore) Create(ctx context.Context, u *User) error {
	if u == nil {
		return fmt.Errorf("create: nil user")
	}
	if u.ID == uuid.Nil {
		return fmt.Errorf("create: zero id")
	}
	if !u.IsValid() {
		return fmt.Errorf("create: invalid user")
	}
	const q = `
		INSERT INTO users (
			id, external_id, username, status, plan_id, expire_at,
			traffic_limit_bytes, traffic_used_bytes, last_reset_at,
			device_limit, hosts_allowlist, hosts_blocklist,
			sub_token, sub_token_rotated_at,
			sub_token_prev, sub_token_prev_expires_at,
			telegram_id, email
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9,
			$10, $11, $12,
			$13, $14,
			$15, $16,
			$17, $18
		)`
	allowlist, err := json.Marshal(u.HostsAllowlist)
	if err != nil {
		return fmt.Errorf("create: marshal hosts_allowlist: %w", err)
	}
	blocklist, err := json.Marshal(u.HostsBlocklist)
	if err != nil {
		return fmt.Errorf("create: marshal hosts_blocklist: %w", err)
	}
	_, err = s.pool.Exec(ctx, q,
		u.ID,
		nullableString(u.ExternalID),
		u.Username,
		string(u.Status),
		u.PlanID,
		u.ExpireAt,
		u.TrafficLimitBytes,
		u.TrafficUsedBytes,
		u.LastResetAt,
		u.DeviceLimit,
		allowlist,
		blocklist,
		u.SubToken,
		u.SubTokenRotatedAt,
		nullableString(u.SubTokenPrev),
		u.SubTokenPrevExpiresAt,
		u.TelegramID,
		nullableString(u.Email),
	)
	if err != nil {
		return mapPgError(err, "create")
	}
	return nil
}

// GetByID returns the user with the given id, or
// ErrNotFound.
func (s *PgStore) GetByID(ctx context.Context, id uuid.UUID) (*User, error) {
	const q = baseSelect + ` WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: id %s", ErrNotFound, id)
		}
		return nil, err
	}
	return u, nil
}

// GetByUsername returns the user with the given
// username, or ErrNotFound.
func (s *PgStore) GetByUsername(ctx context.Context, username string) (*User, error) {
	const q = baseSelect + ` WHERE username = $1`
	row := s.pool.QueryRow(ctx, q, username)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: username %q", ErrNotFound, username)
		}
		return nil, err
	}
	return u, nil
}

// GetBySubToken looks up the user by sub_token. The
// usePrev flag controls whether the prev-token chain
// is consulted (see migration 0011 and Store
// interface comment).
//
// Lookup order:
//  1. current sub_token (always).
//  2. If usePrev is true, sub_token_prev — but only
//     when sub_token_prev is NOT NULL and
//     sub_token_prev_expires_at > NOW().
func (s *PgStore) GetBySubToken(ctx context.Context, token string, usePrev bool) (*User, error) {
	if token == "" {
		return nil, fmt.Errorf("%w: empty token", ErrNotFound)
	}
	// Try the current token first. A short query
	// against the (sub_token) UNIQUE index.
	const qCurrent = baseSelect + ` WHERE sub_token = $1`
	row := s.pool.QueryRow(ctx, qCurrent, token)
	u, err := scanUser(row)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if !usePrev {
		return nil, fmt.Errorf("%w: token", ErrNotFound)
	}
	// Fall through to the prev-token chain.
	const qPrev = baseSelect + `
		WHERE sub_token_prev = $1
		  AND sub_token_prev IS NOT NULL
		  AND sub_token_prev_expires_at > NOW()`
	row = s.pool.QueryRow(ctx, qPrev, token)
	u, err = scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: token (no current or active prev)", ErrNotFound)
		}
		return nil, err
	}
	return u, nil
}

// List returns every user, sorted by CreatedAt asc.
func (s *PgStore) List(ctx context.Context) ([]*User, error) {
	const q = baseSelect + ` ORDER BY created_at ASC`
	return s.listQuery(ctx, q)
}

// ListByStatus returns every user with the given
// status, sorted by CreatedAt asc.
func (s *PgStore) ListByStatus(ctx context.Context, status Status) ([]*User, error) {
	const q = baseSelect + ` WHERE status = $1 ORDER BY created_at ASC`
	return s.listQuery(ctx, q, string(status))
}

// Update replaces the stored copy of u.ID. Returns
// ErrNotFound if no such user exists; ErrDuplicate
// if the rename would collide.
func (s *PgStore) Update(ctx context.Context, u *User) error {
	if u == nil {
		return fmt.Errorf("update: nil user")
	}
	if u.ID == uuid.Nil {
		return fmt.Errorf("update: zero id")
	}
	if !u.IsValid() {
		return fmt.Errorf("update: invalid user")
	}
	allowlist, err := json.Marshal(u.HostsAllowlist)
	if err != nil {
		return fmt.Errorf("update: marshal hosts_allowlist: %w", err)
	}
	blocklist, err := json.Marshal(u.HostsBlocklist)
	if err != nil {
		return fmt.Errorf("update: marshal hosts_blocklist: %w", err)
	}
	const q = `
		UPDATE users SET
			external_id = $2,
			username = $3,
			status = $4,
			plan_id = $5,
			expire_at = $6,
			traffic_limit_bytes = $7,
			traffic_used_bytes = $8,
			last_reset_at = $9,
			device_limit = $10,
			hosts_allowlist = $11,
			hosts_blocklist = $12,
			sub_token = $13,
			sub_token_rotated_at = $14,
			sub_token_prev = $15,
			sub_token_prev_expires_at = $16,
			telegram_id = $17,
			email = $18,
			updated_at = NOW()
		WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q,
		u.ID,
		nullableString(u.ExternalID),
		u.Username,
		string(u.Status),
		u.PlanID,
		u.ExpireAt,
		u.TrafficLimitBytes,
		u.TrafficUsedBytes,
		u.LastResetAt,
		u.DeviceLimit,
		allowlist,
		blocklist,
		u.SubToken,
		u.SubTokenRotatedAt,
		nullableString(u.SubTokenPrev),
		u.SubTokenPrevExpiresAt,
		u.TelegramID,
		nullableString(u.Email),
	)
	if err != nil {
		return mapPgError(err, "update")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: id %s", ErrNotFound, u.ID)
	}
	return nil
}

// Delete removes the user with the given id. Returns
// ErrNotFound if no such user exists.
func (s *PgStore) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return mapPgError(err, "delete")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: id %s", ErrNotFound, id)
	}
	return nil
}

// listQuery is the shared List / ListByStatus body.
// Centralised so the column scan logic is in one
// place.
func (s *PgStore) listQuery(ctx context.Context, q string, args ...any) ([]*User, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list: query: %w", err)
	}
	defer rows.Close()
	out := make([]*User, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("list: scan: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list: rows: %w", err)
	}
	return out, nil
}

// baseSelect is the column list shared by all
// single-row reads. Kept in lockstep with the
// `users` table schema — any new column must be
// added here AND to the User struct AND to the
// Create / Update queries.
const baseSelect = `
	SELECT
		id, external_id, username, status, plan_id, expire_at,
		traffic_limit_bytes, traffic_used_bytes, last_reset_at,
		device_limit, hosts_allowlist, hosts_blocklist,
		sub_token, sub_token_rotated_at,
		sub_token_prev, sub_token_prev_expires_at,
		telegram_id, email,
		created_at, updated_at
	FROM users`

// rowScanner is the minimum interface both
// pgx.Row and pgx.Rows satisfy. scanUser uses it
// so the same code reads single-row and multi-row
// queries.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanUser reads one row into a User. The column
// order MUST match baseSelect. Any new column must
// be added to baseSelect AND to this function in
// the same position.
func scanUser(row rowScanner) (*User, error) {
	var (
		u                 User
		externalID        *string
		planID            *uuid.UUID
		expireAt          *time.Time
		lastResetAt       *time.Time
		allowlist         []byte
		blocklist         []byte
		subTokenRotatedAt *time.Time
		subTokenPrev      *string
		prevExpiresAt     *time.Time
		telegramID        *int64
		email             *string
	)
	if err := row.Scan(
		&u.ID,
		&externalID,
		&u.Username,
		&u.Status,
		&planID,
		&expireAt,
		&u.TrafficLimitBytes,
		&u.TrafficUsedBytes,
		&lastResetAt,
		&u.DeviceLimit,
		&allowlist,
		&blocklist,
		&u.SubToken,
		&subTokenRotatedAt,
		&subTokenPrev,
		&prevExpiresAt,
		&telegramID,
		&email,
		&u.CreatedAt,
		&u.UpdatedAt,
	); err != nil {
		return nil, err
	}
	u.ExternalID = derefString(externalID)
	u.PlanID = planID
	u.ExpireAt = expireAt
	u.LastResetAt = lastResetAt
	if len(allowlist) > 0 {
		if err := json.Unmarshal(allowlist, &u.HostsAllowlist); err != nil {
			return nil, fmt.Errorf("scan: hosts_allowlist: %w", err)
		}
	}
	if u.HostsAllowlist == nil {
		u.HostsAllowlist = []uuid.UUID{}
	}
	if len(blocklist) > 0 {
		if err := json.Unmarshal(blocklist, &u.HostsBlocklist); err != nil {
			return nil, fmt.Errorf("scan: hosts_blocklist: %w", err)
		}
	}
	if u.HostsBlocklist == nil {
		u.HostsBlocklist = []uuid.UUID{}
	}
	u.SubTokenRotatedAt = subTokenRotatedAt
	u.SubTokenPrev = derefString(subTokenPrev)
	u.SubTokenPrevExpiresAt = prevExpiresAt
	u.TelegramID = telegramID
	u.Email = derefString(email)
	return &u, nil
}

// mapPgError translates pgx errors into the
// package's sentinel errors. Currently catches
// SQLSTATE 23505 (unique_violation) and maps to
// ErrDuplicate. Other errors pass through with
// the operation prefix.
func mapPgError(err error, op string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: %s: %s", ErrDuplicate, op, pgErr.ConstraintName)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// nullableString returns nil for "" so the pgx
// driver writes SQL NULL instead of an empty
// string. The DB columns are nullable (TEXT without
// NOT NULL).
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// derefString returns "" for nil — the inverse of
// nullableString. Used in scanUser to convert the
// nullable column into a non-nullable Go string
// field (the field's `omitempty` JSON tag covers
// the API surface).
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Sentinel for tests that want to assert the
// pgx implementation is wired to the Store
// interface. (Compile-time check, not a runtime
// test.)
var _ Store = (*PgStore)(nil)
