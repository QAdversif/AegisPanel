// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration

package users

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/testutil"
)

// TestPgStore_Integration is the smoke test for the
// pgx-backed implementation. It runs only when the
// `integration` build tag is set AND
// INTEGRATION_DATABASE_URL is non-empty (see
// testutil.MustNewPool). The fixture:
//
//  1. takes an advisory lock so concurrent test runs
//     on the same DB do not collide;
//  2. truncates the `users` table;
//  3. runs the assertions;
//  4. cleans up.
//
// Tests live here (vs. in pg_store_test.go) because
// they require a live Postgres and are gated on the
// build tag — the unit tests in pg_store_test.go
// (none yet — the package is small enough that the
// MemoryStore tests cover the behaviour) are the
// fallback when no DB is available.
func TestPgStore_Integration(t *testing.T) {
	ctx := context.Background()
	pool := testutil.MustNewPool(t)
	defer pool.Close()

	// Take an advisory lock so two concurrent test
	// runs against the same DB don't trample each
	// other. The lock id is a stable hash of the
	// table name.
	const lockID = 0x75736572 // "user"
	if _, err := pool.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		t.Fatalf("pg_advisory_lock: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(ctx, "SELECT pg_advisory_unlock($1)", lockID); err != nil {
			t.Logf("pg_advisory_unlock: %v", err)
		}
	}()

	// Wipe the table. The testutil fixture restores
	// the previous state on exit so other integration
	// tests that depend on seeded data are not
	// affected.
	if _, err := pool.Exec(ctx, "TRUNCATE TABLE users RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate users: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "TRUNCATE TABLE users RESTART IDENTITY CASCADE"); err != nil {
			t.Logf("truncate users on cleanup: %v", err)
		}
	})

	s := NewPgStore(pool)

	// --- Create round-trip ----------------------------------
	in := CreateInput{
		Username:          "alice",
		TrafficLimitBytes: 5_000_000_000,
		DeviceLimit:       3,
		Email:             "alice@example.com",
	}
	svc := NewService(s)
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("Service.Create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatalf("ID is zero")
	}
	if created.SubToken == "" {
		t.Fatalf("SubToken is empty")
	}

	got, err := svc.GetByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID = %s, want %s", got.ID, created.ID)
	}
	if got.TrafficLimitBytes != 5_000_000_000 {
		t.Errorf("TrafficLimitBytes = %d, want 5000000000", got.TrafficLimitBytes)
	}
	if got.Email != "alice@example.com" {
		t.Errorf("Email = %q, want %q", got.Email, "alice@example.com")
	}

	// --- Update round-trip -----------------------------------
	newStatus := StatusDisabled
	traffic := int64(2_000)
	updated, err := svc.Update(ctx, got.ID, UpdateInput{
		Status:            &newStatus,
		TrafficLimitBytes: &traffic,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Status != StatusDisabled {
		t.Errorf("Status = %q, want %q", updated.Status, StatusDisabled)
	}
	if updated.TrafficLimitBytes != 2_000 {
		t.Errorf("TrafficLimitBytes = %d, want 2000", updated.TrafficLimitBytes)
	}

	// --- SubToken rotation (migration 0011) -----------------
	rotated, err := svc.RotateSubToken(ctx, got.ID, 1*time.Hour)
	if err != nil {
		t.Fatalf("RotateSubToken: %v", err)
	}
	if rotated.SubToken == created.SubToken {
		t.Errorf("SubToken did not change")
	}
	if rotated.SubTokenPrev != created.SubToken {
		t.Errorf("SubTokenPrev = %q, want %q", rotated.SubTokenPrev, created.SubToken)
	}
	if rotated.SubTokenPrevExpiresAt == nil {
		t.Fatalf("SubTokenPrevExpiresAt is nil")
	}

	// Old token still works (within grace).
	viaPrev, err := svc.GetBySubToken(ctx, created.SubToken, true)
	if err != nil {
		t.Fatalf("GetBySubToken(prev, usePrev=true): %v", err)
	}
	if viaPrev.ID != got.ID {
		t.Errorf("ID via prev = %s, want %s", viaPrev.ID, got.ID)
	}

	// New token also works.
	viaCurrent, err := svc.GetBySubToken(ctx, rotated.SubToken, true)
	if err != nil {
		t.Fatalf("GetBySubToken(current): %v", err)
	}
	if viaCurrent.ID != got.ID {
		t.Errorf("ID via current = %s, want %s", viaCurrent.ID, got.ID)
	}

	// --- List / ListByStatus ---------------------------------
	all, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("List: len = %d, want 1", len(all))
	}
	disabled, err := svc.ListByStatus(ctx, StatusDisabled)
	if err != nil {
		t.Fatalf("ListByStatus(Disabled): %v", err)
	}
	if len(disabled) != 1 {
		t.Errorf("ListByStatus(Disabled): len = %d, want 1", len(disabled))
	}
	active, err := svc.ListByStatus(ctx, StatusActive)
	if err != nil {
		t.Fatalf("ListByStatus(Active): %v", err)
	}
	if len(active) != 0 {
		t.Errorf("ListByStatus(Active): len = %d, want 0", len(active))
	}

	// --- Duplicate username ----------------------------------
	_, err = svc.Create(ctx, CreateInput{Username: "alice"})
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("Create duplicate username: err = %v, want ErrDuplicate", err)
	}

	// --- Delete ----------------------------------------------
	if err := svc.Delete(ctx, got.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = svc.Get(ctx, got.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: err = %v, want ErrNotFound", err)
	}
}
