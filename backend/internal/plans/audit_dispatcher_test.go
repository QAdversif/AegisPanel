// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Audit-dispatcher tests for the v0.7.x deferred
// call-site. Same pattern as
// internal/users/audit_dispatcher_test.go.

package plans

import (
	"context"
	"testing"
	"time"

	"github.com/QAdversif/AegisPanel/internal/audits"
)

func newAuditedSvc(t *testing.T) (*Service, *audits.MemoryStore) {
	t.Helper()
	auditsStore := audits.NewMemoryStore()
	auditsSvc := audits.NewService(auditsStore)
	svc := NewService(NewMemoryStore(nil)).WithAudits(auditsSvc)
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
	p, err := svc.Create(context.Background(), CreateInput{
		Name:              "audit-create-plan",
		Duration:          30 * 24 * time.Hour,
		TrafficLimitBytes: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	e := findByAction(t, auditsStore, "plan.create")
	if e == nil {
		t.Fatal("expected an audit entry for plan.create, got none")
	}
	if e.ResourceType != "plan" {
		t.Errorf("ResourceType: got %q, want %q", e.ResourceType, "plan")
	}
	if e.ResourceID != p.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, p.ID.String())
	}
	if e.After == nil {
		t.Errorf("After: expected the new plan, got nil")
	}
}

func TestService_Update_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newAuditedSvc(t)
	p, err := svc.Create(context.Background(), CreateInput{
		Name:              "audit-update-plan",
		Duration:          30 * 24 * time.Hour,
		TrafficLimitBytes: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	newPrice := int64(200)
	if _, err := svc.Update(context.Background(), p.ID, UpdateInput{
		PriceCents: &newPrice,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	e := findByAction(t, auditsStore, "plan.update")
	if e == nil {
		t.Fatal("expected an audit entry for plan.update, got none")
	}
	if e.ResourceID != p.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, p.ID.String())
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-update plan, got nil")
	}
	if e.After == nil {
		t.Errorf("After: expected the post-update plan, got nil")
	}
}

func TestService_Delete_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc, auditsStore := newAuditedSvc(t)
	p, err := svc.Create(context.Background(), CreateInput{
		Name:              "audit-delete-plan",
		Duration:          30 * 24 * time.Hour,
		TrafficLimitBytes: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(context.Background(), p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	e := findByAction(t, auditsStore, "plan.delete")
	if e == nil {
		t.Fatal("expected an audit entry for plan.delete, got none")
	}
	if e.ResourceID != p.ID.String() {
		t.Errorf("ResourceID: got %q, want %q", e.ResourceID, p.ID.String())
	}
	if e.Before == nil {
		t.Errorf("Before: expected the pre-delete plan, got nil")
	}
	if e.After != nil {
		t.Errorf("After: expected nil for a Delete, got %T", e.After)
	}
}

func TestService_WithoutAudits_NoRecord(t *testing.T) {
	t.Parallel()
	svc := NewService(NewMemoryStore(nil))
	if _, err := svc.Create(context.Background(), CreateInput{
		Name:              "no-audit-plan",
		Duration:          30 * 24 * time.Hour,
		TrafficLimitBytes: 100,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}
