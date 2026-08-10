// SPDX-License-Identifier: AGPL-3.0-or-later

package inboundtemplates

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fixedClock returns a clock pinned to a known
// instant so the test assertions on CreatedAt /
// UpdatedAt are deterministic.
func fixedClock() func() time.Time {
	t := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	s := NewService(NewMemoryStore())
	s.SetClock(fixedClock())
	return s
}

func TestMemoryStore_Roundtrip(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	store.SetClock(fixedClock())

	tpl := &InboundTemplate{
		ID:          uuid.New(),
		Name:        "vless-reality-eu",
		Protocol:    ProtocolVLESS,
		Params:      map[string]any{"flow": "xtls-rprx-vision"},
		Description: "VLESS Reality for the EU fleet",
	}
	if err := store.Create(context.Background(), tpl); err != nil {
		t.Fatalf("create: %v", err)
	}
	if tpl.CreatedAt.IsZero() || tpl.UpdatedAt.IsZero() {
		t.Fatalf("expected non-zero timestamps after create: %+v", tpl)
	}

	// GetByID round-trips.
	got, err := store.GetByID(context.Background(), tpl.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != tpl.Name {
		t.Fatalf("name round-trip mismatch: %q vs %q", got.Name, tpl.Name)
	}
	if got.Protocol != tpl.Protocol {
		t.Fatalf("protocol round-trip mismatch: %q vs %q", got.Protocol, tpl.Protocol)
	}
	if v, ok := got.Params["flow"].(string); !ok || v != "xtls-rprx-vision" {
		t.Fatalf("params round-trip mismatch: %+v", got.Params)
	}

	// GetByName round-trips.
	got2, err := store.GetByName(context.Background(), tpl.Name)
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if got2.ID != tpl.ID {
		t.Fatalf("get by name: id mismatch %s vs %s", got2.ID, tpl.ID)
	}

	// List returns the template.
	list, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list: got %d, want 1", len(list))
	}

	// Delete round-trips.
	if err := store.Delete(context.Background(), tpl.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetByID(context.Background(), tpl.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: got %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_DuplicateName(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	store.SetClock(fixedClock())

	a := &InboundTemplate{
		ID:       uuid.New(),
		Name:     "vless-reality-eu",
		Protocol: ProtocolVLESS,
		Params:   map[string]any{"flow": "xtls-rprx-vision"},
	}
	if err := store.Create(context.Background(), a); err != nil {
		t.Fatalf("create a: %v", err)
	}
	// Same name, different id.
	b := &InboundTemplate{
		ID:       uuid.New(),
		Name:     "vless-reality-eu",
		Protocol: ProtocolHysteria2,
		Params:   map[string]any{"obfs": "salamander"},
	}
	if err := store.Create(context.Background(), b); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("create b: got %v, want ErrDuplicate", err)
	}
}

func TestMemoryStore_UpdatePreservesCreatedAt(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	store.SetClock(fixedClock())

	a := &InboundTemplate{
		ID:       uuid.New(),
		Name:     "vless-reality-eu",
		Protocol: ProtocolVLESS,
		Params:   map[string]any{"flow": "xtls-rprx-vision"},
	}
	if err := store.Create(context.Background(), a); err != nil {
		t.Fatalf("create: %v", err)
	}
	createdAt := a.CreatedAt
	updatedAt := a.UpdatedAt

	// Advance the clock to verify UpdatedAt bumps
	// while CreatedAt is preserved.
	store.SetClock(func() time.Time { return createdAt.Add(time.Hour) })
	a.Name = "vless-reality-eu-renamed"
	if err := store.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !a.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt changed: %s -> %s", createdAt, a.CreatedAt)
	}
	if !a.UpdatedAt.After(updatedAt) {
		t.Fatalf("UpdatedAt not bumped: %s <= %s", a.UpdatedAt, updatedAt)
	}
}

func TestService_CreateValidatesName(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	_, err := s.Create(context.Background(), CreateInput{
		Name:     "",
		Protocol: ProtocolVLESS,
		Params:   map[string]any{},
	})
	var vErr *ValidationError
	if !errors.As(err, &vErr) || vErr.Field != "name" {
		t.Fatalf("expected ValidationError on name, got %v", err)
	}
}

func TestService_CreateValidatesProtocol(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	_, err := s.Create(context.Background(), CreateInput{
		Name:     "wireguard",
		Protocol: Protocol("wireguard"),
		Params:   map[string]any{},
	})
	var vErr *ValidationError
	if !errors.As(err, &vErr) || vErr.Field != "protocol" {
		t.Fatalf("expected ValidationError on protocol, got %v", err)
	}
}

func TestService_UpdatePartialPatch(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	tpl, err := s.Create(context.Background(), CreateInput{
		Name:     "vless-reality-eu",
		Protocol: ProtocolVLESS,
		Params:   map[string]any{"flow": "xtls-rprx-vision"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Patch only the description; everything else
	// is unchanged.
	newDesc := "renamed for the new EU node"
	updated, err := s.Update(context.Background(), tpl.ID, UpdateInput{
		Description: &newDesc,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Description != newDesc {
		t.Fatalf("description: got %q, want %q", updated.Description, newDesc)
	}
	if updated.Name != tpl.Name {
		t.Fatalf("name changed unexpectedly: %q vs %q", updated.Name, tpl.Name)
	}
	if updated.Protocol != tpl.Protocol {
		t.Fatalf("protocol changed unexpectedly: %q vs %q", updated.Protocol, tpl.Protocol)
	}
}

func TestService_DeleteFreesNameForReuse(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	a, err := s.Create(context.Background(), CreateInput{
		Name:     "vless-reality-eu",
		Protocol: ProtocolVLESS,
		Params:   map[string]any{},
	})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := s.Delete(context.Background(), a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Same name should now be free.
	_, err = s.Create(context.Background(), CreateInput{
		Name:     "vless-reality-eu",
		Protocol: ProtocolHysteria2,
		Params:   map[string]any{},
	})
	if err != nil {
		t.Fatalf("recreate after delete: %v", err)
	}
}

func TestMemoryStore_CloneParamsIsolation(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	store.SetClock(fixedClock())

	src := &InboundTemplate{
		ID:       uuid.New(),
		Name:     "src",
		Protocol: ProtocolVLESS,
		Params:   map[string]any{"flow": "xtls-rprx-vision"},
	}
	if err := store.Create(context.Background(), src); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Mutate the original after create; the
	// stored copy must not see the mutation.
	src.Params["flow"] = "MUTATED"
	got, err := store.GetByID(context.Background(), src.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v, _ := got.Params["flow"].(string); v != "xtls-rprx-vision" {
		t.Fatalf("store saw mutation: flow=%q", v)
	}
}

func TestMemoryStore_ConcurrentReadWrite(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	store.SetClock(fixedClock())

	// Pre-populate.
	for i := 0; i < 10; i++ {
		if err := store.Create(context.Background(), &InboundTemplate{
			ID:       uuid.New(),
			Name:     nameFromIndex(i),
			Protocol: ProtocolVLESS,
			Params:   map[string]any{},
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	const goroutines = 16
	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_, _ = store.List(context.Background())
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = store.Create(context.Background(), &InboundTemplate{
					ID:       uuid.New(),
					Name:     nameFromIndex(100 + i%50),
					Protocol: ProtocolVLESS,
					Params:   map[string]any{},
				})
			}
		}()
	}
	wg.Wait()
}

func nameFromIndex(i int) string {
	return "tpl-" + strings.Repeat("x", i%5+1) + "-" + uuid.NewString()[:8]
}
