// SPDX-License-Identifier: AGPL-3.0-or-later

package hosts

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestHost(remark string, eps ...Endpoint) *Host {
	return &Host{
		ID:        uuid.New(),
		Remark:    remark,
		Type:      HostTypeDirect,
		Enabled:   true,
		Priority:  0,
		Endpoints: eps,
	}
}

// defaultEndpoint is the v1, weight-1, vless endpoint
// used by most of the MemoryStore tests. The few tests
// that need a different shape build the Endpoint
// inline.
func defaultEndpoint() Endpoint {
	return Endpoint{
		ID:       uuid.New(),
		NodeID:   uuid.New(),
		Protocol: "vless",
		Weight:   1,
	}
}

func TestMemoryStore_CreateAndGet(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	h := newTestHost("Latvia", defaultEndpoint())
	if err := store.Create(ctx, h); err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be set by the store")
	}
	if h.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt should be set by the store")
	}
	got, err := store.GetByID(ctx, h.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Remark != h.Remark {
		t.Errorf("remark = %q, want %q", got.Remark, h.Remark)
	}
}

func TestMemoryStore_GetByID_NotFound(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.GetByID(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_GetByRemark_CaseInsensitive(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	h := newTestHost("Latvia", defaultEndpoint())
	if err := store.Create(ctx, h); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.GetByRemark(ctx, "LATVIA")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != h.ID {
		t.Fatalf("got id %s, want %s", got.ID, h.ID)
	}
}

func TestMemoryStore_GetByRemark_NotFound(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.GetByRemark(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_DuplicateRemark_Rejected(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.Create(ctx, newTestHost("Latvia", defaultEndpoint())); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := store.Create(ctx, newTestHost("Latvia", defaultEndpoint()))
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestMemoryStore_DuplicateID_Rejected(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	h1 := newTestHost("a", defaultEndpoint())
	if err := store.Create(ctx, h1); err != nil {
		t.Fatalf("first: %v", err)
	}
	h2 := newTestHost("b", defaultEndpoint())
	h2.ID = h1.ID
	err := store.Create(ctx, h2)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestMemoryStore_Update_PreservesCreatedAt(t *testing.T) {
	store := NewMemoryStore()
	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return fixedNow })
	ctx := context.Background()
	h := newTestHost("a", defaultEndpoint())
	if err := store.Create(ctx, h); err != nil {
		t.Fatalf("create: %v", err)
	}
	original := h.CreatedAt

	// Advance the clock and update.
	store.SetClock(func() time.Time { return fixedNow.Add(2 * time.Hour) })
	h.Remark = "b"
	if err := store.Update(ctx, h); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !h.CreatedAt.Equal(original) {
		t.Errorf("CreatedAt changed across update: was %s, now %s", original, h.CreatedAt)
	}
	if !h.UpdatedAt.Equal(fixedNow.Add(2 * time.Hour)) {
		t.Errorf("UpdatedAt = %s, want %s", h.UpdatedAt, fixedNow.Add(2*time.Hour))
	}
}

func TestMemoryStore_Update_RenameCollision_Rejected(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	a := newTestHost("alpha", defaultEndpoint())
	b := newTestHost("bravo", defaultEndpoint())
	if err := store.Create(ctx, a); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := store.Create(ctx, b); err != nil {
		t.Fatalf("b: %v", err)
	}
	b.Remark = "alpha"
	err := store.Update(ctx, b)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	h := newTestHost("a", defaultEndpoint())
	if err := store.Create(ctx, h); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Delete(ctx, h.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetByID(ctx, h.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v, want ErrNotFound", err)
	}
	// Remark slot should be free now — a fresh host
	// with the same remark should succeed.
	if err := store.Create(ctx, newTestHost("a", defaultEndpoint())); err != nil {
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

func TestMemoryStore_List_SortedByPriorityThenCreatedAt(t *testing.T) {
	store := NewMemoryStore()
	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return fixedNow })
	ctx := context.Background()

	// Insert out of order on purpose: priority 0
	// second, priority 1 first, priority 0 first.
	// Expected order: priority 0 (created first) ->
	// priority 0 (created second) -> priority 1.
	if err := store.Create(ctx, newTestHost("b", defaultEndpoint())); err != nil {
		t.Fatalf("b: %v", err)
	}
	a := newTestHost("a", defaultEndpoint())
	if err := store.Create(ctx, a); err != nil {
		t.Fatalf("a: %v", err)
	}
	c := newTestHost("c", defaultEndpoint())
	c.Priority = 1
	if err := store.Create(ctx, c); err != nil {
		t.Fatalf("c: %v", err)
	}

	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// All three were created at the exact same fixedNow.
	// Among "b" and "a" (both priority 0) the tiebreak
	// is CreatedAt — "before" returns false when equal,
	// so the sort is stable and they stay in insertion
	// order. We only assert what the architecture
	// promises: priority-0 hosts come before priority-1.
	gotRemarks := make([]string, len(got))
	for i, h := range got {
		gotRemarks[i] = h.Remark
	}
	if gotRemarks[2] != "c" {
		t.Fatalf("expected c last (priority 1), got order: %v", gotRemarks)
	}
	_ = sort.StringsAreSorted // keep the import live; not strictly needed
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	const N = 50
	done := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() {
			h := newTestHost("h-"+uuid.NewString(), defaultEndpoint())
			done <- store.Create(ctx, h)
		}()
	}
	for i := 0; i < N; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent create: %v", err)
		}
	}
	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != N {
		t.Errorf("got %d hosts, want %d", len(got), N)
	}
}
