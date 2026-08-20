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
//
// closeErr, when non-nil, is returned by Close().
// Pre-PR-#228 this field did not exist; the
// regression test TestServiceCreateFailureOnCloseError
// was added in PR #228 to lock in the fix for the
// "status=ok with empty dump" bug where a
// subprocess that exited non-zero was reported as
// success because its Close error was discarded
// by `defer closeQuiet(src)`.
type fakeDump struct {
	data     []byte
	closed   atomic.Bool
	readErr  error
	closeErr error
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
	return f.closeErr
}

// fakeDumper is a test Dumper that returns a fixed
// ReadCloser. The returned stream can be configured
// to surface a Close error (PR #228 regression
// coverage) or a Read error.
type fakeDumper struct {
	stream io.ReadCloser
}

func (f *fakeDumper) Dump(_ context.Context, _ string) (io.ReadCloser, error) {
	return f.stream, nil
}

func newTestService(t *testing.T, dumpBytes []byte) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	bk, err := NewOSBackend(dir)
	if err != nil {
		t.Fatalf("NewOSBackend: %v", err)
	}
	store := NewLocalStore(bk)
	// Use a non-existent binary path for the
	// default dump/restore configs. `/bin/true`
	// is the obvious placeholder but it is a
	// real binary on Linux that exits 0
	// (silently swallowing every arg), which
	// would make Restore() succeed where the
	// test expects an error. A path that does
	// not exist on any supported runtime gives
	// a portable "exec: not found" failure.
	svc := New(Config{
		BackupsDir:    dir,
		PgDumpBin:     "/dev/null/aegis-no-such-pg_dump-for-tests",
		PgRestoreBin:  "/dev/null/aegis-no-such-pg_restore-for-tests",
		RetentionDays: 30,
		MaxCount:      10,
	}, store, nil)
	svc.SetClock(func() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) })
	svc.SetDumper(&fakeDumper{stream: &fakeDump{data: dumpBytes}})
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

	// Inject a Dumper whose stream blocks until
	// `released`. The first Create to acquire the
	// inflight lock will block inside Dump; the
	// second Create arriving before release hits
	// TryLock and returns ErrBackupInProgress.
	released := make(chan struct{})
	svc.SetDumper(&fakeDumper{stream: &blockingDump{release: released}})

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
	// Pre-PR-##228 this injected a function that
	// returned (nil, err). With the Dumper
	// interface the same effect is "Dumper returns
	// an error" — Service.Create sees it at
	// runDumpToFile:675 and marks the row failed.
	svc.SetDumper(&fakeDumper{stream: nil})
	// We need the fakeDumper to actually return an
	// error from Dump, not just a nil stream.
	// Replace with an explicit error-returning
	// Dumper.
	svc.SetDumper(errDumper{err: errors.New("simulated pg_dump failure")})
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

// errDumper returns a fixed error from Dump. Used
// to simulate the "pg_dump subprocess failed to
// even start" path.
type errDumper struct{ err error }

func (e errDumper) Dump(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, e.err
}

// TestServiceCreateFailureOnCloseError is the
// regression test for the v0.8.15/0.8.16/0.8.17
// silent-failure bug:
//
//   - The dump subprocess (real pg_dump) exits
//     non-zero — e.g. because the DSN was stripped
//     to a bare db name and pg_dump could not
//     find a postgres server on the local socket.
//   - The subprocess's stdout pipe is closed (the
//     kernel signals EOF to readers) and the
//     stderr text is captured.
//   - io.Copy on the pipe returns nil (EOF is
//     not an error).
//   - The Service's runDumpToFile MUST then
//     check src.Close() — which waits for the
//     subprocess and surfaces the exit-code
//     error — and propagate it as a hard failure.
//
// Pre-PR-#228 the Service used `defer closeQuiet(src)`
// which discarded the Close error, so the file
// on disk was a 23-byte empty gzip and the row
// was marked status=ok. The CI smoke test caught
// this in production for v0.8.17; this test
// locks in the fix.
func TestServiceCreateFailureOnCloseError(t *testing.T) {
	ctx := context.Background()
	svc, dir := newTestService(t, []byte("data"))

	closeErr := errors.New("pg_dump: exit status 1 (stderr: connection to server failed)")
	svc.SetDumper(&fakeDumper{stream: &fakeDump{
		data:     []byte("data"),
		closeErr: closeErr,
	}})

	row, err := svc.Create(ctx, TriggerManual)
	if err == nil {
		t.Fatal("Create: expected error from Close failure, got nil")
	}
	if row == nil {
		t.Fatal("Create: expected partial row, got nil")
	}
	if row.Status != StatusFailed {
		t.Fatalf("status = %q, want failed (regression: empty dump must not be status=ok)", row.Status)
	}
	// Partial file must be removed — the operator
	// should never see a 23-byte "successful"
	// backup on disk.
	rows, _ := svc.List(ctx)
	if len(rows) != 1 {
		t.Fatalf("List: %d rows, want 1", len(rows))
	}
	dumpPath := filepath.Join(dir, rows[0].Path)
	if _, statErr := os.Stat(dumpPath); !os.IsNotExist(statErr) {
		t.Fatalf("dump file should be removed on Close error, but stat returned %v", statErr)
	}
}

// blockingDump is a ReadCloser that blocks every
// Read until `release` is closed. Used by
// TestServiceCreateSingleFlight to hold the
// in-flight lock so a concurrent Create can prove
// the ErrBackupInProgress path. Close is a no-op
// (the test doesn't check the result).
type blockingDump struct{ release <-chan struct{} }

func (b *blockingDump) Read(_ []byte) (int, error) {
	<-b.release
	return 0, io.EOF
}
func (b *blockingDump) Close() error { return nil }

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

// TestService_ReloadCron_UpdatesScheduler locks in:
//   - ReloadCron creates a scheduler struct on the
//     Service if one did not exist (cold-start path;
//     the operator boots the panel, hits the UI,
//     and clicks "reload" before the scheduler
//     goroutine has ever run).
//   - The new cron expression is what the scheduler
//     matches against (verified via the cron.matches()
//     probe, which is the same code path the running
//     goroutine uses).
//   - The expression is stored on the scheduler so
//     the admin UI can render the current string.
//
// Pre-PR-Task-3 this method did not exist; the
// scheduler was local to Run() and the operator's
// only way to change the schedule was to edit
// AEGIS_BACKUPS_CRON in the env file and restart
// the panel.
func TestService_ReloadCron_UpdatesScheduler(t *testing.T) {
	svc, _ := newTestService(t, []byte("data"))
	// Reload to "*/15 * * * *" — fires on every
	// 15-minute boundary (00, 15, 30, 45 of every
	// hour, every day). Five fields, not the
	// single-field "*/15" (which the parser would
	// correctly reject as not-5-fields).
	if err := svc.ReloadCron(context.Background(), "*/15 * * * *"); err != nil {
		t.Fatalf("ReloadCron: %v", err)
	}
	if svc.sched == nil {
		t.Fatal("expected scheduler to be initialized after ReloadCron")
	}
	if svc.sched.cron == nil {
		t.Fatal("expected scheduler.cron to be set after ReloadCron")
	}
	if svc.sched.expr != "*/15 * * * *" {
		t.Errorf("scheduler.expr = %q, want %q", svc.sched.expr, "*/15 * * * *")
	}
	// A 15-minute boundary must match the new cron.
	t15 := time.Date(2026, 3, 15, 2, 15, 0, 0, time.UTC)
	if !svc.sched.cron.matches(t15) {
		t.Errorf("expected cron to match :15, got no match")
	}
	// A minute that is NOT on a 15-minute boundary
	// must NOT match — this is what differentiates
	// "*/15 * * * *" from the original "0 2 * * *".
	t07 := time.Date(2026, 3, 15, 2, 7, 0, 0, time.UTC)
	if svc.sched.cron.matches(t07) {
		t.Errorf("cron unexpectedly matched :07 (*/15 * * * * should not fire at :07)")
	}
	// A subsequent ReloadCron with a different valid
	// expression replaces the cron in place.
	if err := svc.ReloadCron(context.Background(), "0 2 * * *"); err != nil {
		t.Fatalf("ReloadCron (second call): %v", err)
	}
	if svc.sched.expr != "0 2 * * *" {
		t.Errorf("scheduler.expr after second reload = %q, want %q", svc.sched.expr, "0 2 * * *")
	}
	if !svc.sched.cron.matches(time.Date(2026, 3, 15, 2, 0, 0, 0, time.UTC)) {
		t.Error("expected 02:00 to match after reload to 0 2 * * *")
	}
}

// TestService_ReloadCron_InvalidExpression locks in:
//   - An invalid cron expression is rejected with a
//     parse error (no silent swallow).
//   - The previous cron (if any) remains in place on
//     parse failure — the operator's running schedule
//     is not disturbed by a typo.
func TestService_ReloadCron_InvalidExpression(t *testing.T) {
	svc, _ := newTestService(t, []byte("data"))
	// Seed a valid cron first so we can prove the
	// invalid one does NOT replace it.
	if err := svc.ReloadCron(context.Background(), "0 2 * * *"); err != nil {
		t.Fatalf("seed ReloadCron: %v", err)
	}
	originalCron := svc.sched.cron
	err := svc.ReloadCron(context.Background(), "not a cron")
	if err == nil {
		t.Fatal("expected error for invalid cron expression, got nil")
	}
	// The original scheduler must still be in place.
	if svc.sched == nil || svc.sched.cron == nil {
		t.Fatal("expected original scheduler to remain after rejected reload")
	}
	// Pointer identity check — the parsed Cron struct
	// must be the exact same object, not a re-parse
	// of the bad input.
	if svc.sched.cron != originalCron {
		t.Error("scheduler.cron was replaced despite parse failure")
	}
	if svc.sched.expr != "0 2 * * *" {
		t.Errorf("scheduler.expr = %q after rejected reload, want %q (original preserved)", svc.sched.expr, "0 2 * * *")
	}
}
