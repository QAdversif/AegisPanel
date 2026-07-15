// SPDX-License-Identifier: AGPL-3.0-or-later
//
//go:build integration

// Integration tests for PgStore. Run with:
//
//	make test-integration
//
// or
//
//	INTEGRATION_DATABASE_URL=postgres://... go test -tags=integration ./internal/nodes/...
//
// The `//go:build integration` tag keeps `go test ./...` fast
// and dependency-free for the default development loop. CI
// runs the tagged suite with a service-container Postgres.
//
// # Setup
//
// Each test seeds a single node and exercises one PgStore
// method. The store is per-test, the database is shared
// across tests (created fresh by MustNewPool with full
// migrations). Tests that need isolation use unique names
// so a per-test fresh DB is enough.
package nodes

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/testutil"
)

// nodeFixture returns a fully-wired node with a random
// suffix on the name so two tests in the same run cannot
// collide on the unique `nodes.name` constraint.
type nodeFixture struct {
	node *Node
}

func newNodeFixture(t *testing.T) nodeFixture {
	t.Helper()
	return nodeFixture{
		node: &Node{
			ID:           uuid.New(),
			Name:         "alpha-" + uuid.New().String()[:8],
			Region:       "eu",
			State:        StateNew,
			Address:      "1.2.3.4:22",
			CapacityHint: "1 Gbps",
			Tags:         []string{"production", "eu"},
		},
	}
}

// --- Create -------------------------------------------------------------

func TestPgStore_Create_RoundTrip(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newNodeFixture(t)
	ctx := context.Background()

	if err := store.Create(ctx, f.node); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if f.node.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be set by the store")
	}
	if f.node.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt should be set by the store")
	}

	got, err := store.GetByID(ctx, f.node.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != f.node.Name {
		t.Errorf("name = %q, want %q", got.Name, f.node.Name)
	}
	if got.CapacityHint != "1 Gbps" {
		t.Errorf("capacity_hint = %q, want %q", got.CapacityHint, "1 Gbps")
	}
	if len(got.Tags) != 2 {
		t.Fatalf("tags = %d, want 2", len(got.Tags))
	}
}

func TestPgStore_Create_RejectsDuplicateName(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newNodeFixture(t)
	ctx := context.Background()

	if err := store.Create(ctx, f.node); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second := newNodeFixture(t)
	second.node.Name = f.node.Name
	err := store.Create(ctx, second.node)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestPgStore_Create_NoTags(t *testing.T) {
	// A node with an empty Tags slice must round-trip with
	// no tags and a nil slice in the result (matches the
	// MemoryStore contract).
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newNodeFixture(t)
	f.node.Tags = nil
	ctx := context.Background()

	if err := store.Create(ctx, f.node); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.GetByID(ctx, f.node.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Tags != nil {
		t.Errorf("Tags = %v, want nil", got.Tags)
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

func TestPgStore_GetByName(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newNodeFixture(t)
	ctx := context.Background()
	if err := store.Create(ctx, f.node); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.GetByName(ctx, f.node.Name)
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got.ID != f.node.ID {
		t.Errorf("id = %s, want %s", got.ID, f.node.ID)
	}
}

func TestPgStore_GetByName_NotFound(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	_, err := store.GetByName(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// --- List ---------------------------------------------------------------

func TestPgStore_List_SortedByCreatedAt(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	ctx := context.Background()

	for _, name := range []string{"first", "second", "third"} {
		f := newNodeFixture(t)
		f.node.Name = name + "-" + uuid.New().String()[:8]
		if err := store.Create(ctx, f.node); err != nil {
			t.Fatalf("seed %s: %v", f.node.Name, err)
		}
	}

	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// Insertion order: first, second, third.
	if !containsName(got, "first") || !containsName(got, "second") || !containsName(got, "third") {
		t.Errorf("missing one of first/second/third in %v", nodeNames(got))
	}
	// Ordering: got[0] should be the oldest (CreatedAt smallest).
	for i := 1; i < len(got); i++ {
		if got[i].CreatedAt.Before(got[i-1].CreatedAt) {
			t.Errorf("got[%d].CreatedAt (%v) < got[%d].CreatedAt (%v)",
				i, got[i].CreatedAt, i-1, got[i-1].CreatedAt)
		}
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

func TestPgStore_List_PopulatesTagsForEachNode(t *testing.T) {
	// Ensure the JOIN is wired: each node in the result
	// must have its tags populated.
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newNodeFixture(t)
	ctx := context.Background()
	if err := store.Create(ctx, f.node); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if len(got[0].Tags) != 2 {
		t.Errorf("tags = %d, want 2", len(got[0].Tags))
	}
}

// --- Update -------------------------------------------------------------

func TestPgStore_Update_ReplacesTags(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newNodeFixture(t)
	ctx := context.Background()
	if err := store.Create(ctx, f.node); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Replace the tag set with a different list.
	f.node.Tags = []string{"staging", "us"}
	if err := store.Update(ctx, f.node); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := store.GetByID(ctx, f.node.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.Tags) != 2 {
		t.Fatalf("tags = %d, want 2", len(got.Tags))
	}
	// Tags should be the new ones, not the originals.
	for _, tag := range got.Tags {
		if tag != "staging" && tag != "us" {
			t.Errorf("unexpected tag %q in result", tag)
		}
	}
}

func TestPgStore_Update_NotFound(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	err := store.Update(context.Background(), &Node{ID: uuid.New(), Name: "x"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPgStore_Update_RenameCollision(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f1 := newNodeFixture(t)
	f2 := newNodeFixture(t)
	ctx := context.Background()
	if err := store.Create(ctx, f1.node); err != nil {
		t.Fatalf("f1: %v", err)
	}
	if err := store.Create(ctx, f2.node); err != nil {
		t.Fatalf("f2: %v", err)
	}
	f2.node.Name = f1.node.Name // collide on Update
	err := store.Update(ctx, f2.node)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

// --- Delete -------------------------------------------------------------

func TestPgStore_Delete_CascadesToTags(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	f := newNodeFixture(t)
	ctx := context.Background()
	if err := store.Create(ctx, f.node); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete(ctx, f.node.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Tags should be gone too.
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM node_tags WHERE node_id = $1`, f.node.ID).Scan(&n); err != nil {
		t.Fatalf("count tags: %v", err)
	}
	if n != 0 {
		t.Errorf("node_tags rows = %d, want 0 (CASCADE)", n)
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

// --- helpers ------------------------------------------------------------

func containsName(nodes []*Node, prefix string) bool {
	for _, n := range nodes {
		if len(n.Name) >= len(prefix) && n.Name[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func nodeNames(nodes []*Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Name
	}
	return out
}
