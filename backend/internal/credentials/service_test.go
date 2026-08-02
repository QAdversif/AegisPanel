// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Service unit tests for the Phase 2 multi-user
// credentials data model.
//
// The Service layer adds input validation on top of
// the Store: it rejects zero UUIDs, empty values, and
// translates `pgx.ErrNoRows` to `ErrNotFound` (the
// MemoryStore does the in-memory equivalents; the
// Service's behaviour is identical across the two
// backends).

package credentials

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/audits"
)

func newService(t *testing.T) (*Service, *audits.MemoryStore) {
	t.Helper()
	auditsStore := audits.NewMemoryStore()
	auditsSvc := audits.NewService(auditsStore)
	svc := NewService(NewMemoryStore()).WithAudits(auditsSvc)
	return svc, auditsStore
}

func findAuditByAction(t *testing.T, s *audits.MemoryStore, action string) *audits.AuditEntry {
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

func TestService_Create_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newService(t)
	row, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "v1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	e := findAuditByAction(t, auditsStore, "credential.create")
	if e == nil {
		t.Fatal("expected an audit entry for credential.create, got none")
	}
	if e.ResourceType != "credential" {
		t.Errorf("ResourceType: got %q, want %q", e.ResourceType, "credential")
	}
	if e.ResourceID != row.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, row.ID.String())
	}
	if e.After == nil {
		t.Errorf("After: expected the new credential, got nil")
	}
}

func TestService_Create_RejectsZeroUserID(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	_, err := svc.Create(context.Background(), uuid.Nil, uuid.New(), "v")
	if err == nil {
		t.Fatal("Create: expected error, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("Create: got %v, want ValidationError", err)
	}
	if ve.Field != "user_id" {
		t.Errorf("Field: got %q, want %q", ve.Field, "user_id")
	}
}

func TestService_Create_RejectsZeroInboundID(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	_, err := svc.Create(context.Background(), uuid.New(), uuid.Nil, "v")
	if err == nil {
		t.Fatal("Create: expected error, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("Create: got %v, want ValidationError", err)
	}
	if ve.Field != "inbound_id" {
		t.Errorf("Field: got %q, want %q", ve.Field, "inbound_id")
	}
}

func TestService_Create_RejectsEmptyValue(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	_, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "")
	if err == nil {
		t.Fatal("Create: expected error, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("Create: got %v, want ValidationError", err)
	}
	if ve.Field != "credential_value" {
		t.Errorf("Field: got %q, want %q", ve.Field, "credential_value")
	}
}

func TestService_Create_RejectsDuplicate(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	userID := uuid.New()
	inbID := uuid.New()
	if _, err := svc.Create(context.Background(), userID, inbID, "first"); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := svc.Create(context.Background(), userID, inbID, "second")
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("second Create: got %v, want ErrDuplicate", err)
	}
}

func TestService_Get_RoundTrip(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	row, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "v")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := svc.Get(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != row.ID {
		t.Errorf("ID: got %s, want %s", got.ID, row.ID)
	}
}

func TestService_Get_NotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	_, err := svc.Get(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get: got %v, want ErrNotFound", err)
	}
}

func TestService_Get_RejectsZeroID(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	_, err := svc.Get(context.Background(), uuid.Nil)
	if err == nil {
		t.Fatal("Get: expected error, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("Get: got %v, want ValidationError", err)
	}
}

func TestService_Rotate_RecordsAuditBeforeAfter(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newService(t)
	row, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "v1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Rotate(context.Background(), row.ID, "v2"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	e := findAuditByAction(t, auditsStore, "credential.rotate")
	if e == nil {
		t.Fatal("expected an audit entry for credential.rotate, got none")
	}
	if e.ResourceID != row.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, row.ID.String())
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-rotate credential, got nil")
	}
	if e.After == nil {
		t.Errorf("After: expected the post-rotate credential, got nil")
	}
}

func TestService_Rotate_NotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	_, err := svc.Rotate(context.Background(), uuid.New(), "v")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Rotate: got %v, want ErrNotFound", err)
	}
}

func TestService_Rotate_RejectsEmptyValue(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	row, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "v1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = svc.Rotate(context.Background(), row.ID, "")
	if err == nil {
		t.Fatal("Rotate: expected error, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("Rotate: got %v, want ValidationError", err)
	}
}

func TestService_Delete_RecordsAuditBefore(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newService(t)
	row, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "v1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(context.Background(), row.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	e := findAuditByAction(t, auditsStore, "credential.delete")
	if e == nil {
		t.Fatal("expected an audit entry for credential.delete, got none")
	}
	if e.ResourceID != row.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, row.ID.String())
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-delete credential, got nil")
	}
	if e.After != nil {
		t.Errorf("After: expected nil for a Delete, got %T", e.After)
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	err := svc.Delete(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: got %v, want ErrNotFound", err)
	}
}

func TestService_WithoutAudits_NoRecord(t *testing.T) {
	t.Parallel()
	// Sanity: existing wiring pattern leaves audits
	// nil for unit tests. The RecordFromContext
	// call must be a no-op (and not panic on the
	// nil audits field).
	svc := NewService(NewMemoryStore())
	if _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "v"); err != nil {
		t.Fatalf("Create: %v", err)
	}
}
