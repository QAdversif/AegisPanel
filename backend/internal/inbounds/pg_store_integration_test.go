// SPDX-License-Identifier: AGPL-3.0-or-later
//
//go:build integration

// Integration tests for PgStore. Run with:
//
//	make test-integration
//
// or
//
//	INTEGRATION_DATABASE_URL=postgres://... go test -tags=integration ./internal/inbounds/...
//
// The `//go:build integration` tag keeps `go test ./...` fast
// and dependency-free for the default development loop. CI
// runs the tagged suite with a service-container Postgres.
//
// # Setup
//
// Each test seeds a single inbound (plus a node) and
// exercises one PgStore method. The store is per-test, the
// database is shared across tests (created fresh by
// MustNewPool with full migrations). Tests that need unique
// names use a random suffix so a per-test fresh DB is enough.
package inbounds

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/QAdversif/AegisPanel/testutil"
)

// --- helpers ------------------------------------------------------------

// seedNode inserts a single node row and returns its id. The
// state value is `new` per the Go model lifecycle (migration
// 0006 aligned `nodes_state_check` with the Go model
// allow-list).
func seedNode(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	const q = `
		INSERT INTO nodes (id, name, region, state, address, ssh_port, ssh_user, core_kind, drain, health)
		VALUES ($1, $2, 'eu', 'new', '1.2.3.4:22', 22, 'root', 'sing-box', FALSE, '{}'::JSONB)`
	if _, err := pool.Exec(context.Background(), q, id, "node-"+id.String()[:8]); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return id
}

// inboundFixture returns a fully-wired inbound on a freshly
// seeded node, with a random suffix on the name so two
// fixtures in the same test do not collide on the
// (node_id, name) unique constraint.
type inboundFixture struct {
	inbound *Inbound
	nodeID  uuid.UUID
}

func newInboundFixture(t *testing.T, pool *pgxpool.Pool) inboundFixture {
	t.Helper()
	nodeID := seedNode(t, pool)
	return inboundFixture{
		inbound: &Inbound{
			ID:         uuid.New(),
			NodeID:     nodeID,
			Name:       "vless-" + uuid.New().String()[:8],
			Protocol:   ProtocolVLESS,
			Listen:     "::",
			ListenPort: 443,
			Enabled:    true,
			Tags:       []string{"production", "eu"},
			Params:     map[string]any{"reality": map[string]any{"enabled": true}},
		},
		nodeID: nodeID,
	}
}

// --- Create -------------------------------------------------------------

func TestPgStore_Create_RoundTrip(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newInboundFixture(t, pool)
	ctx := context.Background()

	if err := store.Create(ctx, f.inbound); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.GetByID(ctx, f.inbound.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != f.inbound.Name {
		t.Errorf("name = %q, want %q", got.Name, f.inbound.Name)
	}
	if got.Protocol != ProtocolVLESS {
		t.Errorf("protocol = %q, want %q", got.Protocol, ProtocolVLESS)
	}
	if got.ListenPort != 443 {
		t.Errorf("listen_port = %d, want 443", got.ListenPort)
	}
	if !got.Enabled {
		t.Errorf("enabled = false, want true")
	}
	if len(got.Tags) != 2 {
		t.Errorf("tags = %d, want 2", len(got.Tags))
	}
	if got.Params == nil {
		t.Errorf("params = nil, want non-nil")
	}
}

func TestPgStore_Create_RejectsDuplicateName(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newInboundFixture(t, pool)
	ctx := context.Background()

	if err := store.Create(ctx, f.inbound); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second := newInboundFixture(t, pool)
	// Second inbound on the same node, same name (different
	// port) should collide on (node_id, name).
	second.inbound.NodeID = f.nodeID
	second.inbound.Name = f.inbound.Name
	second.inbound.ListenPort = f.inbound.ListenPort + 1
	err := store.Create(ctx, second.inbound)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestPgStore_Create_RejectsDuplicatePort(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newInboundFixture(t, pool)
	ctx := context.Background()

	if err := store.Create(ctx, f.inbound); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second := newInboundFixture(t, pool)
	// Second inbound on the same node, same port (different
	// name) should collide on (node_id, listen_port).
	second.inbound.NodeID = f.nodeID
	second.inbound.Name = f.inbound.Name + "-other"
	second.inbound.ListenPort = f.inbound.ListenPort
	err := store.Create(ctx, second.inbound)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestPgStore_Create_NilParams(t *testing.T) {
	// Params is nullable JSONB; a nil map must round-trip as
	// a nil map (not a non-nil empty struct).
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newInboundFixture(t, pool)
	f.inbound.Params = nil
	ctx := context.Background()

	if err := store.Create(ctx, f.inbound); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.GetByID(ctx, f.inbound.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Params != nil {
		t.Errorf("params = %+v, want nil", got.Params)
	}
}

func TestPgStore_Create_NoTags(t *testing.T) {
	// Tags is non-nullable JSONB; an empty slice must round-trip
	// as an empty slice (or nil — both are valid JSON `[]`).
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newInboundFixture(t, pool)
	f.inbound.Tags = nil
	ctx := context.Background()

	if err := store.Create(ctx, f.inbound); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.GetByID(ctx, f.inbound.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.Tags) != 0 {
		t.Errorf("tags = %v, want empty", got.Tags)
	}
}

func TestPgStore_Create_RejectsUnknownProtocol(t *testing.T) {
	// The protocol CHECK constraint must reject values outside
	// the Go model allow-list. The Service layer also
	// validates this, but the DB CHECK is the last line of
	// defence — if the Service is bypassed, the DB still
	// refuses the row.
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newInboundFixture(t, pool)
	f.inbound.Protocol = "wireguard" // not in the allow-list
	ctx := context.Background()

	err := store.Create(ctx, f.inbound)
	if err == nil {
		t.Fatal("Create with unknown protocol should have failed")
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

func TestPgStore_GetByNodeAndName(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newInboundFixture(t, pool)
	ctx := context.Background()
	if err := store.Create(ctx, f.inbound); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.GetByNodeAndName(ctx, f.nodeID, f.inbound.Name)
	if err != nil {
		t.Fatalf("GetByNodeAndName: %v", err)
	}
	if got.ID != f.inbound.ID {
		t.Errorf("id = %s, want %s", got.ID, f.inbound.ID)
	}
}

func TestPgStore_GetByNodeAndName_NotFound(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	_, err := store.GetByNodeAndName(context.Background(), uuid.New(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPgStore_GetByNodeAndPort(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newInboundFixture(t, pool)
	ctx := context.Background()
	if err := store.Create(ctx, f.inbound); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.GetByNodeAndPort(ctx, f.nodeID, f.inbound.ListenPort)
	if err != nil {
		t.Fatalf("GetByNodeAndPort: %v", err)
	}
	if got.ID != f.inbound.ID {
		t.Errorf("id = %s, want %s", got.ID, f.inbound.ID)
	}
}

func TestPgStore_GetByNodeAndPort_NotFound(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	_, err := store.GetByNodeAndPort(context.Background(), uuid.New(), 99999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// --- List ---------------------------------------------------------------

func TestPgStore_ListByNode_SortedByPort(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newInboundFixture(t, pool)
	ctx := context.Background()

	// Three inbounds on the same node, ports 200, 100, 300.
	for _, port := range []int{200, 100, 300} {
		ib := *f.inbound // shallow copy
		ib.ID = uuid.New()
		ib.Name = "p-" + uuid.New().String()[:8]
		ib.ListenPort = port
		if err := store.Create(ctx, &ib); err != nil {
			t.Fatalf("seed port %d: %v", port, err)
		}
	}

	got, err := store.ListByNode(ctx, f.nodeID)
	if err != nil {
		t.Fatalf("ListByNode: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// Sorted by listen_port, then name.
	if got[0].ListenPort != 100 || got[1].ListenPort != 200 || got[2].ListenPort != 300 {
		t.Errorf("order = [%d, %d, %d], want [100, 200, 300]",
			got[0].ListenPort, got[1].ListenPort, got[2].ListenPort)
	}
}

func TestPgStore_ListByNode_Empty(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	got, err := store.ListByNode(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ListByNode: %v", err)
	}
	if got == nil {
		t.Fatal("ListByNode should return [] not nil")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestPgStore_ListByProtocol(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newInboundFixture(t, pool)
	ctx := context.Background()
	if err := store.Create(ctx, f.inbound); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.ListByProtocol(ctx, ProtocolVLESS)
	if err != nil {
		t.Fatalf("ListByProtocol: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("len = 0, want >= 1")
	}
	for _, ib := range got {
		if ib.Protocol != ProtocolVLESS {
			t.Errorf("protocol = %q, want %q", ib.Protocol, ProtocolVLESS)
		}
	}
}

// --- Update -------------------------------------------------------------

func TestPgStore_Update_ReplacesFields(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newInboundFixture(t, pool)
	ctx := context.Background()
	if err := store.Create(ctx, f.inbound); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Update every non-key field.
	f.inbound.Protocol = ProtocolHysteria2
	f.inbound.ListenPort = 8443
	f.inbound.Enabled = false
	f.inbound.Tags = []string{"staging"}
	f.inbound.Params = nil
	if err := store.Update(ctx, f.inbound); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := store.GetByID(ctx, f.inbound.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Protocol != ProtocolHysteria2 {
		t.Errorf("protocol = %q, want %q", got.Protocol, ProtocolHysteria2)
	}
	if got.ListenPort != 8443 {
		t.Errorf("listen_port = %d, want 8443", got.ListenPort)
	}
	if got.Enabled {
		t.Errorf("enabled = true, want false")
	}
	if len(got.Tags) != 1 || got.Tags[0] != "staging" {
		t.Errorf("tags = %v, want [staging]", got.Tags)
	}
	if got.Params != nil {
		t.Errorf("params = %+v, want nil", got.Params)
	}
}

func TestPgStore_Update_NameCollision(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	// Two inbounds on the SAME node, distinct names + ports.
	// The Update path is intentionally scoped to one node —
	// moving an inbound to a different node is a destructive
	// operation handled by the Service layer (Delete + Create
	// or a future Move method), not by Update.
	nodeID := seedNode(t, pool)
	f1 := &Inbound{
		ID:         uuid.New(),
		NodeID:     nodeID,
		Name:       "f1-" + uuid.New().String()[:8],
		Protocol:   ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
		Enabled:    true,
		Tags:       []string{"f1"},
		Params:     map[string]any{"a": "1"},
	}
	f2 := &Inbound{
		ID:         uuid.New(),
		NodeID:     nodeID,
		Name:       "f2-" + uuid.New().String()[:8],
		Protocol:   ProtocolVLESS,
		Listen:     "::",
		ListenPort: 8443,
		Enabled:    true,
		Tags:       []string{"f2"},
		Params:     map[string]any{"b": "2"},
	}
	ctx := context.Background()
	if err := store.Create(ctx, f1); err != nil {
		t.Fatalf("f1: %v", err)
	}
	if err := store.Create(ctx, f2); err != nil {
		t.Fatalf("f2: %v", err)
	}
	// Rename f2 to f1's name; should collide on
	// (node_id, name).
	f2.Name = f1.Name
	err := store.Update(ctx, f2)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestPgStore_Update_PortCollision(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	nodeID := seedNode(t, pool)
	f1 := &Inbound{
		ID:         uuid.New(),
		NodeID:     nodeID,
		Name:       "p1-" + uuid.New().String()[:8],
		Protocol:   ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
		Enabled:    true,
		Tags:       []string{"p1"},
		Params:     map[string]any{"a": "1"},
	}
	f2 := &Inbound{
		ID:         uuid.New(),
		NodeID:     nodeID,
		Name:       "p2-" + uuid.New().String()[:8],
		Protocol:   ProtocolVLESS,
		Listen:     "::",
		ListenPort: 8443,
		Enabled:    true,
		Tags:       []string{"p2"},
		Params:     map[string]any{"b": "2"},
	}
	ctx := context.Background()
	if err := store.Create(ctx, f1); err != nil {
		t.Fatalf("f1: %v", err)
	}
	if err := store.Create(ctx, f2); err != nil {
		t.Fatalf("f2: %v", err)
	}
	// Move f2's port onto f1's port; should collide on
	// (node_id, listen_port).
	f2.ListenPort = f1.ListenPort
	err := store.Update(ctx, f2)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestPgStore_Update_NotFound(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	err := store.Update(context.Background(), &Inbound{ID: uuid.New(), Name: "x"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// --- Delete -------------------------------------------------------------

func TestPgStore_Delete_NotFound(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	err := store.Delete(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPgStore_Delete_RemovesRow(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newInboundFixture(t, pool)
	ctx := context.Background()
	if err := store.Create(ctx, f.inbound); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete(ctx, f.inbound.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := store.GetByID(ctx, f.inbound.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
