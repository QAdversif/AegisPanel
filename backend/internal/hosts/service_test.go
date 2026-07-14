// SPDX-License-Identifier: AGPL-3.0-or-later

package hosts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// --- helpers ------------------------------------------------------------

// testEnv wires a *Service with the given node IDs and
// one inbound per node. The map is the inbounds seeded
// for each node, indexed by node ID. Tests that need a
// second inbound on the same node (or that need a
// specific inbound name / port) seed it explicitly.
type testEnv struct {
	svc           *Service
	nodes         *nodes.Service
	inboundByNode map[uuid.UUID]uuid.UUID
}

func (e *testEnv) inboundFor(nodeID uuid.UUID) uuid.UUID {
	id, ok := e.inboundByNode[nodeID]
	if !ok {
		panic("no inbound seeded for node " + nodeID.String())
	}
	return id
}

// makeSvc seeds N nodes, one inbound per node, and
// returns a testEnv with the wired Service and the
// (node → inbound) lookup. The same in-memory node
// store backs both the nodes and inbounds services so
// the cross-entity validation in the host Service can
// resolve both nodes and inbounds.
func makeSvc(t *testing.T, nodeIDs ...uuid.UUID) *testEnv {
	t.Helper()
	env := &testEnv{
		nodes:         nodes.NewService(nodes.NewMemoryStore()),
		inboundByNode: make(map[uuid.UUID]uuid.UUID, len(nodeIDs)),
	}
	inbStore := inbounds.NewMemoryStore()
	env.svc = NewService(NewMemoryStore(), env.nodes, inbounds.NewService(inbStore, env.nodes))
	ctx := context.Background()
	for _, id := range nodeIDs {
		if _, err := env.nodes.Create(ctx, nodes.CreateInput{
			ID:      id,
			Name:    "node-" + id.String()[:8],
			Region:  "eu",
			State:   nodes.StateOnline,
			Address: "1.2.3.4:22",
		}); err != nil {
			t.Fatalf("seed node %s: %v", id, err)
		}
		inbID := uuid.New()
		if err := inbStore.Create(ctx, &inbounds.Inbound{
			ID:         inbID,
			NodeID:     id,
			Name:       "vless-main",
			Protocol:   inbounds.ProtocolVLESS,
			Listen:     "::",
			ListenPort: 443,
		}); err != nil {
			t.Fatalf("seed inbound for node %s: %v", id, err)
		}
		env.inboundByNode[id] = inbID
	}
	return env
}

func validCreateInput(nodeID, inboundID uuid.UUID) CreateInput {
	return CreateInput{
		Remark: "Latvia",
		Type:   HostTypeDirect,
		Endpoints: []Endpoint{
			{NodeID: nodeID, InboundID: inboundID, Weight: 1},
		},
	}
}

func ptrFalse() *bool { b := false; return &b }

// --- Create: happy paths ------------------------------------------------

func TestService_Create_Direct_Success(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	h, err := env.svc.Create(ctx, validCreateInput(nodeID, env.inboundFor(nodeID)))
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
	env := makeSvc(t, nodeA, nodeB)
	ctx := context.Background()

	in := CreateInput{
		Remark: "Premium EU",
		Type:   HostTypeBalancer,
		Endpoints: []Endpoint{
			{NodeID: nodeA, InboundID: env.inboundFor(nodeA), Weight: 3},
			{NodeID: nodeB, InboundID: env.inboundFor(nodeB), Weight: 2},
		},
		Balancer: &Balancer{Strategy: StrategyRoundRobin},
	}
	h, err := env.svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.Balancer == nil || h.Balancer.Strategy != StrategyRoundRobin {
		t.Errorf("balancer not set")
	}
}

func TestService_Create_AssignsPriorityAndEnabled(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID, env.inboundFor(nodeID))
	in.Enabled = ptrFalse()
	in.Priority = ptrInt(5)
	h, err := env.svc.Create(ctx, in)
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
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID, env.inboundFor(nodeID))
	in.Remark = "   "
	_, err := env.svc.Create(ctx, in)
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
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID, env.inboundFor(nodeID))
	in.Type = HostType("chain")
	_, err := env.svc.Create(ctx, in)
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
	env := makeSvc(t, nodeA, nodeB)
	ctx := context.Background()

	in := CreateInput{
		Remark: "x",
		Type:   HostTypeDirect,
		Endpoints: []Endpoint{
			{NodeID: nodeA, InboundID: env.inboundFor(nodeA), Weight: 1},
			{NodeID: nodeB, InboundID: env.inboundFor(nodeB), Weight: 1},
		},
	}
	_, err := env.svc.Create(ctx, in)
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
	env := makeSvc(t, nodeA)
	ctx := context.Background()

	in := CreateInput{
		Remark: "x",
		Type:   HostTypeBalancer,
		Endpoints: []Endpoint{
			{NodeID: nodeA, InboundID: env.inboundFor(nodeA), Weight: 1},
		},
		Balancer: &Balancer{Strategy: StrategyRoundRobin},
	}
	_, err := env.svc.Create(ctx, in)
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
	env := makeSvc(t, nodeA, nodeB)
	ctx := context.Background()

	in := CreateInput{
		Remark: "x",
		Type:   HostTypeBalancer,
		Endpoints: []Endpoint{
			{NodeID: nodeA, InboundID: env.inboundFor(nodeA), Weight: 1},
			{NodeID: nodeB, InboundID: env.inboundFor(nodeB), Weight: 1},
		},
		// no Balancer
	}
	_, err := env.svc.Create(ctx, in)
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
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID, env.inboundFor(nodeID))
	in.Balancer = &Balancer{Strategy: StrategyRoundRobin}
	_, err := env.svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "balancer" {
		t.Errorf("Field = %q, want balancer", vErr.Field)
	}
}

func TestService_Create_RejectsEndpointWithUnknownNode(t *testing.T) {
	env := makeSvc(t) // no nodes seeded
	ctx := context.Background()

	in := validCreateInput(uuid.New(), uuid.New())
	_, err := env.svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "endpoints[].node_id" {
		t.Errorf("Field = %q, want endpoints[].node_id", vErr.Field)
	}
}

func TestService_Create_RejectsEndpointWithZeroInboundID(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID, uuid.Nil)
	_, err := env.svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "endpoints[].inbound_id" {
		t.Errorf("Field = %q, want endpoints[].inbound_id", vErr.Field)
	}
}

func TestService_Create_RejectsEndpointWithUnknownInbound(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID, uuid.New()) // inbound not seeded
	_, err := env.svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "endpoints[].inbound_id" {
		t.Errorf("Field = %q, want endpoints[].inbound_id", vErr.Field)
	}
}

func TestService_Create_RejectsInboundOnWrongNode(t *testing.T) {
	nodeA := uuid.New()
	nodeB := uuid.New()
	env := makeSvc(t, nodeA, nodeB)
	ctx := context.Background()

	// Reference nodeA's inbound from an endpoint
	// claiming to be on nodeB. The cross-entity check
	// must reject.
	in := validCreateInput(nodeB, env.inboundFor(nodeA))
	_, err := env.svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "endpoints[].inbound_id" {
		t.Errorf("Field = %q, want endpoints[].inbound_id", vErr.Field)
	}
}

func TestService_Create_RejectsNegativeWeight(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID, env.inboundFor(nodeID))
	in.Endpoints[0].Weight = -1
	_, err := env.svc.Create(ctx, in)
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
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID, env.inboundFor(nodeID))
	in.Endpoints[0].Weight = 0
	h, err := env.svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.Endpoints[0].Weight != 1 {
		t.Errorf("weight = %d, want 1 (default)", h.Endpoints[0].Weight)
	}
}

func TestService_Create_RejectsUnknownStatusFilter(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID, env.inboundFor(nodeID))
	in.StatusFilter = []UserStatus{UserStatus("paused")}
	_, err := env.svc.Create(ctx, in)
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
	env := makeSvc(t, nodeA, nodeB)
	ctx := context.Background()

	in := CreateInput{
		Remark: "x",
		Type:   HostTypeBalancer,
		Endpoints: []Endpoint{
			{NodeID: nodeA, InboundID: env.inboundFor(nodeA), Weight: 1},
			{NodeID: nodeB, InboundID: env.inboundFor(nodeB), Weight: 1},
		},
		Balancer: &Balancer{Strategy: BalancerStrategy("mystery")},
	}
	_, err := env.svc.Create(ctx, in)
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
	env := makeSvc(t, nodeA, nodeB)
	ctx := context.Background()

	in := CreateInput{
		Remark: "x",
		Type:   HostTypeBalancer,
		Endpoints: []Endpoint{
			{NodeID: nodeA, InboundID: env.inboundFor(nodeA), Weight: 1},
			{NodeID: nodeB, InboundID: env.inboundFor(nodeB), Weight: 1},
		},
		Balancer: &Balancer{
			Strategy:               StrategyRoundRobin,
			HealthcheckURL:         "ftp://example.com",
			HealthcheckIntervalSec: 30,
		},
	}
	_, err := env.svc.Create(ctx, in)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
}

func TestService_Create_RejectsOutOfRangePriority(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	in := validCreateInput(nodeID, env.inboundFor(nodeID))
	in.Priority = ptrInt(100000)
	_, err := env.svc.Create(ctx, in)
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
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	if _, err := env.svc.Create(ctx, validCreateInput(nodeID, env.inboundFor(nodeID))); err != nil {
		t.Fatalf("first: %v", err)
	}
	in := validCreateInput(nodeID, env.inboundFor(nodeID))
	_, err := env.svc.Create(ctx, in)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

// --- Update -------------------------------------------------------------

func TestService_Update_PartialFields(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	h, err := env.svc.Create(ctx, validCreateInput(nodeID, env.inboundFor(nodeID)))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	enabled := false
	displayName := "🇳🇱 Netherlands"
	upd, err := env.svc.Update(ctx, h.ID, UpdateInput{
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
	env := makeSvc(t, nodeA, nodeB)
	ctx := context.Background()

	// Start as direct.
	in := validCreateInput(nodeA, env.inboundFor(nodeA))
	h, err := env.svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Change type to balancer — should fail because
	// only 1 endpoint.
	t2 := HostTypeBalancer
	_, err = env.svc.Update(ctx, h.ID, UpdateInput{Type: &t2})
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if vErr.Field != "endpoints" {
		t.Errorf("Field = %q, want endpoints", vErr.Field)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	env := makeSvc(t)
	_, err := env.svc.Update(context.Background(), uuid.New(), UpdateInput{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// --- Delete -------------------------------------------------------------

func TestService_Delete_Success(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	ctx := context.Background()

	h, err := env.svc.Create(ctx, validCreateInput(nodeID, env.inboundFor(nodeID)))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := env.svc.Delete(ctx, h.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := env.svc.Get(ctx, h.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v, want ErrNotFound", err)
	}
}

func TestService_Delete_ZeroID_Rejected(t *testing.T) {
	env := makeSvc(t)
	err := env.svc.Delete(context.Background(), uuid.Nil)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
}

// --- SetClock propagates -----------------------------------------------

func TestService_SetClock_PropagatesToStore(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	fixed := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	env.svc.SetClock(func() time.Time { return fixed })
	h, err := env.svc.Create(context.Background(), validCreateInput(nodeID, env.inboundFor(nodeID)))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !h.CreatedAt.Equal(fixed) {
		t.Errorf("CreatedAt = %s, want %s", h.CreatedAt, fixed)
	}
}

// end of file
