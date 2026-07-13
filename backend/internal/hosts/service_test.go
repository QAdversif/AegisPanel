// SPDX-License-Identifier: AGPL-3.0-or-later

package hosts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// --- helpers ------------------------------------------------------------

// makeNodeSvc returns a *nodes.Service pre-seeded with the
// given node IDs. We need real nodes in the store so the
// host service's cross-entity validation has something to
// resolve against.
func makeNodeSvc(t *testing.T, nodeIDs ...uuid.UUID) *nodes.Service {
	t.Helper()
	store := nodes.NewMemoryStore()
	svc := nodes.NewService(store)
	ctx := context.Background()
	for _, id := range nodeIDs {
		_, err := svc.Create(ctx, nodes.CreateInput{
			ID:      id,
			Name:    "node-" + id.String()[:8],
			Region:  "eu",
			State:   nodes.StateOnline,
			Address: "1.2.3.4:22",
		})
		if err != nil {
			t.Fatalf("seed node %s: %v", id, err)
		}
	}
	return svc
}

func makeSvc(t *testing.T, nodeIDs ...uuid.UUID) *Service {
	t.Helper()
	return NewService(NewMemoryStore(), makeNodeSvc(t, nodeIDs...))
}

func validCreateInput(nodeID uuid.UUID) CreateInput {
	return CreateInput{
		Remark: "Latvia",
		Type:   HostTypeDirect,
		Endpoints: []Endpoint{
			{NodeID: nodeID, Protocol: "vless", Weight: 1},
		},
	}
}

func ptrFalse() *bool { b := false; return &b }

// --- Create: happy paths ------------------------------------------------

func TestService_Create_Direct_Success(t *testing.T) {
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	ctx := context.Background()

	h, err := svc.Create(ctx, validCreateInput(nodeID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.ID == uuid.Nil {
		t.Fatal("ID should be assigned")
	}
	if h.Remark != "Latvia" {
		t.Errorf("remark = %q", h.Remark)
	}
	if !h.Enabled {
		t.Errorf("Enabled should default to true")
	}
	if h.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be set")
	}
	if len(h.Endpoints) != 1 {
		t.Fatalf("endpoints = %d, want 1", len(h.Endpoints))
	}
	if h.Endpoints[0].ID == uuid.Nil {
		t.Errorf("Endpoint.ID should be assigned by service")
	}
	if h.Endpoints[0].Weight != 1 {
		t.Errorf("Endpoint.Weight = %d, want 1", h.Endpoints[0].Weight)
	}
}

func TestService_Create_Balancer_Success(t *testing.T) {
	nodeA := uuid.New()
	nodeB := uuid.New()
	svc := makeSvc(t, nodeA, nodeB)
	ctx := context.Background()

	in := CreateInput{
		Remark: "Premium EU",
		Type:   HostTypeBalancer,
		Endpoints: []Endpoint{
			{NodeID: nodeA, Protocol: "vless", Weight: 3},
			{NodeID: nodeB, Protocol: "hysteria2", Weight: 2},
		},
		Balancer: &Balancer{Strategy: StrategyRoundRobin},
	}
	h, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.Balancer == nil || h.Balancer.Strategy != StrategyRoundRobin {
		t.Errorf("balancer not set")
	}
}

func TestService_Create_AssignsPriorityAndEnabled(t *testing.T) {
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Enabled = ptrFalse()
	in.Priority = ptrInt(5)
	h, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.Enabled {
		t.Error("Enabled should be false")
	}
	if h.Priority != 5 {
		t.Errorf("Priority = %d, want 5", h.Priority)
	}
}

// --- Create: validation failures ---------------------------------------

func TestService_Create_RejectsEmptyRemark(t *testing.T) {
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Remark = "   "
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "remark" {
		t.Errorf("Field = %q, want remark", vErr.Field)
	}
}

func TestService_Create_RejectsUnknownType(t *testing.T) {
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Type = HostType("chain")
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "type" {
		t.Errorf("Field = %q, want type", vErr.Field)
	}
}

func TestService_Create_RejectsDirectWithMultipleEndpoints(t *testing.T) {
	nodeA := uuid.New()
	nodeB := uuid.New()
	svc := makeSvc(t, nodeA, nodeB)
	ctx := context.Background()

	in := CreateInput{
		Remark: "x",
		Type:   HostTypeDirect,
		Endpoints: []Endpoint{
			{NodeID: nodeA, Protocol: "vless", Weight: 1},
			{NodeID: nodeB, Protocol: "hysteria2", Weight: 1},
		},
	}
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "endpoints" {
		t.Errorf("Field = %q, want endpoints", vErr.Field)
	}
}

func TestService_Create_RejectsBalancerWithSingleEndpoint(t *testing.T) {
	nodeA := uuid.New()
	svc := makeSvc(t, nodeA)
	ctx := context.Background()

	in := CreateInput{
		Remark: "x",
		Type:   HostTypeBalancer,
		Endpoints: []Endpoint{
			{NodeID: nodeA, Protocol: "vless", Weight: 1},
		},
		Balancer: &Balancer{Strategy: StrategyRoundRobin},
	}
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "endpoints" {
		t.Errorf("Field = %q, want endpoints", vErr.Field)
	}
}

func TestService_Create_RejectsBalancerWithoutBalancer(t *testing.T) {
	nodeA := uuid.New()
	nodeB := uuid.New()
	svc := makeSvc(t, nodeA, nodeB)
	ctx := context.Background()

	in := CreateInput{
		Remark: "x",
		Type:   HostTypeBalancer,
		Endpoints: []Endpoint{
			{NodeID: nodeA, Protocol: "vless", Weight: 1},
			{NodeID: nodeB, Protocol: "hysteria2", Weight: 1},
		},
		// no Balancer
	}
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "balancer" {
		t.Errorf("Field = %q, want balancer", vErr.Field)
	}
}

func TestService_Create_RejectsDirectWithBalancer(t *testing.T) {
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Balancer = &Balancer{Strategy: StrategyRoundRobin}
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "balancer" {
		t.Errorf("Field = %q, want balancer", vErr.Field)
	}
}

func TestService_Create_RejectsEndpointWithUnknownNode(t *testing.T) {
	svc := makeSvc(t) // no nodes seeded
	ctx := context.Background()

	in := validCreateInput(uuid.New())
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "endpoints[].node_id" {
		t.Errorf("Field = %q, want endpoints[].node_id", vErr.Field)
	}
}

func TestService_Create_RejectsEndpointWithUnknownProtocol(t *testing.T) {
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Endpoints[0].Protocol = "wireguard"
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "endpoints[].protocol" {
		t.Errorf("Field = %q, want endpoints[].protocol", vErr.Field)
	}
}

func TestService_Create_RejectsNegativeWeight(t *testing.T) {
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Endpoints[0].Weight = -1
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "endpoints[].weight" {
		t.Errorf("Field = %q, want endpoints[].weight", vErr.Field)
	}
}

func TestService_Create_DefaultsZeroWeightToOne(t *testing.T) {
	// A weight of 0 in the create body is treated as
	// "use the default" (1) so the operator can omit
	// it on common single-protocol endpoints. A
	// negative weight is a real error.
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Endpoints[0].Weight = 0
	h, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.Endpoints[0].Weight != 1 {
		t.Errorf("weight = %d, want 1 (default)", h.Endpoints[0].Weight)
	}
}

func TestService_Create_RejectsUnknownStatusFilter(t *testing.T) {
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.StatusFilter = []UserStatus{UserStatus("paused")}
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "status_filter" {
		t.Errorf("Field = %q, want status_filter", vErr.Field)
	}
}

func TestService_Create_RejectsUnknownBalancerStrategy(t *testing.T) {
	nodeA := uuid.New()
	nodeB := uuid.New()
	svc := makeSvc(t, nodeA, nodeB)
	ctx := context.Background()

	in := CreateInput{
		Remark: "x",
		Type:   HostTypeBalancer,
		Endpoints: []Endpoint{
			{NodeID: nodeA, Protocol: "vless", Weight: 1},
			{NodeID: nodeB, Protocol: "hysteria2", Weight: 1},
		},
		Balancer: &Balancer{Strategy: BalancerStrategy("mystery")},
	}
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "balancer.strategy" {
		t.Errorf("Field = %q, want balancer.strategy", vErr.Field)
	}
}

func TestService_Create_RejectsInvalidHealthcheckURL(t *testing.T) {
	nodeA := uuid.New()
	nodeB := uuid.New()
	svc := makeSvc(t, nodeA, nodeB)
	ctx := context.Background()

	in := CreateInput{
		Remark: "x",
		Type:   HostTypeBalancer,
		Endpoints: []Endpoint{
			{NodeID: nodeA, Protocol: "vless", Weight: 1},
			{NodeID: nodeB, Protocol: "hysteria2", Weight: 1},
		},
		Balancer: &Balancer{
			Strategy:               StrategyRoundRobin,
			HealthcheckURL:         "ftp://example.com",
			HealthcheckIntervalSec: 30,
		},
	}
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
}

func TestService_Create_RejectsOutOfRangePriority(t *testing.T) {
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID)
	in.Priority = ptrInt(100000)
	_, err := svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "priority" {
		t.Errorf("Field = %q, want priority", vErr.Field)
	}
}

func TestService_Create_RejectsDuplicateRemark(t *testing.T) {
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	ctx := context.Background()

	if _, err := svc.Create(ctx, validCreateInput(nodeID)); err != nil {
		t.Fatalf("first: %v", err)
	}
	in := validCreateInput(nodeID)
	_, err := svc.Create(ctx, in)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

// --- Update -------------------------------------------------------------

func TestService_Update_PartialFields(t *testing.T) {
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	ctx := context.Background()

	h, err := svc.Create(ctx, validCreateInput(nodeID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	enabled := false
	displayName := "🇳🇱 Netherlands"
	upd, err := svc.Update(ctx, h.ID, UpdateInput{
		Enabled:     &enabled,
		DisplayName: &displayName,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Enabled {
		t.Errorf("Enabled should be false")
	}
	if upd.DisplayName != "🇳🇱 Netherlands" {
		t.Errorf("DisplayName = %q", upd.DisplayName)
	}
	if upd.Remark != h.Remark {
		t.Errorf("Remark should be unchanged: was %q, now %q", h.Remark, upd.Remark)
	}
}

func TestService_Update_TypeChangeEnforcesNewEndpointCount(t *testing.T) {
	nodeA := uuid.New()
	nodeB := uuid.New()
	svc := makeSvc(t, nodeA, nodeB)
	ctx := context.Background()

	// Start as direct.
	in := validCreateInput(nodeA)
	in.Endpoints[0].NodeID = nodeA
	h, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Change type to balancer — should fail because
	// only 1 endpoint.
	t2 := HostTypeBalancer
	_, err = svc.Update(ctx, h.ID, UpdateInput{Type: &t2})
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "endpoints" {
		t.Errorf("Field = %q, want endpoints", vErr.Field)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	svc := makeSvc(t)
	_, err := svc.Update(context.Background(), uuid.New(), UpdateInput{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// --- Delete -------------------------------------------------------------

func TestService_Delete_Success(t *testing.T) {
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	ctx := context.Background()

	h, err := svc.Create(ctx, validCreateInput(nodeID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Delete(ctx, h.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(ctx, h.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v, want ErrNotFound", err)
	}
}

func TestService_Delete_ZeroID_Rejected(t *testing.T) {
	svc := makeSvc(t)
	err := svc.Delete(context.Background(), uuid.Nil)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
}

// --- SetClock propagates -----------------------------------------------

func TestService_SetClock_PropagatesToStore(t *testing.T) {
	nodeID := uuid.New()
	svc := makeSvc(t, nodeID)
	fixed := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return fixed })
	h, err := svc.Create(context.Background(), validCreateInput(nodeID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !h.CreatedAt.Equal(fixed) {
		t.Errorf("CreatedAt = %s, want %s", h.CreatedAt, fixed)
	}
}
