// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Integration tests for the pgx-backed plans
// Store. Gated on INTEGRATION_DATABASE_URL; skipped
// when the env var is unset (the unit-test path
// uses MemoryStore, which is the canonical
// "no Postgres" path).
//
// The fixture is a single test database with the
// standard `plans` table from migration 0001. The
// tests truncate the table between cases so order
// does not matter; the test process is the only
// writer.

package plans

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// integrationDSN is the DSN the integration tests
// read. When unset, the test binary runs as
// unit-only and every test in this file is
// skipped.
func integrationDSN() string { return os.Getenv("INTEGRATION_DATABASE_URL") }

// newPgStore opens a fresh pgxpool against the
// integration DSN and returns a *PgStore +
// a cleanup func that closes the pool and
// truncates the plans table.
func newPgStore(t *testing.T) (*PgStore, func()) {
	t.Helper()
	dsn := integrationDSN()
	if dsn == "" {
		t.Skip("INTEGRATION_DATABASE_URL not set; skipping pg integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `TRUNCATE TABLE plans`); err != nil {
		t.Fatalf("TRUNCATE plans: %v", err)
	}
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `TRUNCATE TABLE plans`)
		pool.Close()
	}
	return NewPgStore(pool), cleanup
}

// TestPgStore_BasicCRUD is the smoke for the
// pgx-backed Store: Create / GetByID / GetByName /
// Update / List / Delete.
func TestPgStore_BasicCRUD(t *testing.T) {
	ctx := context.Background()
	s, cleanup := newPgStore(t)
	defer cleanup()

	p := &Plan{
		ID:                uuid.New(),
		Name:              "starter",
		TrafficLimitBytes: 5_000_000_000,
		Duration:          30 * 24 * time.Hour,
		DeviceLimit:       3,
		ResetPeriod:       ResetMonthly,
		PriceCents:        500,
	}
	if err := s.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "starter" {
		t.Errorf("Name = %q, want %q", got.Name, "starter")
	}
	// Duration round-trip: encode to days-only
	// Interval, decode back. 30 days = 30 * 24h.
	wantDur := 30 * 24 * time.Hour
	if got.Duration != wantDur {
		t.Errorf("Duration = %s, want %s (30 days round-trip)", got.Duration, wantDur)
	}

	gotByName, err := s.GetByName(ctx, "starter")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if gotByName.ID != p.ID {
		t.Errorf("GetByName: ID = %s, want %s", gotByName.ID, p.ID)
	}

	// Update.
	got.Name = "starter-v2"
	got.PriceCents = 700
	if err := s.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got2, err := s.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID after Update: %v", err)
	}
	if got2.Name != "starter-v2" {
		t.Errorf("Name after Update = %q, want %q", got2.Name, "starter-v2")
	}
	if got2.PriceCents != 700 {
		t.Errorf("PriceCents after Update = %d, want 700", got2.PriceCents)
	}

	// List.
	out, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("List: len = %d, want 1", len(out))
	}
	if out[0].ID != p.ID {
		t.Errorf("List[0].ID = %s, want %s", out[0].ID, p.ID)
	}

	// Delete.
	if err := s.Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetByID(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID after Delete: err = %v, want ErrNotFound", err)
	}
}

// TestPgStore_DuplicateName covers the (name)
// UNIQUE constraint. Two plans with the same name
// must fail with ErrDuplicate (Postgres SQLSTATE
// 23505 → unique_violation).
func TestPgStore_DuplicateName(t *testing.T) {
	ctx := context.Background()
	s, cleanup := newPgStore(t)
	defer cleanup()

	p1 := &Plan{
		ID:          uuid.New(),
		Name:        "dupe",
		Duration:    30 * 24 * time.Hour,
		ResetPeriod: ResetMonthly,
	}
	if err := s.Create(ctx, p1); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	p2 := &Plan{
		ID:          uuid.New(),
		Name:        "dupe",
		Duration:    60 * 24 * time.Hour,
		ResetPeriod: ResetMonthly,
	}
	if err := s.Create(ctx, p2); !errors.Is(err, ErrDuplicate) {
		t.Errorf("Create duplicate: err = %v, want ErrDuplicate", err)
	}
}

// TestPgStore_DurationRoundTrip covers the encode /
// decode path for non-24h-aligned durations. A
// 36-hour plan must round-trip as 36h, not 24h or
// 48h.
func TestPgStore_DurationRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, cleanup := newPgStore(t)
	defer cleanup()

	cases := []time.Duration{
		1 * time.Minute,
		1 * time.Hour,
		36 * time.Hour,
		90 * 24 * time.Hour,
	}
	for _, d := range cases {
		p := &Plan{
			ID:          uuid.New(),
			Name:        "dur-" + d.String(),
			Duration:    d,
			ResetPeriod: ResetMonthly,
		}
		if err := s.Create(ctx, p); err != nil {
			t.Fatalf("Create(%s): %v", d, err)
		}
		got, err := s.GetByID(ctx, p.ID)
		if err != nil {
			t.Fatalf("GetByID(%s): %v", d, err)
		}
		if got.Duration != d {
			t.Errorf("Duration round-trip: got %s, want %s", got.Duration, d)
		}
	}
}

// TestPgStore_NotFound covers the "row missing"
// branch on Get / Update / Delete.
func TestPgStore_NotFound(t *testing.T) {
	ctx := context.Background()
	s, cleanup := newPgStore(t)
	defer cleanup()

	if _, err := s.GetByID(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID missing: err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetByName(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByName missing: err = %v, want ErrNotFound", err)
	}
	p := &Plan{
		ID:          uuid.New(),
		Name:        "ghost",
		Duration:    30 * 24 * time.Hour,
		ResetPeriod: ResetMonthly,
	}
	if err := s.Update(ctx, p); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update missing: err = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete missing: err = %v, want ErrNotFound", err)
	}
}
