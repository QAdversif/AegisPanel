// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Audit-dispatcher tests for the v0.7.x deferred
// call-site. The pattern mirrors the existing
// `dispatcher_test.go` (webhooks): a real
// `*audits.Service` backed by a MemoryStore, then
// the user Service wired via WithAudits. The test
// reads the MemoryStore after the mutation to
// verify the right Action / ResourceType /
// ResourceID was recorded with the right Before /
// After.
//
// Why no "spy" helper like webhooks.Spy: the
// audit log is the source of truth (a real
// in-memory table), not a fan-out surface. The
// MemoryStore's List returns the metadata but
// elides Before / After (the list path is
// bandwidth-constrained; the /{id} path
// returns the full row). Tests use GetByID to
// fetch the full row for Before / After asserts.

package users

import (
	"context"
	"testing"

	"github.com/QAdversif/AegisPanel/internal/audits"
)

// newAuditedSvc wires a users.Service with a
// real audits.Service backed by a MemoryStore.
// Tests use the returned MemoryStore to read
// back the entries the Service wrote.
func newAuditedSvc(t *testing.T) (*Service, *audits.MemoryStore) {
	t.Helper()
	auditsStore := audits.NewMemoryStore()
	auditsSvc := audits.NewService(auditsStore)
	svc := NewService(NewMemoryStore(nil)).WithAudits(auditsSvc)
	return svc, auditsStore
}

// findByAction returns the newest entry with the
// given action, or nil if none exist. The full
// row is fetched via GetByID so the Before / After
// fields are populated (List elides them).
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

// countByAction returns the number of entries
// with the given action.
func countByAction(t *testing.T, s *audits.MemoryStore, action string) int {
	t.Helper()
	entries, err := s.List(context.Background(), audits.ListFilter{
		Action: action,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("audits.List: %v", err)
	}
	return len(entries)
}

func TestService_Create_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newAuditedSvc(t)
	u, err := svc.Create(context.Background(), CreateInput{
		Username: "audit-create",
		Email:    "audit-create@example.com",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	e := findByAction(t, auditsStore, "user.create")
	if e == nil {
		t.Fatal("expected an audit entry for user.create, got none")
	}
	if e.ResourceType != "user" {
		t.Errorf("ResourceType: got %q, want %q", e.ResourceType, "user")
	}
	if e.ResourceID != u.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, u.ID.String())
	}
	if e.Before != nil {
		t.Errorf("Before: expected nil for a Create, got %T", e.Before)
	}
	if e.After == nil {
		t.Errorf("After: expected the new user, got nil")
	}
}

func TestService_Update_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newAuditedSvc(t)
	u, err := svc.Create(context.Background(), CreateInput{
		Username: "audit-update",
		Email:    "before@example.com",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	beforeCount := countByAction(t, auditsStore, "user.update")
	status := StatusDisabled
	if _, err := svc.Update(context.Background(), u.ID, UpdateInput{
		Status: &status,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := countByAction(t, auditsStore, "user.update"); got != beforeCount+1 {
		t.Fatalf("expected %d user.update entries, got %d", beforeCount+1, got)
	}
	e := findByAction(t, auditsStore, "user.update")
	if e == nil {
		t.Fatal("expected an audit entry for user.update, got none")
	}
	if e.ResourceID != u.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, u.ID.String())
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-update user, got nil")
	}
	if e.After == nil {
		t.Errorf("After: expected the post-update user, got nil")
	}
}

func TestService_Delete_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newAuditedSvc(t)
	u, err := svc.Create(context.Background(), CreateInput{
		Username: "audit-delete",
		Email:    "delete@example.com",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(context.Background(), u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	e := findByAction(t, auditsStore, "user.delete")
	if e == nil {
		t.Fatal("expected an audit entry for user.delete, got none")
	}
	if e.ResourceID != u.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, u.ID.String())
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-delete user, got nil")
	}
	if e.After != nil {
		t.Errorf("After: expected nil for a Delete, got %T", e.After)
	}
}

func TestService_RotateSubToken_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newAuditedSvc(t)
	u, err := svc.Create(context.Background(), CreateInput{
		Username: "audit-rotate",
		Email:    "rotate@example.com",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.RotateSubToken(context.Background(), u.ID, 0); err != nil {
		t.Fatalf("RotateSubToken: %v", err)
	}
	e := findByAction(t, auditsStore, "user.rotate_token")
	if e == nil {
		t.Fatal("expected an audit entry for user.rotate_token, got none")
	}
	if e.ResourceID != u.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, u.ID.String())
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-rotation user, got nil")
	}
	if e.After == nil {
		t.Errorf("After: expected the post-rotation user, got nil")
	}
}

func TestService_WithoutAudits_NoRecord(t *testing.T) {
	t.Parallel()
	// Sanity: existing unit tests construct
	// NewService without WithAudits. The
	// RecordFromContext call must be a no-op
	// (and not panic on the nil audits field).
	svc := NewService(NewMemoryStore(nil))
	if _, err := svc.Create(context.Background(), CreateInput{
		Username: "no-audit",
		Email:    "nope@example.com",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}
