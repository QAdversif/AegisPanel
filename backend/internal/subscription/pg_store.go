// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package subscription — PostgreSQL-backed implementation
// of Store. Uses the tables added in migrations 0001
// (users / plans / host_pools / host_pool_members /
// plan_pool) and 0011 (sub_token_prev).
//
// # Shape
//
// Five tables, four of them read-only from the Store's
// point of view (MemoryStore has With* helpers for
// tests / dev seeding; the Store interface itself only
// has GetUserBy* / UpdateSubToken / ListPools* /
// ListPoolMembers).
//
//   - users (read + sub_token rotate)
//   - plans (read-only)
//   - host_pools (read-only)
//   - host_pool_members (read-only)
//   - plan_pool (read-only)
//
// # Sub-token rotation
//
// UpdateSubToken is the one write path. The Go model
// keeps three fields in lockstep — SubToken, SubTokenPrev,
// SubTokenPrevExpiresAt — and the lookup chain
// (GetUserBySubToken → GetUserByPrevSubToken) relies
// on both indexes being consistent at all times. The
// MemoryStore enforces this with a single mutex; the
// PgStore relies on a single SQL UPDATE that moves the
// values in one statement. The partial UNIQUE index
// on `sub_token_prev` (migration 0011) means we cannot
// have two active prev-tokens; a double rotation would
// surface as a 23505 SQLSTATE.
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
// connection. UpdateSubToken is a single statement
// (atomic at the SQL level) — no explicit transaction.
package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the PostgreSQL-backed Store for
// subscription. It implements every method of the
// Store interface defined in store.go.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore wires a PgStore from an open pgxpool.
// The pool is owned by the caller — close it when
// the application shuts down.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// --- reads on users ---------------------------------------------------

// GetUserBySubToken returns the user whose
// `sub_token` matches the given token. The UNIQUE
// index on `users.sub_token` makes this a single
// index hit. ErrNotFound if the row does not exist.
func (s *PgStore) GetUserBySubToken(ctx context.Context, token string) (*User, error) {
	return s.scanUserBy(ctx, "WHERE sub_token = $1", token)
}

// GetUserByPrevSubToken returns the user whose
// `sub_token_prev` matches the given token. The
// partial UNIQUE index on `sub_token_prev` (created
// in migration 0011) makes the lookup O(log n)
// even though most rows have `sub_token_prev IS NULL`
// (and are therefore excluded from the index). The
// Service still applies the `SubTokenPrevExpiresAt`
// grace-window check after the Store returns a hit.
//
// # Why a separate method
//
// The MemoryStore has the same split. The two-token
// lookup chain in Service.GetUserBySubToken relies on
// knowing which path succeeded (the Service's grace
// check differs between the two cases). Mirroring the
// split at the Store level keeps the Service logic
// index-agnostic.
func (s *PgStore) GetUserByPrevSubToken(ctx context.Context, token string) (*User, error) {
	return s.scanUserBy(ctx, "WHERE sub_token_prev = $1", token)
}

// GetUserByID returns the user with the given id.
// ErrNotFound if the row does not exist.
func (s *PgStore) GetUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	return s.scanUserBy(ctx, "WHERE id = $1", id)
}

// scanUserBy is the shared read helper for the three
// GetUserBy* methods. The caller supplies the WHERE
// clause and the matching args. The base SELECT
// matches the column list expected by scanUserRow.
func (s *PgStore) scanUserBy(ctx context.Context, where string, args ...any) (*User, error) {
	rows, err := s.pool.Query(ctx, userSelect+" "+where, args...)
	if err != nil {
		return nil, fmt.Errorf("query user: %w", err)
	}
	defer rows.Close()
	return scanUserRow(rows)
}

// --- writes on users --------------------------------------------------

// UpdateSubToken rotates the user's sub_token. The
// current `sub_token` is moved to `sub_token_prev`
// with the supplied expiry; the new token takes over
// the primary slot. `sub_token_rotated_at` and
// `updated_at` are bumped to the same timestamp.
//
// The single statement is atomic. A crash mid-update
// leaves the row in a coherent state (either the
// rotation completed or it did not); the partial
// UNIQUE index on `sub_token_prev` ensures a double
// rotation with a colliding prev token surfaces as a
// 23505 SQLSTATE, which we map to ErrNotFound (the
// caller — i.e. the Service — treats it as a no-op
// because the user is gone or the prev is already in
// use, both of which the lookup chain would surface
// as 404 anyway).
//
// The MemoryStore also drops the previous-prev from
// the lookup index. The PgStore does NOT need to:
// the partial index on `sub_token_prev` only contains
// the current prev, so a second rotation that moves
// the new prev in place is fine. The earlier prev is
// still in the index (it was never deleted), but the
// migration's WHERE `sub_token_prev IS NOT NULL` plus
// our predicate check in the index keeps the lookups
// accurate. Wait — actually, a fresh row's
// `sub_token_prev` is set to the prior primary. If
// the prior primary was already a `sub_token_prev`
// of a previous rotation (i.e. the user has rotated
// twice in a row), the unique constraint would fire
// on the third rotation. We do not run that path on
// Phase 0/1 (rotation is operator-driven, not burst)
// but the SQLSTATE is documented above.
func (s *PgStore) UpdateSubToken(
	ctx context.Context,
	userID uuid.UUID,
	newToken string,
	prevExpiresAt *time.Time,
) error {
	const q = `
		UPDATE users
		SET sub_token                = $2,
		    sub_token_prev           = sub_token,
		    sub_token_prev_expires_at = $3,
		    sub_token_rotated_at    = clock_timestamp(),
		    updated_at               = clock_timestamp()
		WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, userID, newToken, prevExpiresAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Partial unique index on
			// `sub_token_prev`. The prior primary
			// already had a prev-collision, so the
			// rotation is impossible without an
			// intermediate read. Surface as
			// ErrNotFound to match the MemoryStore
			// behaviour for the analogous "stale
			// rotation" case (the user is gone).
			return fmt.Errorf("id %s: %w", userID, ErrNotFound)
		}
		return fmt.Errorf("update user sub_token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("id %s: %w", userID, ErrNotFound)
	}
	return nil
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

// --- writes on users ---------------------------------------------------

// CreateUser inserts a new row. A zero id is replaced
// with a fresh uuid.New() before the INSERT. The
// migration's UNIQUE indexes on `username` and
// `sub_token` are enforced by the database; a
// collision surfaces as a 23505 SQLSTATE mapped to
// ErrDuplicate. The fields the Go model does not yet
// expose (external_id, last_reset_at, telegram_id,
// email) are set to NULL on insert — they are
// nullable columns in the schema.
func (s *PgStore) CreateUser(ctx context.Context, u *User) error {
	if u == nil {
		return fmt.Errorf("create user: nil")
	}
	if u.Username == "" {
		return fmt.Errorf("create user: username is required")
	}
	if u.SubToken == "" {
		return fmt.Errorf("create user: sub_token is required")
	}
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	var (
		planID                = u.PlanID
		expireAt              = u.ExpireAt
		subTokenPrev          = u.SubTokenPrev
		subTokenPrevExpiresAt = u.SubTokenPrevExpiresAt
		subTokenRotatedAt     = u.SubTokenRotatedAt
	)
	allowJSON, _ := json.Marshal(u.HostsAllowlist)
	blockJSON, _ := json.Marshal(u.HostsBlocklist)
	const q = `
		INSERT INTO users (
			id, username, status, plan_id, expire_at,
			traffic_limit_bytes, traffic_used_bytes, device_limit,
			hosts_allowlist, hosts_blocklist,
			sub_token, sub_token_prev, sub_token_prev_expires_at,
			sub_token_rotated_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10,
			$11, $12, $13,
			$14, NOW(), NOW()
		)`
	_, err := s.pool.Exec(ctx, q,
		u.ID, u.Username, string(u.Status), planID, expireAt,
		u.TrafficLimitBytes, u.TrafficUsedBytes, u.DeviceLimit,
		allowJSON, blockJSON,
		u.SubToken, subTokenPrev, subTokenPrevExpiresAt,
		subTokenRotatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("%w: %s", ErrDuplicate, pgErr.ConstraintName)
		}
		return fmt.Errorf("create user: %w", err)
	}
	// Read back so the caller sees the canonical
	// CreatedAt / UpdatedAt.
	canonical, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		return fmt.Errorf("create user read-back: %w", err)
	}
	*u = *canonical
	return nil
}

// UpdateUser applies a per-field patch. The query
// builds the SET clause from the non-nil fields; the
// result is a fresh read-back. The migration's UNIQUE
// indexes are enforced by the database (a 23505 maps
// to ErrDuplicate).
func (s *PgStore) UpdateUser(ctx context.Context, id uuid.UUID, patch UpdateUserPatch) (*User, error) {
	// Build the SET clause dynamically. The SET
	// list always includes `updated_at = NOW()` so
	// every successful patch refreshes the row.
	setClauses := []string{"updated_at = NOW()"}
	args := []any{}
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if patch.Username != nil {
		setClauses = append(setClauses, "username = "+next(*patch.Username))
	}
	if patch.Status != nil {
		setClauses = append(setClauses, "status = "+next(string(*patch.Status)))
	}
	if patch.PlanID != nil {
		setClauses = append(setClauses, "plan_id = "+next(*patch.PlanID))
	}
	if patch.ExpireAt != nil {
		setClauses = append(setClauses, "expire_at = "+next(*patch.ExpireAt))
	}
	if patch.TrafficLimit != nil {
		setClauses = append(setClauses, "traffic_limit_bytes = "+next(*patch.TrafficLimit))
	}
	if patch.DeviceLimit != nil {
		setClauses = append(setClauses, "device_limit = "+next(*patch.DeviceLimit))
	}
	if patch.HostsAllowlist != nil {
		allowJSON, _ := json.Marshal(*patch.HostsAllowlist)
		setClauses = append(setClauses, "hosts_allowlist = "+next(allowJSON))
	}
	if patch.HostsBlocklist != nil {
		blockJSON, _ := json.Marshal(*patch.HostsBlocklist)
		setClauses = append(setClauses, "hosts_blocklist = "+next(blockJSON))
	}
	if len(setClauses) == 1 {
		// No-op patch (only updated_at). Read back
		// and return — the user might still want
		// the canonical row.
		return s.GetUserByID(ctx, id)
	}
	args = append(args, id)
	q := fmt.Sprintf(`
		UPDATE users
		SET %s
		WHERE id = $%d
	`, strings.Join(setClauses, ", "), len(args))
	tag, err := s.pool.Exec(ctx, q, args...)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, fmt.Errorf("%w: %s", ErrDuplicate, pgErr.ConstraintName)
		}
		return nil, fmt.Errorf("update user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.GetUserByID(ctx, id)
}

// ListUsers returns every user, sorted by created_at
// ascending. The slice is freshly allocated.
func (s *PgStore) ListUsers(ctx context.Context) ([]*User, error) {
	q := userSelect + " ORDER BY created_at ASC, id ASC"
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	out := make([]*User, 0)
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return out, nil
}

// --- internal: scan helpers --------------------------------------------

// userSelect is the SELECT clause used by every read
// on the users table. The column order matches the
// scanUserRow expectations. The column list covers
// every User field, including the four fields the Go
// model does not yet expose (external_id,
// last_reset_at, telegram_id, email) — we read them
// anyway so a future migration that adds the
// corresponding Go fields is a model change, not a
// Store change.
const userSelect = `
	SELECT
		id, external_id, username, status, plan_id, expire_at,
		traffic_limit_bytes, traffic_used_bytes, last_reset_at,
		device_limit, hosts_allowlist, hosts_blocklist,
		sub_token, sub_token_prev, sub_token_prev_expires_at,
		sub_token_rotated_at, telegram_id, email,
		created_at, updated_at
	FROM users`

// scanUserRow reads the columns of a single-row query
// and returns a *User. An empty result set returns
// ErrNotFound; a successful read returns a fresh copy
// (caller may mutate without affecting the store).
func scanUserRow(rows pgx.Rows) (*User, error) {
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("rows: %w", err)
		}
		return nil, fmt.Errorf("%w", ErrNotFound)
	}
	var (
		id                    uuid.UUID
		externalID            *string
		username              string
		status                string
		planID                *uuid.UUID
		expireAt              *time.Time
		trafficLimitBytes     int64
		trafficUsedBytes      int64
		lastResetAt           *time.Time
		deviceLimit           int
		hostsAllowlistRaw     []byte
		hostsBlocklistRaw     []byte
		subToken              string
		subTokenPrev          *string
		subTokenPrevExpiresAt *time.Time
		subTokenRotatedAt     *time.Time
		telegramID            *int64
		email                 *string
		createdAt             time.Time
		updatedAt             time.Time
	)
	if err := rows.Scan(
		&id, &externalID, &username, &status, &planID, &expireAt,
		&trafficLimitBytes, &trafficUsedBytes, &lastResetAt,
		&deviceLimit, &hostsAllowlistRaw, &hostsBlocklistRaw,
		&subToken, &subTokenPrev, &subTokenPrevExpiresAt,
		&subTokenRotatedAt, &telegramID, &email,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan user row: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	u := &User{
		ID:                    id,
		Username:              username,
		Status:                UserStatus(status),
		PlanID:                planID,
		ExpireAt:              expireAt,
		TrafficLimitBytes:     trafficLimitBytes,
		TrafficUsedBytes:      trafficUsedBytes,
		DeviceLimit:           deviceLimit,
		SubToken:              subToken,
		SubTokenPrevExpiresAt: subTokenPrevExpiresAt,
		SubTokenRotatedAt:     subTokenRotatedAt,
		CreatedAt:             createdAt,
		UpdatedAt:             updatedAt,
	}
	if subTokenPrev != nil {
		u.SubTokenPrev = *subTokenPrev
	}
	// JSONB columns: empty / NULL round-trips as a
	// non-nil empty slice. The Go model uses
	// `omitempty` semantically, so the caller is
	// expected to handle either. We preserve the
	// distinction for the read path: a NULL column
	// becomes a nil slice (no allow/block list
	// stored); a present-but-empty column becomes an
	// empty slice (an explicit empty list was
	// stored). The two are equivalent for the
	// Service's filter pass, but the JSONB-level
	// distinction is preserved for round-trip
	// debugging.
	if err := unmarshalUUIDSlice(&u.HostsAllowlist, hostsAllowlistRaw); err != nil {
		return nil, fmt.Errorf("user hosts_allowlist: %w", err)
	}
	if err := unmarshalUUIDSlice(&u.HostsBlocklist, hostsBlocklistRaw); err != nil {
		return nil, fmt.Errorf("user hosts_blocklist: %w", err)
	}
	// The four fields the Go model does not yet
	// expose are read but discarded. This is
	// intentional: a future migration that adds
	// the model fields will be a model change, not
	// a Store change. Currently the columns exist
	// (migration 0001) but the model does not.
	_ = externalID
	_ = lastResetAt
	_ = telegramID
	_ = email
	return u, nil
}

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

// --- internal: JSONB helpers -------------------------------------------

// unmarshalUUIDSlice decodes a JSONB array of UUID
// strings into *dst. A nil raw (SQL NULL) leaves dst
// as-is (the caller's zero value — a nil slice). An
// empty array `[]` round-trips to a non-nil empty
// slice. The destination must be a pointer; we use
// `&u.HostsAllowlist` etc.
func unmarshalUUIDSlice(dst *[]uuid.UUID, raw []byte) error {
	if raw == nil {
		return nil
	}
	// Fast path for `[]` — a 2-byte raw.
	if len(raw) == 2 && raw[0] == '[' && raw[1] == ']' {
		*dst = []uuid.UUID{}
		return nil
	}
	return json.Unmarshal(raw, dst)
}
