// SPDX-License-Identifier: AGPL-3.0-or-later
//
// PgStore is the PostgreSQL-backed implementation of
// Store. It uses the `plans` table (migration
// 0001_initial.sql). The `plan_pool` join table is
// intentionally NOT touched by this Store in v0.6.0
// — the `subscription` package keeps its read-only
// view of plan_pool for the render path. v0.6.x will
// fold the plan_pool CRUD into this Store and let
// the subscription package delegate to it.
//
// # Shape
//
// One row in `plans` per Plan. The (name) UNIQUE
// constraint maps to ErrDuplicate (Postgres
// SQLSTATE 23505). The (reset_period) CHECK matches
// the Go AllowedResetPeriods set; the Service is
// the authoritative gate.
//
// # Duration handling
//
// The `duration` column is `INTERVAL NOT NULL`. The
// Service stores Duration as a `time.Duration`
// (nanoseconds). The PgStore encodes / decodes it
// as int64 microseconds (the natural unit for
// Postgres INTERVAL — `bigint::INTERVAL` interprets
// the value as microseconds, matching the
// intervalToDuration decode on the read side).
//
// Why microseconds, not the pgx-native
// `pgtype.Interval`? Two reasons:
//
//   1. The `pgtype.Interval.Valid` bool makes the
//      zero value encode as SQL NULL, which silently
//      blows up against the `NOT NULL` constraint
//      with a confusing error (we hit this once — see
//      PR #131 CI failure). Microsecond-int64 +
//      `::INTERVAL` cast is bulletproof.
//
//   2. The Subscription package's `Plan.Duration`
//      is already `time.Duration` (nanoseconds), so
//      a single canonical unit on the Go side keeps
//      the call sites clean.
//
// Sub-microsecond precision is lost (Postgres
// INTERVAL is microsecond-precision). The
// subscription tariff use case does not need
// nanosecond granularity; the human-readable
// "30 days" / "1 year" / "1 hour" granularity is
// what the operator picks from the UI.
//
// Months are NOT modelled. The Go side has
// `time.Duration` (nanoseconds) which is
// month-unaware. A 30-day tariff round-trips
// exactly. A 31-day tariff round-trips as 31 days
// (no "month" component is involved).
//
// # Concurrency
//
// pgxpool handles connection pooling. Each call
// uses its own connection from the pool. Create /
// Update / Delete are single-statement operations
// (no children to atomically insert), so no
// explicit transaction is needed.

package plans

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the PostgreSQL-backed Store.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore wires a PgStore from an open pgxpool.
// The pool is owned by the caller — close it when
// the application shuts down.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// Create inserts a new plan row. Returns ErrDuplicate
// on (name) collision (Postgres SQLSTATE 23505 →
// unique_violation).
func (s *PgStore) Create(ctx context.Context, p *Plan) error {
	if p == nil {
		return fmt.Errorf("create: nil plan")
	}
	if p.ID == uuid.Nil {
		return fmt.Errorf("create: zero id")
	}
	if !p.IsValid() {
		return fmt.Errorf("create: invalid plan")
	}
	const q = `
		INSERT INTO plans (
			id, name, traffic_limit_bytes, duration,
			device_limit, reset_period, price_cents
		) VALUES (
			$1, $2, $3, $4::INTERVAL,
			$5, $6, $7
		)`
	_, err := s.pool.Exec(ctx, q,
		p.ID,
		p.Name,
		p.TrafficLimitBytes,
		durationToMicroseconds(p.Duration),
		p.DeviceLimit,
		string(p.ResetPeriod),
		p.PriceCents,
	)
	if err != nil {
		return mapPgError(err, "create")
	}
	return nil
}

// GetByID returns the plan with the given id, or
// ErrNotFound.
func (s *PgStore) GetByID(ctx context.Context, id uuid.UUID) (*Plan, error) {
	const q = baseSelect + ` WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	p, err := scanPlan(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: id %s", ErrNotFound, id)
		}
		return nil, err
	}
	return p, nil
}

// GetByName returns the plan with the given name, or
// ErrNotFound.
func (s *PgStore) GetByName(ctx context.Context, name string) (*Plan, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: empty name", ErrNotFound)
	}
	const q = baseSelect + ` WHERE name = $1`
	row := s.pool.QueryRow(ctx, q, name)
	p, err := scanPlan(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: name %q", ErrNotFound, name)
		}
		return nil, err
	}
	return p, nil
}

// List returns every plan, sorted by CreatedAt asc.
func (s *PgStore) List(ctx context.Context) ([]*Plan, error) {
	const q = baseSelect + ` ORDER BY created_at ASC`
	return s.listQuery(ctx, q)
}

// Update replaces the stored copy of p.ID. Returns
// ErrNotFound if no such plan exists; ErrDuplicate
// if the rename would collide.
func (s *PgStore) Update(ctx context.Context, p *Plan) error {
	if p == nil {
		return fmt.Errorf("update: nil plan")
	}
	if p.ID == uuid.Nil {
		return fmt.Errorf("update: zero id")
	}
	if !p.IsValid() {
		return fmt.Errorf("update: invalid plan")
	}
	const q = `
		UPDATE plans SET
			name = $2,
			traffic_limit_bytes = $3,
			duration = $4::INTERVAL,
			device_limit = $5,
			reset_period = $6,
			price_cents = $7,
			updated_at = NOW()
		WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q,
		p.ID,
		p.Name,
		p.TrafficLimitBytes,
		durationToMicroseconds(p.Duration),
		p.DeviceLimit,
		string(p.ResetPeriod),
		p.PriceCents,
	)
	if err != nil {
		return mapPgError(err, "update")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: id %s", ErrNotFound, p.ID)
	}
	return nil
}

// Delete removes the plan with the given id. Returns
// ErrNotFound if no such plan exists.
//
// The `users.plan_id` column has no FK constraint
// (migration 0001), so deleting a plan that still
// has users pointing at it leaves those users with
// a dangling plan_id. The subscription package's
// ListPoolsForUser handles dangling plan IDs by
// returning an empty pool list, so the user
// silently loses access to the plan's pools. The
// UI shows a confirm dialog before Delete.
func (s *PgStore) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM plans WHERE id = $1`, id)
	if err != nil {
		return mapPgError(err, "delete")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: id %s", ErrNotFound, id)
	}
	return nil
}

// listQuery is the shared List body. Centralised so
// the column scan logic is in one place.
func (s *PgStore) listQuery(ctx context.Context, q string, args ...any) ([]*Plan, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list: query: %w", err)
	}
	defer rows.Close()
	out := make([]*Plan, 0)
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, fmt.Errorf("list: scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list: rows: %w", err)
	}
	return out, nil
}

// baseSelect is the column list shared by all
// single-row reads. Kept in lockstep with the
// `plans` table schema — any new column must be
// added here AND to the Plan struct AND to the
// Create / Update queries.
const baseSelect = `
	SELECT
		id, name, traffic_limit_bytes, duration,
		device_limit, reset_period, price_cents,
		created_at, updated_at
	FROM plans`

// rowScanner is the minimum interface both
// pgx.Row and pgx.Rows satisfy. scanPlan uses it so
// the same code reads single-row and multi-row
// queries.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanPlan reads one row into a Plan. The column
// order MUST match baseSelect. Any new column must
// be added to baseSelect AND to this function in
// the same position.
func scanPlan(row rowScanner) (*Plan, error) {
	var (
		p        Plan
		interval pgtype.Interval
	)
	if err := row.Scan(
		&p.ID,
		&p.Name,
		&p.TrafficLimitBytes,
		&interval,
		&p.DeviceLimit,
		&p.ResetPeriod,
		&p.PriceCents,
		&p.CreatedAt,
		&p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	p.Duration = intervalToDuration(interval)
	return &p, nil
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

// Sentinel for tests that want to assert the
// pgx implementation is wired to the Store
// interface. (Compile-time check, not a runtime
// test.)
var _ Store = (*PgStore)(nil)

// --- duration <-> microsecond helpers --------------------------------
//
// Encode: time.Duration -> int64 microseconds.
// Postgres `bigint::INTERVAL` interprets the
// value as microseconds, which is the natural
// unit for INTERVAL (sub-microsecond precision
// is not needed for a subscription tariff).
//
// Decode: pgtype.Interval -> time.Duration. pgx
// scans the binary INTERVAL into a
// pgtype.Interval{Months, Days, Microseconds,
// Valid}. We sum the three components (months
// mapped as 30 days, the documented policy — see
// service.go for the rationale) into a single
// time.Duration in nanoseconds.

// durationToMicroseconds converts a time.Duration
// to int64 microseconds. Integer division truncates
// sub-microsecond nanoseconds; the subscription
// tariff use case does not need that precision.
func durationToMicroseconds(d time.Duration) int64 {
	return int64(d / time.Microsecond)
}

// intervalToDuration converts a pgtype.Interval to a
// time.Duration. Months are mapped as 30 days; the
// panel does not model calendar months in a
// Duration (the closest is `reset_period =
// 'monthly'`, which is the traffic-reset cadence,
// a separate field). The choice of 30 days per
// "month" is documented in the service.go package
// doc comment; v0.6.x may introduce a
// calendar-aware duration if real customer demand
// appears.
func intervalToDuration(iv pgtype.Interval) time.Duration {
	const (
		nsPerDay   = int64(24 * 3600 * 1e9)
		nsPerMicro = int64(1e3)
		daysPerMon = int64(30)
	)
	daysFromMonths := int64(iv.Months) * daysPerMon
	total := daysFromMonths*nsPerDay + int64(iv.Days)*nsPerDay + iv.Microseconds*nsPerMicro
	return time.Duration(total)
}
