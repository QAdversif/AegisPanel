// SPDX-License-Identifier: AGPL-3.0-or-later

package backups

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDump returns a deterministic ReadCloser that
// yields the given bytes. Used in service tests to
// drive Create() without invoking the real pg_dump
// binary.
type fakeDump struct {
	data    []byte
	closed  atomic.Bool
	readErr error
}

func (f *fakeDump) Read(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, io.EOF
	}
	if f.readErr != nil {
		return 0, f.readErr
	}
	n := copy(p, f.data)
	if n < len(f.data) {
		// Stub: pretend we read everything at once.
		return len(f.data), io.EOF
	}
	return n, io.EOF
}

func (f *fakeDump) Close() error {
	f.closed.Store(true)
	return nil
}

func newTestService(t *testing.T, dumpBytes []byte) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	bk, err := NewOSBackend(dir)
	if err != nil {
		t.Fatalf("NewOSBackend: %v", err)
	}
	store := NewLocalStore(bk)
	svc := New(Config{
		BackupsDir:    dir,
		PgDumpBin:     "/bin/true", // never invoked because SetDumpFn overrides
		PgRestoreBin:  "/bin/true",
		RetentionDays: 30,
		MaxCount:      10,
	}, store, nil)
	svc.SetClock(func() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) })
	svc.SetDumpFn(func(ctx context.Context) (io.ReadCloser, error) {
		return &fakeDump{data: dumpBytes}, nil
	})
	return svc, dir
}

func TestServiceCreateHappyPath(t *testing.T) {
	ctx := context.Background()
	svc, dir := newTestService(t, []byte("hello pg_dump world\n"))

	row, err := svc.Create(ctx, TriggerManual)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if row.Status != StatusOK {
		t.Fatalf("status = %q, want ok", row.Status)
	}
	if row.SizeBytes == 0 {
		t.Fatalf("SizeBytes = 0, want > 0")
	}
	if row.ChecksumSHA256 == "" {
		t.Fatal("ChecksumSHA256 is empty")
	}
	// Dump file exists on disk.
	dumpPath := filepath.Join(dir, row.Path)
	if _, err := os.Stat(dumpPath); err != nil {
		t.Fatalf("dump file not on disk: %v", err)
	}
	// Sidecar exists.
	if _, err := os.Stat(dumpPath + ".sha256"); err != nil {
		t.Fatalf("sidecar not on disk: %v", err)
	}
	// Row appears in list.
	rows, _ := svc.List(ctx)
	if len(rows) != 1 || rows[0].ID != row.ID {
		t.Fatalf("List: %+v", rows)
	}
}

func TestServiceCreateSingleFlight(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, []byte("data"))

	// Inject a dumpFn that blocks until `released`.
	// The first Create to acquire the inflight lock
	// will block here; the second Create arriving
	// before release hits TryLock and returns
	// ErrBackupInProgress.
	released := make(chan struct{})
	svc.SetDumpFn(func(ctx context.Context) (io.ReadCloser, error) {
		<-released
		return &fakeDump{data: []byte("x")}, nil
	})

	firstResult := make(chan error, 1)
	go func() {
		_, err := svc.Create(ctx, TriggerManual)
		firstResult <- err
	}()

	// Give the first Create time to grab the inflight
	// lock and reach dumpFn. 200ms is generous; the
	// race-free way to do this would be a channel from
	// inside dumpFn, but the 200ms sleep is fine for a
	// unit test.
	time.Sleep(200 * time.Millisecond)

	// Second Create: must fail with ErrBackupInProgress.
	_, err := svc.Create(ctx, TriggerManual)
	if !errors.Is(err, ErrBackupInProgress) {
		t.Fatalf("second Create: want ErrBackupInProgress, got %v", err)
	}

	// Release the first; it should complete normally.
	close(released)
	select {
	case err := <-firstResult:
		if err != nil {
			t.Fatalf("first Create: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first Create did not complete within 2s of release")
	}
}

func TestServiceCreateFailure(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, []byte("data"))
	svc.SetDumpFn(func(ctx context.Context) (io.ReadCloser, error) {
		return nil, errors.New("simulated pg_dump failure")
	})
	row, err := svc.Create(ctx, TriggerManual)
	if err == nil {
		t.Fatal("Create: expected error, got nil")
	}
	if row == nil {
		t.Fatal("Create: expected partial row, got nil")
	}
	if row.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", row.Status)
	}
	if row.Error == "" {
		t.Fatal("Error is empty")
	}
	// Row persists (so the operator can see the
	// failure in the UI).
	rows, _ := svc.List(ctx)
	if len(rows) != 1 {
		t.Fatalf("List: %d rows, want 1", len(rows))
	}
}

func TestServiceDeleteRemovesFile(t *testing.T) {
	ctx := context.Background()
	svc, dir := newTestService(t, []byte("data"))
	row, err := svc.Create(ctx, TriggerManual)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dumpPath := filepath.Join(dir, row.Path)
	if _, err := os.Stat(dumpPath); err != nil {
		t.Fatalf("dump file not on disk: %v", err)
	}
	if err := svc.Delete(ctx, row.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(dumpPath); !os.IsNotExist(err) {
		t.Fatalf("dump file still on disk: %v", err)
	}
	// Idempotent
	if err := svc.Delete(ctx, row.ID); err != nil {
		t.Fatalf("Delete (idempotent): %v", err)
	}
}

func TestServiceOpenStreamsBytes(t *testing.T) {
	ctx := context.Background()
	payload := []byte("the quick brown fox jumps over the lazy dog")
	svc, _ := newTestService(t, payload)
	row, err := svc.Create(ctx, TriggerManual)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	r, err := svc.Open(ctx, row.ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	// The Open stream is the raw .dump.gz, which
	// contains the gzip-compressed payload. We can
	// either check the bytes directly or gunzip them
	// — for this test we just confirm the stream
	// is non-empty and looks gzippy.
	if len(got) == 0 {
		t.Fatal("got empty stream")
	}
	// Magic bytes for gzip: 1f 8b
	if len(got) >= 2 && (got[0] != 0x1f || got[1] != 0x8b) {
		t.Fatalf("expected gzip magic, got %x", got[:2])
	}
}

func TestServiceCleanupRetentionAge(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, []byte("data"))
	// Override cfg to 1-day retention.
	svc.cfg.RetentionDays = 1
	// Create two rows:
	//   old   = 2026-07-29 12:00 (day 1)
	//   fresh = 2026-07-30 12:00 (day 2)
	// Cleanup runs at 2026-07-30 14:00 (2h after
	// fresh, comfortably inside the 1d window for
	// fresh but well outside it for old). Cutoff is
	// therefore 2026-07-29 14:00, so:
	//   - old   (07-29 12:00)  BEFORE cutoff → drop
	//   - fresh (07-30 12:00) !BEFORE cutoff → keep
	old, _ := svc.Create(ctx, TriggerManual) // day 1 (clock 07-29 12:00)
	svc.SetClock(func() time.Time { return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC) })
	fresh, _ := svc.Create(ctx, TriggerManual) // day 2
	if old.ID == fresh.ID {
		t.Fatal("expected different IDs for different times")
	}
	// Tick forward 2h for the explicit cleanup call.
	// Note: each Create() above already invoked an
	// internal Cleanup with its own cutoff; the manual
	// pass below is what actually drops `old`.
	svc.SetClock(func() time.Time { return time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC) })
	if err := svc.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	rows, _ := svc.List(ctx)
	if len(rows) != 1 {
		t.Fatalf("after cleanup: %d rows, want 1 (the fresh one)", len(rows))
	}
	if rows[0].ID != fresh.ID {
		t.Fatalf("kept %s, want %s", rows[0].ID, fresh.ID)
	}
}

func TestServiceCleanupRetentionCount(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, []byte("data"))
	svc.cfg.RetentionDays = 0 // disable age
	svc.cfg.MaxCount = 2
	// Create 4 rows by ticking the clock.
	for i := 0; i < 4; i++ {
		_, _ = svc.Create(ctx, TriggerManual)
		svc.SetClock(func() time.Time { return time.Date(2026, 7, 29, 12, i+1, 0, 0, time.UTC) })
	}
	// After 4 creates, the clock is at 12:04. The
	// most recent 2 should remain.
	rows, _ := svc.List(ctx)
	if len(rows) != 2 {
		t.Fatalf("after count cleanup: %d rows, want 2", len(rows))
	}
}

func TestServiceRestoreBlockedByDefault(t *testing.T) {
	svc, _ := newTestService(t, []byte("data"))
	row, _ := svc.Create(context.Background(), TriggerManual)
	// Default: AllowUIRestore=false
	if err := svc.Restore(context.Background(), row.ID); !errors.Is(err, ErrBackupDisabled) {
		t.Fatalf("Restore default: want ErrBackupDisabled, got %v", err)
	}
	// Opt in
	svc.cfg.AllowUIRestore = true
	if err := svc.Restore(context.Background(), row.ID); err == nil {
		t.Fatal("Restore (no pg_restore binary): expected error, got nil")
	}
}

func TestPathValidationInOSBackend(t *testing.T) {
	dir := t.TempDir()
	b, err := NewOSBackend(dir)
	if err != nil {
		t.Fatalf("NewOSBackend: %v", err)
	}
	ctx := context.Background()
	bad := []string{"", "..", "/etc/passwd", "../escape", "back\\slash", "foo/../../bar"}
	for _, p := range bad {
		t.Run(p, func(t *testing.T) {
			if _, err := b.Create(ctx, p); err == nil {
				t.Errorf("Create(%q): expected error, got nil", p)
			}
			if _, err := b.Open(ctx, p); err == nil {
				t.Errorf("Open(%q): expected error, got nil", p)
			}
		})
	}
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := hashFile(path)
	if err != nil {
		t.Fatalf("hashFile: %v", err)
	}
	want := hashHex([]byte("hello"))
	if got != want {
		t.Errorf("hashFile = %q, want %q", got, want)
	}
}

// Test that a path traversal can't escape the
// backups directory by using a relative "..".
func TestPathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	b, err := NewOSBackend(dir)
	if err != nil {
		t.Fatalf("NewOSBackend: %v", err)
	}
	ctx := context.Background()
	if _, _, err := b.Stat(ctx, "../../etc/passwd"); err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if err := b.Remove(ctx, ".."); err == nil {
		t.Fatal("expected error for path traversal on remove, got nil")
	}
}
