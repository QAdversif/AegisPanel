// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Audit-dispatcher tests for the v0.7.x deferred
// call-site. Same pattern as
// internal/users/audit_dispatcher_test.go.

package nodes

import (
	"context"
	"testing"

	"github.com/QAdversif/AegisPanel/internal/audits"
)

func newAuditedSvc(t *testing.T) (*Service, *audits.MemoryStore) {
	t.Helper()
	auditsStore := audits.NewMemoryStore()
	auditsSvc := audits.NewService(auditsStore)
	svc := NewService(NewMemoryStore()).WithAudits(auditsSvc)
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

func TestService_Create_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newAuditedSvc(t)
	n, err := svc.Create(context.Background(), CreateInput{
		Name:    "audit-create-node",
		Region:  "eu-test",
		Address: "1.2.3.4:22",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	e := findByAction(t, auditsStore, "node.create")
	if e == nil {
		t.Fatal("expected an audit entry for node.create, got none")
	}
	if e.ResourceType != "node" {
		t.Errorf("ResourceType: got %q, want %q", e.ResourceType, "node")
	}
	if e.ResourceID != n.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, n.ID.String())
	}
	if e.After == nil {
		t.Errorf("After: expected the new node, got nil")
	}
}

func TestService_Update_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newAuditedSvc(t)
	n, err := svc.Create(context.Background(), CreateInput{
		Name:    "audit-update-node",
		Region:  "eu-test",
		Address: "1.2.3.4:22",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	state := StateOnline
	if _, err := svc.Update(context.Background(), n.ID, UpdateInput{
		State: &state,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	e := findByAction(t, auditsStore, "node.update")
	if e == nil {
		t.Fatal("expected an audit entry for node.update, got none")
	}
	if e.ResourceID != n.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, n.ID.String())
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-update node, got nil")
	}
	if e.After == nil {
		t.Errorf("After: expected the post-update node, got nil")
	}
}

func TestService_Delete_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newAuditedSvc(t)
	n, err := svc.Create(context.Background(), CreateInput{
		Name:    "audit-delete-node",
		Region:  "eu-test",
		Address: "1.2.3.4:22",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(context.Background(), n.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	e := findByAction(t, auditsStore, "node.delete")
	if e == nil {
		t.Fatal("expected an audit entry for node.delete, got none")
	}
	if e.ResourceID != n.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, n.ID.String())
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-delete node, got nil")
	}
	if e.After != nil {
		t.Errorf("After: expected nil for a Delete, got %T", e.After)
	}
}

func TestService_WithoutAudits_NoRecord(t *testing.T) {
	t.Parallel()
	svc := NewService(NewMemoryStore())
	if _, err := svc.Create(context.Background(), CreateInput{
		Name:    "no-audit-node",
		Region:  "eu-test",
		Address: "1.2.3.4:22",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}
