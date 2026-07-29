// SPDX-License-Identifier: AGPL-3.0-or-later

package backups

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestLocalStoreCRUD walks Insert → Get → List →
// Update → Delete on a real temp directory, with no
// pgx dependency. The LocalStore's index file is the
// only state under test.
func TestLocalStoreCRUD(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	bk, err := NewOSBackend(dir)
	if err != nil {
		t.Fatalf("NewOSBackend: %v", err)
	}
	store := NewLocalStore(bk)

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	row := &Backup{
		ID:        "bck_00000000000000_aaaaaaaa",
		CreatedAt: now,
		Trigger:   TriggerManual,
		Status:    StatusRunning,
		Path:      "bck_00000000000000_aaaaaaaa.dump.gz",
	}
	if err := store.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := store.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != row.ID || got.Path != row.Path {
		t.Fatalf("Get returned %+v, want ID=%s Path=%s", got, row.ID, row.Path)
	}

	// Update
	row.Status = StatusOK
	row.SizeBytes = 4096
	row.ChecksumSHA256 = "deadbeef"
	if err := store.Update(ctx, row); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = store.Get(ctx, row.ID)
	if got.Status != StatusOK || got.SizeBytes != 4096 {
		t.Fatalf("Update did not stick: %+v", got)
	}

	// Insert second
	row2 := &Backup{
		ID:        "bck_00000000000001_bbbbbbbb",
		CreatedAt: now.Add(time.Minute),
		Trigger:   TriggerScheduled,
		Status:    StatusOK,
		Path:      "bck_00000000000001_bbbbbbbb.dump.gz",
	}
	if err := store.Insert(ctx, row2); err != nil {
		t.Fatalf("Insert row2: %v", err)
	}

	// List newest-first
	rows, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != row2.ID || rows[1].ID != row.ID {
		t.Fatalf("List order/contents wrong: %+v", rows)
	}

	// Delete idempotent
	if err := store.Delete(ctx, row.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(ctx, row.ID); err != nil {
		t.Fatalf("Delete (idempotent): %v", err)
	}
	rows, _ = store.List(ctx)
	if len(rows) != 1 {
		t.Fatalf("after delete: %d rows, want 1", len(rows))
	}

	// Get missing
	if _, err := store.Get(ctx, "nonexistent"); err == nil {
		t.Fatal("Get nonexistent: want ErrNotFound, got nil")
	}
}

// TestLocalStoreIndexFileExists asserts that the
// osBackend was actually created during the test
// (covers the mkdir branch).
func TestOSBackendMkdir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "backups")
	if _, err := NewOSBackend(dir); err != nil {
		t.Fatalf("NewOSBackend created-nested: %v", err)
	}
	// Re-open: should not fail.
	if _, err := NewOSBackend(dir); err != nil {
		t.Fatalf("NewOSBackend re-open: %v", err)
	}
}
