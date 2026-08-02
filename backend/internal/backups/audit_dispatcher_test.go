// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Audit-dispatcher tests for the v0.7.x deferred
// call-site. Same pattern as
// internal/users/audit_dispatcher_test.go.
//
// Backups have a different audit shape than
// CRUD services: the v0.7.x dispatcher fires
// three events per Create invocation:
//
//   - `backup.create` when the running row is
//     first inserted (operator-initiated);
//   - `backup.complete` at the terminal OK
//     transition (system-driven; the row is
//     the OK-state row);
//   - `backup.fail` at the terminal Failed
//     transition (system-driven; the row is
//     the failed-state row with the error set).
//
// The Delete path fires `backup.delete` with
// the pre-delete row as the Before.
//
// The test uses a fake dumpFn (a no-op ReadCloser)
// so the pg_dump subprocess is never invoked;
// the LocalStore handles the index / row
// persistence.

package backups

import (
	"context"
	"io"
	"testing"

	"github.com/QAdversif/AegisPanel/internal/audits"
)

func newAuditedSvc(t *testing.T) (*Service, *audits.MemoryStore) {
	t.Helper()
	auditsStore := audits.NewMemoryStore()
	auditsSvc := audits.NewService(auditsStore)
	// NewOSBackend roots the LocalStore at
	// a real directory; t.TempDir() gives us
	// a per-test scratch dir that the OS
	// cleans up automatically.
	dir := t.TempDir()
	be, err := NewOSBackend(dir)
	if err != nil {
		t.Fatalf("NewOSBackend: %v", err)
	}
	// Pool is nil (no metadata counts; tests
	// tolerate the zero values).
	svc := New(Config{BackupsDir: dir}, NewLocalStore(be), nil)
	svc.WithAudits(auditsSvc)
	return svc, auditsStore
}

func findByAction(t *testing.T, s *audits.MemoryStore, action string) *audits.AuditEntry {
	t.Helper()
	entries, err := s.List(context.Background(), audits.ListFilter{
		Action: action,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("audits.List: %v", err)
	}
	if len(entries) == 0 {
		return nil
	}
	full, err := s.GetByID(context.Background(), entries[0].ID)
	if err != nil {
		t.Fatalf("audits.GetByID: %v", err)
	}
	return full
}

func TestService_Create_RecordsAuditCreateAndComplete(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newAuditedSvc(t)
	// Replace the real pg_dump subprocess with
	// a no-op that returns an empty stream. The
	// LocalStore's row insertion / Update paths
	// run normally; the empty file yields a 0-byte
	// dump (size 0, hash e3b0...).
	svc.SetDumpFn(func(_ context.Context) (io.ReadCloser, error) {
		return io.NopCloser(emptyStream{}), nil
	})
	row, err := svc.Create(context.Background(), TriggerManual)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// `backup.create` should be recorded with
	// the running row.
	createEntry := findByAction(t, auditsStore, "backup.create")
	if createEntry == nil {
		t.Fatal("expected an audit entry for backup.create, got none")
	}
	if createEntry.ResourceID != row.ID {
		t.Errorf("ResourceID: got %q, want %q", createEntry.ResourceID, row.ID)
	}
	if createEntry.After == nil {
		t.Errorf("After: expected the row, got nil")
	}

	// `backup.complete` should be recorded with
	// the OK row (the row's status was updated
	// to StatusOK in Create before the function
	// returned).
	completeEntry := findByAction(t, auditsStore, "backup.complete")
	if completeEntry == nil {
		t.Fatal("expected an audit entry for backup.complete, got none")
	}
	if completeEntry.ResourceID != row.ID {
		t.Errorf("ResourceID: got %q, want %q", completeEntry.ResourceID, row.ID)
	}
}

func TestService_Delete_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newAuditedSvc(t)
	svc.SetDumpFn(func(_ context.Context) (io.ReadCloser, error) {
		return io.NopCloser(emptyStream{}), nil
	})
	row, err := svc.Create(context.Background(), TriggerManual)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(context.Background(), row.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	e := findByAction(t, auditsStore, "backup.delete")
	if e == nil {
		t.Fatal("expected an audit entry for backup.delete, got none")
	}
	if e.ResourceID != row.ID {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, row.ID)
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-delete row, got nil")
	}
	if e.After != nil {
		t.Errorf("After: expected nil for a Delete, got %T", e.After)
	}
}

func TestService_WithoutAudits_NoRecord(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, err := NewOSBackend(dir)
	if err != nil {
		t.Fatalf("NewOSBackend: %v", err)
	}
	svc := New(Config{BackupsDir: dir}, NewLocalStore(be), nil)
	svc.SetDumpFn(func(_ context.Context) (io.ReadCloser, error) {
		return io.NopCloser(emptyStream{}), nil
	})
	if _, err := svc.Create(context.Background(), TriggerManual); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// emptyStream is a ReadCloser that returns
// io.EOF on the first read. Used to feed the
// backups.Service's "fake" dump path so the
// test does not need a real pg_dump
// subprocess.
type emptyStream struct{}

func (emptyStream) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func (emptyStream) Close() error {
	return nil
}
