// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Audit-dispatcher tests for the v0.7.x deferred
// call-site. Same pattern as
// internal/users/audit_dispatcher_test.go.

package hosts

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/audits"

	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// newAuditedSvc wires a hosts.Service with a
// real audits.Service backed by a MemoryStore.
// The cross-entity validation in
// hosts.Service.normaliseEndpoints requires
// every Endpoint.NodeID to resolve to a real
// node, so we pre-seed a single node and an
// inbound per test setup. The audit hook is
// orthogonal to the cross-entity check, so the
// minimal node + inbound seed is fine.
func newAuditedSvc(t *testing.T) (*Service, *uuid.UUID, *uuid.UUID, *audits.MemoryStore) {
	t.Helper()
	auditsStore := audits.NewMemoryStore()
	auditsSvc := audits.NewService(auditsStore)

	nodesSvc := nodes.NewService(nodes.NewMemoryStore())
	inbSvc := inbounds.NewService(inbounds.NewMemoryStore(), nodesSvc)
	svc := NewService(NewMemoryStore(), nodesSvc, inbSvc).WithAudits(auditsSvc)

	n, err := nodesSvc.Create(context.Background(), nodes.CreateInput{
		Name:    "audit-host-node",
		Region:  "eu-test",
		Address: "1.2.3.4:22",
	})
	if err != nil {
		t.Fatalf("nodes.Create: %v", err)
	}
	i, err := inbSvc.Create(context.Background(), inbounds.CreateInput{
		NodeID:     n.ID,
		Name:       "audit-host-inbound",
		Protocol:   inbounds.ProtocolVLESS,
		ListenPort: 8443,
	})
	if err != nil {
		t.Fatalf("inbSvc.Create: %v", err)
	}
	return svc, &n.ID, &i.ID, auditsStore
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
	svc, nodeID, inbID, auditsStore := newAuditedSvc(t)
	enabled := true
	h, err := svc.Create(context.Background(), CreateInput{
		Remark:      "audit-create-host",
		DisplayName: "Audit Test Host",
		Type:        HostTypeDirect,
		Enabled:     &enabled,
		Endpoints: []Endpoint{{
			NodeID:    *nodeID,
			InboundID: *inbID,
			Address:   []string{"fallback"},
		}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	e := findByAction(t, auditsStore, "host.create")
	if e == nil {
		t.Fatal("expected an audit entry for host.create, got none")
	}
	if e.ResourceType != "host" {
		t.Errorf("ResourceType: got %q, want %q", e.ResourceType, "host")
	}
	if e.ResourceID != h.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, h.ID.String())
	}
	if e.After == nil {
		t.Errorf("After: expected the new host, got nil")
	}
}

func TestService_Update_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, nodeID, inbID, auditsStore := newAuditedSvc(t)
	enabled := true
	h, err := svc.Create(context.Background(), CreateInput{
		Remark:      "audit-update-host",
		DisplayName: "Audit Test Host",
		Type:        HostTypeDirect,
		Enabled:     &enabled,
		Endpoints: []Endpoint{{
			NodeID:    *nodeID,
			InboundID: *inbID,
			Address:   []string{"fallback"},
		}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	newRemark := "audit-update-host-renamed"
	if _, err := svc.Update(context.Background(), h.ID, UpdateInput{
		Remark: &newRemark,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	e := findByAction(t, auditsStore, "host.update")
	if e == nil {
		t.Fatal("expected an audit entry for host.update, got none")
	}
	if e.ResourceID != h.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, h.ID.String())
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-update host, got nil")
	}
	if e.After == nil {
		t.Errorf("After: expected the post-update host, got nil")
	}
}

func TestService_Delete_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, nodeID, inbID, auditsStore := newAuditedSvc(t)
	enabled := true
	h, err := svc.Create(context.Background(), CreateInput{
		Remark:      "audit-delete-host",
		DisplayName: "Audit Test Host",
		Type:        HostTypeDirect,
		Enabled:     &enabled,
		Endpoints: []Endpoint{{
			NodeID:    *nodeID,
			InboundID: *inbID,
			Address:   []string{"fallback"},
		}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(context.Background(), h.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	e := findByAction(t, auditsStore, "host.delete")
	if e == nil {
		t.Fatal("expected an audit entry for host.delete, got none")
	}
	if e.ResourceID != h.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, h.ID.String())
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-delete host, got nil")
	}
	if e.After != nil {
		t.Errorf("After: expected nil for a Delete, got %T", e.After)
	}
}

func TestService_WithoutAudits_NoRecord(t *testing.T) {
	t.Parallel()
	nodesSvc := nodes.NewService(nodes.NewMemoryStore())
	inbSvc := inbounds.NewService(inbounds.NewMemoryStore(), nodesSvc)
	svc := NewService(NewMemoryStore(), nodesSvc, inbSvc)
	n, err := nodesSvc.Create(context.Background(), nodes.CreateInput{
		Name:    "no-audit-host-node",
		Region:  "eu-test",
		Address: "1.2.3.4:22",
	})
	if err != nil {
		t.Fatalf("nodes.Create: %v", err)
	}
	i, err := inbSvc.Create(context.Background(), inbounds.CreateInput{
		NodeID:     n.ID,
		Name:       "no-audit-host-inbound",
		Protocol:   inbounds.ProtocolVLESS,
		ListenPort: 8443,
	})
	if err != nil {
		t.Fatalf("inbSvc.Create: %v", err)
	}
	enabled := true
	if _, err := svc.Create(context.Background(), CreateInput{
		Remark:      "no-audit-host",
		DisplayName: "No Audit",
		Type:        HostTypeDirect,
		Enabled:     &enabled,
		Endpoints: []Endpoint{{
			NodeID:    n.ID,
			InboundID: i.ID,
			Address:   []string{"fallback"},
		}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}
