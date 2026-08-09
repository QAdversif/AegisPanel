// SPDX-License-Identifier: AGPL-3.0-or-later
//
//go:build integration

// Integration tests for PgStore. Run with:
//
//	make test-integration
//
// or
//
//	INTEGRATION_DATABASE_URL=postgres://... go test -tags=integration ./internal/hosts/...
//
// The `//go:build integration` tag keeps `go test ./...`
// fast and dependency-free for the default development
// loop. CI runs the tagged suite with a service-container
// Postgres.
//
// # Setup
//
// Each test seeds a single host (plus its nodes /
// inbounds as needed) and exercises one PgStore method.
// The store is per-test, the database is shared across
// tests (created fresh by MustNewPool with full
// migrations). The fixtures use unique UUIDs and
// remarks so a per-test fresh DB is enough for
// isolation — no transaction wrapper is needed.
package hosts

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/QAdversif/AegisPanel/testutil"
)

// --- helpers ------------------------------------------------------------

// seedNode inserts a single node row via raw SQL and
// returns its id. The nodes package's Service would also
// work but adds a layer of validation the host tests
// do not need.
//
// The state value is one of the Go model lifecycle
// tokens (new | online | draining | offline | disabled);
// migration 0006 aligned the nodes_state_check CHECK
// with the Go model, so any value outside that allow-list
// would trip the constraint.
func seedNode(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	const q = `
		INSERT INTO nodes (id, name, region, state, address, ssh_port, ssh_user, core_kind, drain, health)
		VALUES ($1, $2, 'eu', 'new', '1.2.3.4:22', 22, 'root', 'sing-box', FALSE, '{}'::JSONB)`
	_, err := pool.Exec(context.Background(), q, id, "node-"+id.String()[:8])
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return id
}

// seedInbound inserts a single inbound row and returns
// its id. Mirrors the inbounds package's Service.Create
// shape but with raw SQL so the test does not depend on
// the inbounds service clock / normalisation.
func seedInbound(t *testing.T, pool *pgxpool.Pool, nodeID uuid.UUID, name string, port int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	const q = `
		INSERT INTO inbounds (id, node_id, name, protocol, listen, listen_port, enabled, tags, params)
		VALUES ($1, $2, $3, 'vless', '::', $4, TRUE, '[]'::JSONB, '{}'::JSONB)`
	_, err := pool.Exec(context.Background(), q, id, nodeID, name, port)
	if err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	return id
}

// hostFixture returns a fully-wired host with two
// endpoints on two different nodes, ready to insert.
type hostFixture struct {
	host  *Host
	nodeA uuid.UUID
	nodeB uuid.UUID
	inbA  uuid.UUID
	inbB  uuid.UUID
}

func newHostFixture(t *testing.T, pool *pgxpool.Pool) hostFixture {
	t.Helper()
	f := hostFixture{
		host: &Host{
			ID:           uuid.New(),
			Remark:       "Latvia",
			Type:         HostTypeDirect,
			Enabled:      true,
			Priority:     0,
			Country:      "LV",
			City:         "Riga",
			StatusFilter: []UserStatus{UserStatusActive},
			Tags:         []string{"production", "eu"},
		},
	}
	f.nodeA = seedNode(t, pool)
	f.nodeB = seedNode(t, pool)
	f.inbA = seedInbound(t, pool, f.nodeA, "vless-a", 443)
	f.inbB = seedInbound(t, pool, f.nodeB, "vless-b", 8443)
	portA := 443
	portB := 8443
	f.host.Endpoints = []Endpoint{
		{
			ID:        uuid.New(),
			NodeID:    f.nodeA,
			InboundID: f.inbA,
			Weight:    3,
			Address:   []string{"a.example.com"},
			Port:      &portA,
			SNI:       []string{"sni-a.example.com"},
			Host:      []string{"host-a.example.com"},
			Path:      "/ws",
		},
		{
			ID:        uuid.New(),
			NodeID:    f.nodeB,
			InboundID: f.inbB,
			Weight:    1,
			Address:   []string{"b.example.com"},
			Port:      &portB,
			Path:      "/grpc",
		},
	}
	return f
}

// --- Create -------------------------------------------------------------

func TestPgStore_Create_RoundTrip(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newHostFixture(t, pool)
	ctx := context.Background()

	if err := store.Create(ctx, f.host); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if f.host.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be set by the store")
	}
	if f.host.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt should be set by the store")
	}

	got, err := store.GetByID(ctx, f.host.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Remark != "Latvia" {
		t.Errorf("remark = %q", got.Remark)
	}
	if len(got.Endpoints) != 2 {
		t.Fatalf("endpoints = %d, want 2", len(got.Endpoints))
	}
	if got.Endpoints[0].InboundID != f.inbA {
		t.Errorf("endpoints[0].inbound_id = %s, want %s", got.Endpoints[0].InboundID, f.inbA)
	}
	if got.Endpoints[0].Address[0] != "a.example.com" {
		t.Errorf("endpoints[0].address[0] = %q", got.Endpoints[0].Address[0])
	}
	if got.Endpoints[1].Path != "/grpc" {
		t.Errorf("endpoints[1].path = %q", got.Endpoints[1].Path)
	}
}

func TestPgStore_Create_RejectsDuplicateRemark(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newHostFixture(t, pool)
	ctx := context.Background()

	if err := store.Create(ctx, f.host); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second := newHostFixture(t, pool)
	second.host.Remark = f.host.Remark
	err := store.Create(ctx, second.host)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestPgStore_Create_BalancerWithNilPort(t *testing.T) {
	// Endpoints with port = nil must round-trip as
	// nil (NOT 0) — the v3 model distinguishes
	// "absent" from "explicitly zero".
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newHostFixture(t, pool)
	ctx := context.Background()

	// Drop the port override on the first endpoint.
	f.host.Endpoints[0].Port = nil

	if err := store.Create(ctx, f.host); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.GetByID(ctx, f.host.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Endpoints[0].Port != nil {
		t.Errorf("endpoints[0].port = %d, want nil", *got.Endpoints[0].Port)
	}
}

func TestPgStore_Create_NilBalancer(t *testing.T) {
	// type=direct must have a nil Balancer block;
	// the column is nullable and the round-trip must
	// preserve that.
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newHostFixture(t, pool)
	ctx := context.Background()

	f.host.Balancer = nil

	if err := store.Create(ctx, f.host); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.GetByID(ctx, f.host.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Balancer != nil {
		t.Errorf("Balancer = %+v, want nil", got.Balancer)
	}
}

func TestPgStore_Create_WithBalancer(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newHostFixture(t, pool)
	ctx := context.Background()

	f.host.Type = HostTypeBalancer
	f.host.Balancer = &Balancer{
		Strategy:               StrategyRoundRobin,
		HealthcheckURL:         "http://example.com/health",
		HealthcheckIntervalSec: 30,
		FailoverEndpointIDs:    []uuid.UUID{f.host.Endpoints[1].ID},
	}
	// type=balancer requires >=2 endpoints, which
	// the fixture already provides.

	if err := store.Create(ctx, f.host); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.GetByID(ctx, f.host.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Balancer == nil {
		t.Fatal("Balancer should round-trip as non-nil")
	}
	if got.Balancer.Strategy != StrategyRoundRobin {
		t.Errorf("Balancer.Strategy = %q, want %q", got.Balancer.Strategy, StrategyRoundRobin)
	}
	if len(got.Balancer.FailoverEndpointIDs) != 1 {
		t.Errorf("FailoverEndpointIDs len = %d, want 1", len(got.Balancer.FailoverEndpointIDs))
	}
}

// --- Get ----------------------------------------------------------------

func TestPgStore_GetByID_NotFound(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	_, err := store.GetByID(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPgStore_GetByRemark(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newHostFixture(t, pool)
	ctx := context.Background()
	if err := store.Create(ctx, f.host); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.GetByRemark(ctx, f.host.Remark)
	if err != nil {
		t.Fatalf("GetByRemark: %v", err)
	}
	if got.ID != f.host.ID {
		t.Errorf("id = %s, want %s", got.ID, f.host.ID)
	}
}

func TestPgStore_GetByRemark_NotFound(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	_, err := store.GetByRemark(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// --- List ---------------------------------------------------------------

func TestPgStore_List_SortedByPriorityThenCreatedAt(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	ctx := context.Background()

	// Two extra hosts with the same priority 0 but
	// different remarks, inserted in known order.
	for _, remark := range []string{"alpha", "bravo"} {
		fx := newHostFixture(t, pool)
		fx.host.Remark = remark
		if err := store.Create(ctx, fx.host); err != nil {
			t.Fatalf("seed %s: %v", remark, err)
		}
	}
	// One higher-priority host.
	hi := newHostFixture(t, pool)
	hi.host.Remark = "premium"
	hi.host.Priority = 100
	if err := store.Create(ctx, hi.host); err != nil {
		t.Fatalf("seed hi: %v", err)
	}

	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// 3 hosts total: alpha, bravo (priority 0, in
	// insertion order), then premium (priority 100,
	// last). Higher priority value sorts later in
	// the rendered list per the v3 model.
	if len(got) != 3 {
		t.Fatalf("got %d hosts, want 3", len(got))
	}
	if got[0].Remark != "alpha" {
		t.Errorf("first remark = %q, want alpha", got[0].Remark)
	}
	if got[1].Remark != "bravo" {
		t.Errorf("second remark = %q, want bravo", got[1].Remark)
	}
	if got[2].Remark != "premium" {
		t.Errorf("last remark = %q, want premium", got[2].Remark)
	}
}

func TestPgStore_List_EmptyReturnsNonNil(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	got, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got == nil {
		t.Fatal("List should return [] not nil")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestPgStore_List_PopulatesEndpointsForEachHost(t *testing.T) {
	// Ensure the JOIN is wired: each host in the result
	// must have its endpoints populated.
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newHostFixture(t, pool)
	ctx := context.Background()
	if err := store.Create(ctx, f.host); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if len(got[0].Endpoints) != 2 {
		t.Errorf("endpoints = %d, want 2", len(got[0].Endpoints))
	}
}

// --- Update -------------------------------------------------------------

func TestPgStore_Update_ReplacesEndpoints(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newHostFixture(t, pool)
	ctx := context.Background()
	if err := store.Create(ctx, f.host); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Replace the endpoint set with a single new
	// endpoint on a new node.
	newNode := seedNode(t, pool)
	newInb := seedInbound(t, pool, newNode, "vless-new", 9000)
	f.host.Endpoints = []Endpoint{{
		ID:        uuid.New(),
		NodeID:    newNode,
		InboundID: newInb,
		Weight:    5,
		Path:      "/new",
	}}
	if err := store.Update(ctx, f.host); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := store.GetByID(ctx, f.host.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.Endpoints) != 1 {
		t.Fatalf("endpoints = %d, want 1", len(got.Endpoints))
	}
	if got.Endpoints[0].NodeID != newNode {
		t.Errorf("endpoints[0].node_id = %s, want %s", got.Endpoints[0].NodeID, newNode)
	}
	if got.Endpoints[0].Path != "/new" {
		t.Errorf("path = %q, want /new", got.Endpoints[0].Path)
	}
}

func TestPgStore_Update_NotFound(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	err := store.Update(context.Background(), &Host{ID: uuid.New(), Remark: "x"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPgStore_Update_RenameCollision(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f1 := newHostFixture(t, pool)
	f2 := newHostFixture(t, pool)
	// The fixture defaults to remark "Latvia"; without
	// the override below the second Create would trip
	// the UNIQUE(hosts.remark) constraint before we
	// even reach the Update path. Give f2 a unique
	// starting remark so the rename collision happens
	// at the Update step, where this test lives.
	f2.host.Remark = "Lithuania"
	ctx := context.Background()
	if err := store.Create(ctx, f1.host); err != nil {
		t.Fatalf("f1: %v", err)
	}
	if err := store.Create(ctx, f2.host); err != nil {
		t.Fatalf("f2: %v", err)
	}
	f2.host.Remark = f1.host.Remark // collide on Update
	err := store.Update(ctx, f2.host)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

// --- Delete -------------------------------------------------------------

func TestPgStore_Delete_CascadesToEndpoints(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newHostFixture(t, pool)
	ctx := context.Background()
	if err := store.Create(ctx, f.host); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete(ctx, f.host.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Endpoints should be gone too.
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM host_endpoints WHERE host_id = $1`, f.host.ID).Scan(&n); err != nil {
		t.Fatalf("count endpoints: %v", err)
	}
	if n != 0 {
		t.Errorf("host_endpoints rows = %d, want 0 (CASCADE)", n)
	}
}

func TestPgStore_Delete_NotFound(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	err := store.Delete(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// --- v0.8.x: host→inbound + host→node lookups ---------------------

// TestPgStore_HostsForInbound_FirstHitWins pins the
// Builder-side v0.8.x contract: the (nodeID,
// inboundID) pair resolves to a single host id (the
// first match when the pair is referenced by
// multiple hosts). The deterministic first hit is
// the v0.8.x contract; an operator who creates the
// same (node, inbound) pair in two hosts gets a
// Service-layer warning log (not a panic).
func TestPgStore_HostsForInbound_FirstHitWins(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newHostFixture(t, pool)
	ctx := context.Background()

	if err := store.Create(ctx, f.host); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.HostsForInbound(ctx, f.nodeA, f.inbA)
	if err != nil {
		t.Fatalf("HostsForInbound: %v", err)
	}
	if got == nil {
		t.Fatal("HostsForInbound returned nil; want the host id")
	}
	if *got != f.host.ID {
		t.Errorf("host id = %s, want %s", *got, f.host.ID)
	}
}

// TestPgStore_HostsForInbound_NotInAnyHost covers the
// "inbound exists but is not referenced by any host"
// state: a builder rendering an un-provisioned
// inbound gets nil back, not an error. The builder
// sets `InboundSpec.HostID = ""` in this case.
func TestPgStore_HostsForInbound_NotInAnyHost(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newHostFixture(t, pool)
	ctx := context.Background()

	if err := store.Create(ctx, f.host); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A (node, inbound) pair that no host's
	// endpoint references. The node id is a fresh
	// UUID; the inbound id is also fresh.
	got, err := store.HostsForInbound(ctx, uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("HostsForInbound: %v", err)
	}
	if got != nil {
		t.Errorf("host id = %s, want nil (no host references the pair)", *got)
	}
}

// TestPgStore_NodesForHost_DistinctSorted pins the
// user-fan-out-side v0.8.x contract: the host
// returns every DISTINCT node id the host's
// Endpoints reference (deduplication is the
// "distinct" semantic; a host with two endpoints
// on the same node returns one entry). Empty
// result (not nil) for a host with no endpoints.
func TestPgStore_NodesForHost_DistinctSorted(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newHostFixture(t, pool)
	ctx := context.Background()

	if err := store.Create(ctx, f.host); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.NodesForHost(ctx, f.host.ID)
	if err != nil {
		t.Fatalf("NodesForHost: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("nodes = %d, want 2 (the fixture seeds 2 distinct nodes)", len(got))
	}
	// Set-equality check (DISTINCT has no defined
	// order; the SQL has no ORDER BY on the
	// node-only projection).
	gotSet := make(map[uuid.UUID]struct{}, len(got))
	for _, n := range got {
		gotSet[n] = struct{}{}
	}
	if _, ok := gotSet[f.nodeA]; !ok {
		t.Errorf("nodeA (%s) not in result %v", f.nodeA, got)
	}
	if _, ok := gotSet[f.nodeB]; !ok {
		t.Errorf("nodeB (%s) not in result %v", f.nodeB, got)
	}
}

// TestPgStore_NodesForHost_NotFound covers the
// user-fan-out "deleted host in allowlist" path:
// a host id that does not exist returns ErrNotFound.
// `enqueueUserDelta` treats this as "no nodes for
// that host" (fail-closed; warning log).
func TestPgStore_NodesForHost_NotFound(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	_, err := store.NodesForHost(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestPgStore_NodesForHost_EmptyReturnsNonNil pins
// the "host with no endpoints returns an empty
// slice (not nil) + no error" contract. The
// `enqueueUserDelta` caller treats the result as a
// set; the empty case is "host exists but has no
// endpoints", which is a valid state for a host
// being constructed in two phases.
func TestPgStore_NodesForHost_EmptyReturnsNonNil(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	hostID := uuid.New()
	// Insert a host with no endpoints via raw SQL
	// (the store's Create validates the endpoint
	// count, so we go around it). Two separate
	// parameters: $1 is the uuid id, $2 is the
	// remark text. Using the same parameter twice
	// with two different casts (`$1, '$1::text'`)
	// would trip Postgres' "inconsistent types
	// deduced for parameter $1" error
	// (SQLSTATE 42P08), because the planner pins
	// the first cast and the second one has to
	// match.
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO hosts (id, remark, type, enabled, priority,
			status_filter, country, city, tags, balancer)
		VALUES ($1, $2, 'direct', true, 0, '[]'::jsonb, '',
			'', '[]'::jsonb, null)`,
		hostID, "no-endpoints-host-"+hostID.String(),
	); err != nil {
		t.Fatalf("seed host: %v", err)
	}

	got, err := store.NodesForHost(context.Background(), hostID)
	if err != nil {
		t.Fatalf("NodesForHost: %v", err)
	}
	if got == nil {
		t.Error("got nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}
