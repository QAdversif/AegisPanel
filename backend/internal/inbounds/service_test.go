// SPDX-License-Identifier: AGPL-3.0-or-later

package inbounds

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// --- helpers ------------------------------------------------------------

// seedNodeSvc returns a *nodes.Service with a single
// pre-seeded node. Tests that need a second node call
// it again and pass the new id into CreateInput.
func seedNodeSvc(t *testing.T) (svc *nodes.Service, id uuid.UUID) {
	t.Helper()
	store := nodes.NewMemoryStore()
	svc = nodes.NewService(store)
	id = uuid.New()
	if _, err := svc.Create(context.Background(), nodes.CreateInput{
		ID:      id,
		Name:    "node-" + id.String()[:8],
		Region:  "eu",
		State:   nodes.StateOnline,
		Address: "1.2.3.4:22",
	}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return svc, id
}

// makeSvc returns an inbounds Service with the given
// nodes service.
func makeSvc(t *testing.T, nodesSvc *nodes.Service) *Service {
	t.Helper()
	return NewService(NewMemoryStore(), nodesSvc)
}

func validCreateInput(nodeID uuid.UUID) CreateInput {
	return CreateInput{
		NodeID:     nodeID,
		Name:       "vless-main",
		Protocol:   ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
		Tags:       []string{"production", "eu"},
		Params:     map[string]any{"uuid": "00000000-0000-0000-0000-000000000000"},
	}
}

// --- Create: happy paths ------------------------------------------------

func TestService_Create_Success(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	i, err := svc.Create(ctx, validCreateInput(nodeID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if i.ID == uuid.Nil {
		t.Error("ID should be assigned")
	}
	if i.Name != "vless-main" {
		t.Errorf("name = %q", i.Name)
	}
	if i.Listen != "::" {
		t.Errorf("listen = %q, want ::", i.Listen)
	}
	if !i.Enabled {
		t.Error("Enabled should default to true")
	}
	if i.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestService_Create_DefaultsListenToWildcard(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Listen = ""
	i, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if i.Listen != "::" {
		t.Errorf("listen = %q, want ::", i.Listen)
	}
}

// --- Create: validation failures ---------------------------------------

func TestService_Create_RejectsEmptyName(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Name = ""
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "name" {
		t.Errorf("Field = %q, want name", vErr.Field)
	}
}

func TestService_Create_RejectsNameWithSpaces(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Name = "vless main"
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "name" {
		t.Errorf("Field = %q, want name", vErr.Field)
	}
}

func TestService_Create_RejectsUnknownNode(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	in := validCreateInput(uuid.New()) // not the seeded node
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "node_id" {
		t.Errorf("Field = %q, want node_id", vErr.Field)
	}
}

func TestService_Create_RejectsUnknownProtocol(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Protocol = Protocol("wireguard")
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "protocol" {
		t.Errorf("Field = %q, want protocol", vErr.Field)
	}
}

func TestService_Create_RejectsPortOutOfRange(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.ListenPort = 0
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "listen_port" {
		t.Errorf("Field = %q, want listen_port", vErr.Field)
	}
}

func TestService_Create_RejectsBadListen(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Listen = "not\ta valid listen"
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "listen" {
		t.Errorf("Field = %q, want listen", vErr.Field)
	}
}

func TestService_Create_AcceptsIPv4Wildcard(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Listen = "0.0.0.0"
	_, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
}

func TestService_Create_AcceptsHostname(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Listen = "vless.example.com"
	_, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
}

func TestService_Create_RejectsDuplicateNameOnSameNode(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	if _, err := svc.Create(ctx, validCreateInput(nodeID)); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Same name, different port — still a duplicate.
	in := validCreateInput(nodeID)
	in.ListenPort = 8443
	_, err := svc.Create(ctx, in)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestService_Create_RejectsDuplicatePortOnSameNode(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	if _, err := svc.Create(ctx, validCreateInput(nodeID)); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Same port, different name — still a duplicate.
	in := validCreateInput(nodeID)
	in.Name = "hy2-fallback"
	in.Protocol = ProtocolHysteria2
	_, err := svc.Create(ctx, in)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestService_Create_AllowsSameNameAcrossNodes(t *testing.T) {
	nodesSvc, nodeA := seedNodeSvc(t)
	nodeB := uuid.New()
	if _, err := nodesSvc.Create(context.Background(), nodes.CreateInput{
		ID:      nodeB,
		Name:    "node-b",
		Region:  "eu",
		State:   nodes.StateOnline,
		Address: "5.6.7.8:22",
	}); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	if _, err := svc.Create(ctx, validCreateInput(nodeA)); err != nil {
		t.Fatalf("a: %v", err)
	}
	if _, err := svc.Create(ctx, validCreateInput(nodeB)); err != nil {
		t.Fatalf("b: %v", err)
	}
}

func TestService_Update_PartialFields(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	i, err := svc.Create(ctx, validCreateInput(nodeID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	enabled := false
	port := 2053
	upd, err := svc.Update(ctx, i.ID, UpdateInput{
		Enabled:    &enabled,
		ListenPort: &port,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Enabled {
		t.Error("Enabled should be false")
	}
	if upd.ListenPort != 2053 {
		t.Errorf("ListenPort = %d, want 2053", upd.ListenPort)
	}
	if upd.Name != i.Name {
		t.Errorf("Name should be unchanged: was %q, now %q", i.Name, upd.Name)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	_, err := svc.Update(context.Background(), uuid.New(), UpdateInput{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestService_Update_PortCollisionRejected(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	a, err := svc.Create(ctx, validCreateInput(nodeID))
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	in := validCreateInput(nodeID)
	in.Name = "b"
	in.ListenPort = 8443
	b, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	// Try to move b onto a's port.
	port := 443
	_, err = svc.Update(ctx, b.ID, UpdateInput{ListenPort: &port})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
	// a must still be the row at 443.
	got, err := svc.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("get a: %v", err)
	}
	if got.ListenPort != 443 {
		t.Errorf("a.ListenPort = %d, want 443 (unchanged)", got.ListenPort)
	}
}

// --- Delete -------------------------------------------------------------

func TestService_Delete_Success(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	i, err := svc.Create(ctx, validCreateInput(nodeID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Delete(ctx, i.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(ctx, i.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: err = %v, want ErrNotFound", err)
	}
}

func TestService_Delete_ZeroID_Rejected(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	err := svc.Delete(context.Background(), uuid.Nil)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	err := svc.Delete(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// --- List ---------------------------------------------------------------

func TestService_ListByNode_RejectsZeroNodeID(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	_, err := svc.ListByNode(context.Background(), uuid.Nil)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
}

func TestService_ListByProtocol_RejectsUnknown(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	_, err := svc.ListByProtocol(context.Background(), Protocol("wireguard"))
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "protocol" {
		t.Errorf("Field = %q, want protocol", vErr.Field)
	}
}

// --- SetClock propagates -----------------------------------------------

func TestService_SetClock_PropagatesToStore(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	fixed := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return fixed })
	i, err := svc.Create(context.Background(), validCreateInput(nodeID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !i.CreatedAt.Equal(fixed) {
		t.Errorf("CreatedAt = %s, want %s", i.CreatedAt, fixed)
	}
}

// --- Tag normalisation --------------------------------------------------

func TestService_Create_NormalisesTags(t *testing.T) {
	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Tags = []string{"  production  ", "production", "eu", "", "  "}
	i, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	want := []string{"production", "eu"}
	if len(i.Tags) != len(want) {
		t.Fatalf("tags = %v, want %v", i.Tags, want)
	}
	for k, v := range want {
		if i.Tags[k] != v {
			t.Errorf("tag[%d] = %q, want %q", k, i.Tags[k], v)
		}
	}
}
