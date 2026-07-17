// SPDX-License-Identifier: AGPL-3.0-or-later
//
//go:build integration

// Integration tests for PgStore. Run with:
//
//	make test-integration
//
// or
//
//	INTEGRATION_DATABASE_URL=postgres://... go test -tags=integration ./internal/panelcfg/...
//
// The `//go:build integration` tag keeps `go test ./...` fast
// and dependency-free. CI runs the tagged suite with a
// service-container Postgres.
//
// # Setup
//
// Each test starts from a fresh database (created by
// `testutil.MustNewPool`, which DROPs + CREATEs the
// target DB and applies every migration in
// `migrations/`). The default row from migration 0010
// is therefore present at the start of every test —
// GetActive must return it without any seeding.

// Package panelcfg — integration tests for PgStore.
package panelcfg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/QAdversif/AegisPanel/testutil"
)

// helpers ----------------------------------------------------------------

// runPgStore creates a fresh PgStore from the shared
// integration pool and returns the pool too — the
// helpers below need raw SQL access for seeding and
// for asserting on table state directly.
func runPgStore(t *testing.T) (*PgStore, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.MustNewPool(t)
	return NewPgStore(pool), pool
}

// countActiveRows returns the number of rows in
// `panel_path_config` with `is_active = TRUE` and
// `expires_at IS NULL OR expires_at > now()`. Used to
// assert the "at most one active row" invariant after
// SetActive / Reset calls.
func countActiveRows(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM panel_path_config
		WHERE is_active = TRUE
		  AND (expires_at IS NULL OR expires_at > NOW())`).Scan(&n)
	if err != nil {
		t.Fatalf("count active rows: %v", err)
	}
	return n
}

// GetActive --------------------------------------------------------------

// TestPgStore_GetActive_DefaultRowSeeded — migration
// 0010 inserts the sentinel default row with
// `sub_path = ''`. GetActive must return it on a
// fresh database without any further setup.
func TestPgStore_GetActive_DefaultRowSeeded(t *testing.T) {
	store, _ := runPgStore(t)
	got, err := store.GetActive(context.Background())
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if got.SubPath != DefaultSubPath {
		t.Errorf("SubPath = %q, want %q (the empty default)", got.SubPath, DefaultSubPath)
	}
	if !got.IsActive {
		t.Errorf("IsActive = false, want true")
	}
	if got.ID != SentinelID {
		t.Errorf("ID = %v, want sentinel %v", got.ID, SentinelID)
	}
	if got.ExpiresAt != nil {
		t.Errorf("ExpiresAt = %v, want nil for default", got.ExpiresAt)
	}
}

// TestPgStore_GetActive_NoActiveReturnsErrNotFound —
// if the seeded default row is deactivated, GetActive
// returns ErrNotFound. This path is hard to reach in
// production (the default row is only deactivated by
// an explicit Reset + manual deactivation) but the
// store must still return a clean error.
func TestPgStore_GetActive_NoActiveReturnsErrNotFound(t *testing.T) {
	store, pool := runPgStore(t)

	// Manually deactivate the seeded default row. The
	// store has no public method for this; raw SQL is
	// the only path. The Reset API re-activates the
	// sentinel row, so the only way to reach the
	// "no active row" state is a manual intervention.
	if _, err := pool.Exec(context.Background(),
		`UPDATE panel_path_config SET is_active = FALSE WHERE id = $1`, SentinelID); err != nil {
		t.Fatalf("deactivate sentinel: %v", err)
	}

	_, err := store.GetActive(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// GetByID ----------------------------------------------------------------

// TestPgStore_GetByID_Sentinel — the default row is
// addressable by its sentinel id.
func TestPgStore_GetByID_Sentinel(t *testing.T) {
	store, _ := runPgStore(t)
	got, err := store.GetByID(context.Background(), SentinelID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != SentinelID {
		t.Errorf("ID = %v, want %v", got.ID, SentinelID)
	}
	if got.SubPath != DefaultSubPath {
		t.Errorf("SubPath = %q, want %q", got.SubPath, DefaultSubPath)
	}
}

// TestPgStore_GetByID_UnknownReturnsErrNotFound — a
// non-sentinel UUID with no matching row must return
// ErrNotFound, not a nil error.
func TestPgStore_GetByID_UnknownReturnsErrNotFound(t *testing.T) {
	store, _ := runPgStore(t)
	_, err := store.GetByID(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// SetActive --------------------------------------------------------------

// TestPgStore_SetActive_InsertsNewActiveRow — SetActive
// deactivates the previous active row and inserts a
// fresh one with `is_active = TRUE`. The new row's
// `SubPath` is exactly what the caller passed.
func TestPgStore_SetActive_InsertsNewActiveRow(t *testing.T) {
	store, _ := runPgStore(t)
	ctx := context.Background()

	const newPath = "aabbccdd-11223344"
	got, err := store.SetActive(ctx, newPath, 0)
	if err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if got.SubPath != newPath {
		t.Errorf("SubPath = %q, want %q", got.SubPath, newPath)
	}
	if !got.IsActive {
		t.Errorf("IsActive = false, want true")
	}
	if got.ID == SentinelID {
		t.Errorf("ID = sentinel, want a fresh UUID for the rotated row")
	}

	// GetActive now returns the new row.
	active, err := store.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if active.ID != got.ID {
		t.Errorf("GetActive.ID = %v, want %v", active.ID, got.ID)
	}
}

// TestPgStore_SetActive_RejectsInvalidPath — the
// validator runs before any SQL is issued. The
// original default row must remain untouched on a
// failed SetActive.
func TestPgStore_SetActive_RejectsInvalidPath(t *testing.T) {
	store, _ := runPgStore(t)
	ctx := context.Background()

	before, err := store.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive (before): %v", err)
	}

	// Empty path is not allowed (default is reserved
	// for Reset).
	if _, err := store.SetActive(ctx, "", 0); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("SetActive(\"\") err = %v, want ErrInvalidPath", err)
	}
	// Too short.
	if _, err := store.SetActive(ctx, "abc", 0); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("SetActive(\"abc\") err = %v, want ErrInvalidPath", err)
	}
	// Invalid char.
	if _, err := store.SetActive(ctx, "AB!@#", 0); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("SetActive(\"AB!@#\") err = %v, want ErrInvalidPath", err)
	}

	after, err := store.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive (after): %v", err)
	}
	if after.ID != before.ID {
		t.Errorf("active row changed after a failed SetActive: %v vs %v", after.ID, before.ID)
	}
}

// TestPgStore_SetActive_GraceWindow — when a
// non-zero grace is supplied, the OLD active row's
// `expires_at` is set to `now + grace` and its
// `is_active` is FALSE. The OLD row is still
// queryable by id; GetActive filters it out (because
// the predicate is `expires_at > now()`).
func TestPgStore_SetActive_GraceWindow(t *testing.T) {
	store, pool := runPgStore(t)
	ctx := context.Background()

	before, err := store.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}

	const newPath = "grace-test-001"
	const grace = 1 * time.Hour
	got, err := store.SetActive(ctx, newPath, grace)
	if err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if got.SubPath != newPath {
		t.Errorf("SubPath = %q, want %q", got.SubPath, newPath)
	}

	// Old row: still in the table, but is_active = FALSE
	// and expires_at is set to ~now + grace.
	var (
		oldIsActive bool
		oldExpires  *time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT is_active, expires_at FROM panel_path_config WHERE id = $1`, before.ID,
	).Scan(&oldIsActive, &oldExpires); err != nil {
		t.Fatalf("query old row: %v", err)
	}
	if oldIsActive {
		t.Errorf("old row is_active = true, want false")
	}
	if oldExpires == nil {
		t.Fatalf("old row expires_at = nil, want set to ~now+grace")
	}
	// Allow a ±10s slack for clock drift between Go
	// and the testutil's Now().
	delta := time.Until(*oldExpires) - grace
	if delta < -10*time.Second || delta > 10*time.Second {
		t.Errorf("old row expires_at = %v, want ~now+%v (delta %v)", oldExpires, grace, delta)
	}
}

// TestPgStore_SetActive_NoGraceClearsExpiry — when
// grace = 0, the old row's `expires_at` is set to
// NULL (immediate cut-over). The default behaviour
// matches the 3X-UI convention.
func TestPgStore_SetActive_NoGraceClearsExpiry(t *testing.T) {
	store, pool := runPgStore(t)
	ctx := context.Background()

	before, err := store.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}

	if _, err := store.SetActive(ctx, "immediate-cut-001", 0); err != nil {
		t.Fatalf("SetActive: %v", err)
	}

	var oldExpires *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT expires_at FROM panel_path_config WHERE id = $1`, before.ID,
	).Scan(&oldExpires); err != nil {
		t.Fatalf("query old row: %v", err)
	}
	if oldExpires != nil {
		t.Errorf("old row expires_at = %v, want nil for grace=0", oldExpires)
	}
}

// TestPgStore_SetActive_AtMostOneActive — the
// "at most one active row" invariant must hold after
// any number of SetActive calls. We do three
// consecutive rotations and assert that the table
// has exactly one active row at each step.
func TestPgStore_SetActive_AtMostOneActive(t *testing.T) {
	store, pool := runPgStore(t)
	ctx := context.Background()

	paths := []string{
		"rotation-one-aaaa",
		"rotation-two-bbbb",
		"rotation-three-ccc",
	}
	for _, p := range paths {
		if _, err := store.SetActive(ctx, p, 0); err != nil {
			t.Fatalf("SetActive(%q): %v", p, err)
		}
		if got := countActiveRows(t, pool); got != 1 {
			t.Errorf("active rows after SetActive(%q) = %d, want 1", p, got)
		}
	}

	// Final state: the most recent SetActive's path.
	active, err := store.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if active.SubPath != paths[len(paths)-1] {
		t.Errorf("active SubPath = %q, want %q", active.SubPath, paths[len(paths)-1])
	}
}

// TestPgStore_SetActive_RejectsDuplicatePath — the
// `sub_path` column has a UNIQUE constraint. Two
// SetActive calls with the same path result in the
// second one failing with ErrInvalidPath (the closest
// semantic mapping; the duplicate would also surface
// as 23505 unique_violation, which we collapse to
// ErrInvalidPath because the operator is the only
// caller and the validator already rejected the
// obvious duplicates on the validation path).
func TestPgStore_SetActive_RejectsDuplicatePath(t *testing.T) {
	store, _ := runPgStore(t)
	ctx := context.Background()

	const dup = "duplicate-path-zzzz"
	if _, err := store.SetActive(ctx, dup, 0); err != nil {
		t.Fatalf("first SetActive: %v", err)
	}
	_, err := store.SetActive(ctx, dup, 0)
	if !errors.Is(err, ErrInvalidPath) {
		t.Errorf("second SetActive err = %v, want ErrInvalidPath", err)
	}
}

// Reset ------------------------------------------------------------------

// TestPgStore_Reset_ReactivatesDefault — Reset
// deactivates the active row and re-activates the
// sentinel default row. GetActive must return the
// sentinel row with `sub_path = ''`.
func TestPgStore_Reset_ReactivatesDefault(t *testing.T) {
	store, pool := runPgStore(t)
	ctx := context.Background()

	// Rotate twice so we have a non-default active
	// row and at least one historical row.
	if _, err := store.SetActive(ctx, "before-reset-aaaa", 0); err != nil {
		t.Fatalf("SetActive 1: %v", err)
	}
	if _, err := store.SetActive(ctx, "before-reset-bbbb", 0); err != nil {
		t.Fatalf("SetActive 2: %v", err)
	}

	reset, err := store.Reset(ctx)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if reset.ID != SentinelID {
		t.Errorf("Reset.ID = %v, want sentinel %v", reset.ID, SentinelID)
	}
	if reset.SubPath != DefaultSubPath {
		t.Errorf("Reset.SubPath = %q, want %q", reset.SubPath, DefaultSubPath)
	}

	// Exactly one active row, and it is the sentinel.
	if got := countActiveRows(t, pool); got != 1 {
		t.Errorf("active rows after Reset = %d, want 1", got)
	}
	active, err := store.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive after Reset: %v", err)
	}
	if active.ID != SentinelID {
		t.Errorf("active.ID = %v, want sentinel %v", active.ID, SentinelID)
	}
}

// TestPgStore_Reset_OnlyOneActiveAfterMany —
// alternating SetActive / Reset calls must each
// leave the table with exactly one active row.
func TestPgStore_Reset_OnlyOneActiveAfterMany(t *testing.T) {
	store, pool := runPgStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := store.SetActive(ctx, "alt-path-001", 0); err != nil {
			t.Fatalf("SetActive #%d: %v", i, err)
		}
		if got := countActiveRows(t, pool); got != 1 {
			t.Errorf("active rows after SetActive #%d = %d, want 1", i, got)
		}
		if _, err := store.Reset(ctx); err != nil {
			t.Fatalf("Reset #%d: %v", i, err)
		}
		if got := countActiveRows(t, pool); got != 1 {
			t.Errorf("active rows after Reset #%d = %d, want 1", i, got)
		}
	}
}
