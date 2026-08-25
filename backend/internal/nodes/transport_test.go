// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Tests for the v0.8.31 per-node agent_transport column
// (migration 0024) and the `aegis admin node
// rotate-transport` plumbing.
//
// # Scope
//
// The file covers three layers:
//
//  1. MemoryStore: SetAgentTransport
//     (SetAgentTransport_OK, SetAgentTransport_NotFound,
//     SetAgentTransport_BumpsUpdatedAt) +
//     Create-defaults-to-http.
//
//  2. Service.RotateTransport:
//     RotateTransport_OK_HTTP_to_GRPC,
//     RotateTransport_OK_GRPC_to_HTTP (the operator
//     might want to roll back if the v0.8.30 mTLS
//     handshake fails in prod),
//     RotateTransport_RejectsUnknownValue,
//     RotateTransport_NotFound,
//     RotateTransport_NoOp_WhenAlreadyAtTarget.
//
//  3. Service.RotateTransport audit + webhook:
//     RotateTransport_RecordsAuditRow,
//     RotateTransport_DispatchesWebhook.
//
// The PgStore integration tests for the same column
// (SetAgentTransport, scan in nodeWithTagsSelect, write
// in insertNode/Update) live in pg_store_integration_test.go
// to keep the build tag consistent (`-tags=integration`).

package nodes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/webhooks"
)

func TestMemoryStore_Create_DefaultsAgentTransportToHTTP(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	// Fresh nodes with no AgentTransport set on the
	// input struct must end up with the v0.8.31
	// default ('http') after Create. The default
	// matches the SQL column DEFAULT (migration 0024)
	// so the in-memory and pg backends agree at the
	// moment of insertion.
	n := &Node{ID: uuid.New(), Name: "alpha", Region: "eu", Address: "10.0.0.1:22"}
	if err := s.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AgentTransport != AgentTransportHTTP {
		t.Fatalf("AgentTransport = %q, want %q (default)", got.AgentTransport, AgentTransportHTTP)
	}
}

func TestMemoryStore_Create_RespectsExplicitAgentTransport(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	// A caller that explicitly sets the field keeps
	// it. The defaulting logic only fires on the
	// empty-string sentinel.
	n := &Node{
		ID:             uuid.New(),
		Name:           "alpha",
		Region:         "eu",
		Address:        "10.0.0.1:22",
		AgentTransport: AgentTransportGRPC,
	}
	if err := s.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := s.GetByID(ctx, n.ID)
	if got.AgentTransport != AgentTransportGRPC {
		t.Fatalf("AgentTransport = %q, want %q (explicit)", got.AgentTransport, AgentTransportGRPC)
	}
}

func TestMemoryStore_SetAgentTransport_OK(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	n := &Node{ID: uuid.New(), Name: "alpha", Region: "eu", Address: "10.0.0.1:22"}
	if err := s.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Flip http -> grpc. The store trusts the
	// input; the Service.RotateTransport validator
	// is the upstream gate.
	if err := s.SetAgentTransport(ctx, n.ID, AgentTransportGRPC); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, _ := s.GetByID(ctx, n.ID)
	if got.AgentTransport != AgentTransportGRPC {
		t.Fatalf("AgentTransport = %q, want %q", got.AgentTransport, AgentTransportGRPC)
	}
}

func TestMemoryStore_SetAgentTransport_NotFound(t *testing.T) {
	s := NewMemoryStore()
	err := s.SetAgentTransport(context.Background(), uuid.New(), AgentTransportGRPC)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_SetAgentTransport_BumpsUpdatedAt(t *testing.T) {
	// v0.8.31 follow-the-pattern: the dedicated
	// single-column setters bump UpdatedAt so the
	// audit log's `after.updated_at` shows the
	// rotation time. Without the bump, the
	// operator's audit timeline would show
	// "node created at T" with no subsequent
	// touch, hiding the rotation.
	s := NewMemoryStore()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return t0 })
	ctx := context.Background()
	n := &Node{ID: uuid.New(), Name: "alpha", Region: "eu", Address: "10.0.0.1:22"}
	if err := s.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}

	t1 := t0.Add(time.Hour)
	s.SetClock(func() time.Time { return t1 })
	if err := s.SetAgentTransport(ctx, n.ID, AgentTransportGRPC); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, _ := s.GetByID(ctx, n.ID)
	if !got.UpdatedAt.Equal(t1) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, t1)
	}
	if !got.CreatedAt.Equal(t0) {
		t.Fatalf("CreatedAt should be preserved, got %v", got.CreatedAt)
	}
}

func TestService_RotateTransport_OK_HTTP_to_GRPC(t *testing.T) {
	svc, store := newSvc(t)
	ctx := context.Background()
	n, err := svc.Create(ctx, CreateInput{Name: "alpha", Region: "eu", Address: "10.0.0.1:22"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n.AgentTransport != AgentTransportHTTP {
		t.Fatalf("fresh node AgentTransport = %q, want %q", n.AgentTransport, AgentTransportHTTP)
	}

	updated, err := svc.RotateTransport(ctx, n.ID, AgentTransportGRPC)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if updated.AgentTransport != AgentTransportGRPC {
		t.Fatalf("updated AgentTransport = %q, want %q", updated.AgentTransport, AgentTransportGRPC)
	}
	// Round-trip via the store: the value survives
	// the read path. Catches a Service-only write
	// that forgot to flush.
	again, _ := store.GetByID(ctx, n.ID)
	if again.AgentTransport != AgentTransportGRPC {
		t.Fatalf("store AgentTransport = %q, want %q", again.AgentTransport, AgentTransportGRPC)
	}
}

func TestService_RotateTransport_OK_GRPC_to_HTTP(t *testing.T) {
	// Roll-back path: the operator may need to
	// revert a node to HTTP if the v0.8.30 mTLS
	// handshake fails in prod. The closed set is
	// {http, grpc}; the rotation is bi-directional.
	svc, _ := newSvc(t)
	ctx := context.Background()
	n, _ := svc.Create(ctx, CreateInput{Name: "alpha", Region: "eu", Address: "10.0.0.1:22"})

	if _, err := svc.RotateTransport(ctx, n.ID, AgentTransportGRPC); err != nil {
		t.Fatalf("first rotate: %v", err)
	}
	updated, err := svc.RotateTransport(ctx, n.ID, AgentTransportHTTP)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if updated.AgentTransport != AgentTransportHTTP {
		t.Fatalf("rollback AgentTransport = %q, want %q", updated.AgentTransport, AgentTransportHTTP)
	}
}

func TestService_RotateTransport_RejectsUnknownValue(t *testing.T) {
	// v0.8.31 closes the set to {http, grpc}. Any
	// other value must fail at the Service layer
	// with a ValidationError pointing at
	// `agent_transport`; the SQL CHECK is the
	// safety net but the Go-side rejection
	// surfaces a clearer error to the operator.
	svc, _ := newSvc(t)
	ctx := context.Background()
	n, _ := svc.Create(ctx, CreateInput{Name: "alpha", Region: "eu", Address: "10.0.0.1:22"})

	for _, bad := range []string{"", "dual", "HTTP", "GRPC", "ws", "grpc\n"} {
		_, err := svc.RotateTransport(ctx, n.ID, bad)
		if err == nil {
			t.Fatalf("RotateTransport(%q) = nil err, want ValidationError", bad)
		}
		var vErr *ValidationError
		if !errors.As(err, &vErr) {
			t.Fatalf("RotateTransport(%q) err = %v, want ValidationError", bad, err)
		}
		if vErr.Field != "agent_transport" {
			t.Fatalf("RotateTransport(%q) field = %q, want %q", bad, vErr.Field, "agent_transport")
		}
	}
}

func TestService_RotateTransport_NotFound(t *testing.T) {
	svc, _ := newSvc(t)
	_, err := svc.RotateTransport(context.Background(), uuid.New(), AgentTransportGRPC)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestService_RotateTransport_NoOp_WhenAlreadyAtTarget(t *testing.T) {
	// v0.8.31 idempotency: the CLI is runnable on
	// cron or as a remediation; a no-op rotation
	// must NOT bump UpdatedAt and must NOT emit an
	// audit row. The audit log would otherwise
	// fill with "rotated http -> http" entries on
	// every operator check-in.
	auditsStore := audits.NewMemoryStore()
	auditsSvc := audits.NewService(auditsStore)
	svc, _ := newSvc(t)
	svc.WithAudits(auditsSvc)
	ctx := context.Background()
	n, _ := svc.Create(ctx, CreateInput{Name: "alpha", Region: "eu", Address: "10.0.0.1:22"})

	// First call: http -> http is a no-op.
	updated, err := svc.RotateTransport(ctx, n.ID, AgentTransportHTTP)
	if err != nil {
		t.Fatalf("no-op rotate: %v", err)
	}
	if updated.AgentTransport != AgentTransportHTTP {
		t.Fatalf("AgentTransport = %q, want %q", updated.AgentTransport, AgentTransportHTTP)
	}
	pre, _ := auditsStore.List(ctx, audits.ListFilter{ResourceID: n.ID.String()})
	for _, e := range pre {
		// The `node.create` audit row is fine
		// (the Service.Create call wrote it).
		// The no-op rotate must NOT have
		// added a `node.transport.rotated` row.
		if e.Action == "node.transport.rotated" {
			t.Fatalf("no-op rotate wrote a transport.rotated row: %+v", e)
		}
	}

	// Real rotation: http -> grpc. One transport.rotated row.
	if _, err := svc.RotateTransport(ctx, n.ID, AgentTransportGRPC); err != nil {
		t.Fatalf("real rotate: %v", err)
	}
	post, _ := auditsStore.List(ctx, audits.ListFilter{ResourceID: n.ID.String(), Action: "node.transport.rotated"})
	if len(post) != 1 {
		t.Fatalf("real rotate transport.rotated rows = %d, want 1", len(post))
	}
	e := post[0]
	if e.Action != "node.transport.rotated" {
		t.Fatalf("audit action = %q, want %q", e.Action, "node.transport.rotated")
	}
	if e.ResourceType != "node" {
		t.Fatalf("audit resource_type = %q, want %q", e.ResourceType, "node")
	}
	if e.ResourceID != n.ID.String() {
		t.Fatalf("audit resource_id = %q, want %q", e.ResourceID, n.ID.String())
	}
	// The MemoryStore.List path elides Before /
	// After (saves bandwidth on the wire). The
	// canonical read for the full row is
	// GetByID, which is what the audit UI's
	// detail pane uses. The test follows the
	// same path.
	full, err := auditsStore.GetByID(ctx, e.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	after, ok := full.After.(map[string]any)
	if !ok {
		t.Fatalf("audit after = %T, want map[string]any", full.After)
	}
	if got, want := after["agent_transport"], AgentTransportGRPC; got != want {
		t.Fatalf("audit after.agent_transport = %v, want %v", got, want)
	}
	if got, want := after["agent_transport_prev"], AgentTransportHTTP; got != want {
		t.Fatalf("audit after.agent_transport_prev = %v, want %v", got, want)
	}
}

func TestService_RotateTransport_DispatchesWebhook(t *testing.T) {
	// v0.8.31: the rotate-transport flow fires a
	// webhooks.EventNodeUpdated event (the
	// existing event the rest of the panel uses
	// for "the node row changed" — adding a new
	// event would inflate the webhook contract
	// surface for a low-frequency change).
	spy := webhooks.NewSpy()
	epID := spy.Subscribe(t, webhooks.EventNodeUpdated)

	svc := NewService(NewMemoryStore()).WithWebhooks(spy.Svc())
	ctx := context.Background()
	n, err := svc.Create(ctx, CreateInput{
		Name:    "alpha",
		Region:  "eu",
		Address: "10.0.0.1:22",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.RotateTransport(ctx, n.ID, AgentTransportGRPC); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	spy.AssertDeliveredFor(t, epID, webhooks.EventNodeUpdated)
}
