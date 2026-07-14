// SPDX-License-Identifier: AGPL-3.0-or-later

package inbounds

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// newTestInbound returns a unique valid inbound with
// the given name and port. Two callers in the same test
// get distinct inbounds (each has a fresh ID).
func newTestInbound(nodeID uuid.UUID, name string, port int) *Inbound {
	return &Inbound{
		ID:         uuid.New(),
		NodeID:     nodeID,
		Name:       name,
		Protocol:   ProtocolVLESS,
		Listen:     "::",
		ListenPort: port,
		Enabled:    true,
	}
}

func TestMemoryStore_CreateAndGet(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeID := uuid.New()
	i := newTestInbound(nodeID, "vless-main", 443)
	if err := store.Create(ctx, i); err != nil {
		t.Fatalf("create: %v", err)
	}
	if i.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be set by the store")
	}
	if i.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt should be set by the store")
	}
	got, err := store.GetByID(ctx, i.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != i.Name {
		t.Errorf("name = %q, want %q", got.Name, i.Name)
	}
}

func TestMemoryStore_DuplicateName_Rejected(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeID := uuid.New()
	if err := store.Create(ctx, newTestInbound(nodeID, "vless-main", 443)); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Same node, same name, different port.
	err := store.Create(ctx, newTestInbound(nodeID, "vless-main", 8443))
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestMemoryStore_DuplicatePort_Rejected(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeID := uuid.New()
	if err := store.Create(ctx, newTestInbound(nodeID, "vless-main", 443)); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Same node, different name, same port.
	err := store.Create(ctx, newTestInbound(nodeID, "hy2-main", 443))
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestMemoryStore_DuplicateID_Rejected(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeID := uuid.New()
	first := newTestInbound(nodeID, "a", 443)
	if err := store.Create(ctx, first); err != nil {
		t.Fatalf("first: %v", err)
	}
	second := newTestInbound(nodeID, "b", 8443)
	second.ID = first.ID
	err := store.Create(ctx, second)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestMemoryStore_SameNameAcrossNodes_Allowed(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeA := uuid.New()
	nodeB := uuid.New()
	if err := store.Create(ctx, newTestInbound(nodeA, "vless-main", 443)); err != nil {
		t.Fatalf("a: %v", err)
	}
	// Same name, different node — must be allowed.
	if err := store.Create(ctx, newTestInbound(nodeB, "vless-main", 443)); err != nil {
		t.Fatalf("b: %v", err)
	}
}

func TestMemoryStore_GetByID_NotFound(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.GetByID(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_GetByNodeAndName(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeID := uuid.New()
	i := newTestInbound(nodeID, "vless-main", 443)
	if err := store.Create(ctx, i); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.GetByNodeAndName(ctx, nodeID, "vless-main")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != i.ID {
		t.Errorf("got id %s, want %s", got.ID, i.ID)
	}
}

func TestMemoryStore_GetByNodeAndPort(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeID := uuid.New()
	i := newTestInbound(nodeID, "vless-main", 443)
	if err := store.Create(ctx, i); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.GetByNodeAndPort(ctx, nodeID, 443)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != i.ID {
		t.Errorf("got id %s, want %s", got.ID, i.ID)
	}
}

func TestMemoryStore_ListByNode_SortedByPort(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeID := uuid.New()
	// Insert out of order on purpose: 8443, 443, 2053.
	if err := store.Create(ctx, newTestInbound(nodeID, "a", 8443)); err != nil {
		t.Fatalf("8443: %v", err)
	}
	if err := store.Create(ctx, newTestInbound(nodeID, "b", 443)); err != nil {
		t.Fatalf("443: %v", err)
	}
	if err := store.Create(ctx, newTestInbound(nodeID, "c", 2053)); err != nil {
		t.Fatalf("2053: %v", err)
	}
	got, err := store.ListByNode(ctx, nodeID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	wantPorts := []int{443, 2053, 8443}
	if len(got) != len(wantPorts) {
		t.Fatalf("got %d inbounds, want %d", len(got), len(wantPorts))
	}
	for j, item := range got {
		if item.ListenPort != wantPorts[j] {
			t.Errorf("item[%d] port = %d, want %d", j, item.ListenPort, wantPorts[j])
		}
	}
}

func TestMemoryStore_ListByNode_OnlyReturnsRequestedNode(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeA := uuid.New()
	nodeB := uuid.New()
	if err := store.Create(ctx, newTestInbound(nodeA, "a", 443)); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := store.Create(ctx, newTestInbound(nodeB, "b", 443)); err != nil {
		t.Fatalf("b: %v", err)
	}
	got, err := store.ListByNode(ctx, nodeA)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d inbounds for nodeA, want 1", len(got))
	}
	if got[0].NodeID != nodeA {
		t.Errorf("got node %s, want %s", got[0].NodeID, nodeA)
	}
}

func TestMemoryStore_ListByProtocol(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeA := uuid.New()
	nodeB := uuid.New()
	vless1 := newTestInbound(nodeA, "v1", 443)
	vless1.Protocol = ProtocolVLESS
	vless2 := newTestInbound(nodeB, "v2", 443)
	vless2.Protocol = ProtocolVLESS
	hy2 := newTestInbound(nodeA, "h1", 8443)
	hy2.Protocol = ProtocolHysteria2
	if err := store.Create(ctx, vless1); err != nil {
		t.Fatalf("vless1: %v", err)
	}
	if err := store.Create(ctx, vless2); err != nil {
		t.Fatalf("vless2: %v", err)
	}
	if err := store.Create(ctx, hy2); err != nil {
		t.Fatalf("hy2: %v", err)
	}
	got, err := store.ListByProtocol(ctx, ProtocolVLESS)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d vless inbounds, want 2", len(got))
	}
}

func TestMemoryStore_Update_PreservesCreatedAt(t *testing.T) {
	store := NewMemoryStore()
	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return fixedNow })
	ctx := context.Background()
	nodeID := uuid.New()
	i := newTestInbound(nodeID, "a", 443)
	if err := store.Create(ctx, i); err != nil {
		t.Fatalf("create: %v", err)
	}
	originalCreated := i.CreatedAt

	store.SetClock(func() time.Time { return fixedNow.Add(2 * time.Hour) })
	i.Name = "b"
	if err := store.Update(ctx, i); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !i.CreatedAt.Equal(originalCreated) {
		t.Errorf("CreatedAt changed across update: was %s, now %s", originalCreated, i.CreatedAt)
	}
	if !i.UpdatedAt.Equal(fixedNow.Add(2 * time.Hour)) {
		t.Errorf("UpdatedAt = %s, want %s", i.UpdatedAt, fixedNow.Add(2*time.Hour))
	}
}

func TestMemoryStore_Update_PortCollision_Rejected(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeID := uuid.New()
	a := newTestInbound(nodeID, "a", 443)
	b := newTestInbound(nodeID, "b", 8443)
	if err := store.Create(ctx, a); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := store.Create(ctx, b); err != nil {
		t.Fatalf("b: %v", err)
	}
	// Try to move b onto a's port.
	b.ListenPort = 443
	err := store.Update(ctx, b)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeID := uuid.New()
	i := newTestInbound(nodeID, "a", 443)
	if err := store.Create(ctx, i); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Delete(ctx, i.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetByID(ctx, i.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: err = %v, want ErrNotFound", err)
	}
	// Both indexes should be free — a new inbound
	// with the same name and the same port must
	// succeed.
	if err := store.Create(ctx, newTestInbound(nodeID, "a", 443)); err != nil {
		t.Fatalf("recreate: %v", err)
	}
}

func TestMemoryStore_Delete_NotFound(t *testing.T) {
	store := NewMemoryStore()
	err := store.Delete(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_Clone_IsDeepEnough(t *testing.T) {
	src := &Inbound{
		ID:         uuid.New(),
		NodeID:     uuid.New(),
		Name:       "x",
		Protocol:   ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
		Enabled:    true,
		Tags:       []string{"a", "b"},
		Params:     map[string]any{"k": "v"},
	}
	dst := cloneInbound(src)
	// Mutate dst's slices and map; src must not change.
	dst.Tags[0] = "MUTATED"
	dst.Params["k"] = "MUTATED"
	if src.Tags[0] != "a" {
		t.Errorf("Tags clone is shallow: %v", src.Tags)
	}
	if src.Params["k"] != "v" {
		t.Errorf("Params clone is shallow: %v", src.Params)
	}
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeID := uuid.New()
	const N = 50
	done := make(chan error, N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			item := newTestInbound(nodeID, uuid.NewString(), 10000+idx)
			done <- store.Create(ctx, item)
		}(i)
	}
	for i := 0; i < N; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent create: %v", err)
		}
	}
	got, err := store.ListByNode(ctx, nodeID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != N {
		t.Errorf("got %d inbounds, want %d", len(got), N)
	}
}
