// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Audit-dispatcher tests for the v0.7.x deferred
// call-site. Same pattern as
// internal/users/audit_dispatcher_test.go.

package inbounds

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// newAuditedSvc wires an inbounds.Service with
// a real audits.Service backed by a MemoryStore.
// The inbounds Service requires every inbound
// to reference a real node, so the nodes service
// is exposed for the test to seed a node before
// creating an inbound.
func newAuditedSvc(t *testing.T) (*Service, *nodes.Service, *audits.MemoryStore) {
	t.Helper()
	auditsStore := audits.NewMemoryStore()
	auditsSvc := audits.NewService(auditsStore)
	nodesSvc := nodes.NewService(nodes.NewMemoryStore())
	svc := NewService(NewMemoryStore(), nodesSvc).WithAudits(auditsSvc)
	return svc, nodesSvc, auditsStore
}

func seedNodeForAudit(t *testing.T, nodesSvc *nodes.Service) uuid.UUID {
	t.Helper()
	n, err := nodesSvc.Create(context.Background(), nodes.CreateInput{
		Name:    "audit-inbound-node",
		Region:  "eu-test",
		Address: "1.2.3.4:22",
	})
	if err != nil {
		t.Fatalf("nodes.Create: %v", err)
	}
	return n.ID
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
	svc, nodesSvc, auditsStore := newAuditedSvc(t)
	nid := seedNodeForAudit(t, nodesSvc)
	enabled := true
	i, err := svc.Create(context.Background(), CreateInput{
		NodeID:     nid,
		Name:       "audit-create-inbound",
		Protocol:   ProtocolVLESS,
		ListenPort: 8443,
		Enabled:    &enabled,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	e := findByAction(t, auditsStore, "inbound.create")
	if e == nil {
		t.Fatal("expected an audit entry for inbound.create, got none")
	}
	if e.ResourceType != "inbound" {
		t.Errorf("ResourceType: got %q, want %q", e.ResourceType, "inbound")
	}
	if e.ResourceID != i.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, i.ID.String())
	}
	if e.After == nil {
		t.Errorf("After: expected the new inbound, got nil")
	}
}

func TestService_Update_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, nodesSvc, auditsStore := newAuditedSvc(t)
	nid := seedNodeForAudit(t, nodesSvc)
	enabled := true
	i, err := svc.Create(context.Background(), CreateInput{
		NodeID:     nid,
		Name:       "audit-update-inbound",
		Protocol:   ProtocolVLESS,
		ListenPort: 8443,
		Enabled:    &enabled,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	newName := "audit-update-inbound-renamed"
	if _, err := svc.Update(context.Background(), i.ID, UpdateInput{
		Name: &newName,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	e := findByAction(t, auditsStore, "inbound.update")
	if e == nil {
		t.Fatal("expected an audit entry for inbound.update, got none")
	}
	if e.ResourceID != i.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, i.ID.String())
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-update inbound, got nil")
	}
	if e.After == nil {
		t.Errorf("After: expected the post-update inbound, got nil")
	}
}

func TestService_Delete_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, nodesSvc, auditsStore := newAuditedSvc(t)
	nid := seedNodeForAudit(t, nodesSvc)
	enabled := true
	i, err := svc.Create(context.Background(), CreateInput{
		NodeID:     nid,
		Name:       "audit-delete-inbound",
		Protocol:   ProtocolVLESS,
		ListenPort: 8443,
		Enabled:    &enabled,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(context.Background(), i.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	e := findByAction(t, auditsStore, "inbound.delete")
	if e == nil {
		t.Fatal("expected an audit entry for inbound.delete, got none")
	}
	if e.ResourceID != i.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, i.ID.String())
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-delete inbound, got nil")
	}
	if e.After != nil {
		t.Errorf("After: expected nil for a Delete, got %T", e.After)
	}
}

func TestService_WithoutAudits_NoRecord(t *testing.T) {
	t.Parallel()
	nodesSvc := nodes.NewService(nodes.NewMemoryStore())
	svc := NewService(NewMemoryStore(), nodesSvc)
	nid := seedNodeForAudit(t, nodesSvc)
	enabled := true
	if _, err := svc.Create(context.Background(), CreateInput{
		NodeID:     nid,
		Name:       "no-audit-inbound",
		Protocol:   ProtocolVLESS,
		ListenPort: 8443,
		Enabled:    &enabled,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}
