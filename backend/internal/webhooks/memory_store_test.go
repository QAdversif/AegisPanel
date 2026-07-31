// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemoryStore_CreateAndGetEndpoint(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	e := &Endpoint{
		ID:      uuid.New(),
		URL:     "https://example.com/hook",
		Secret:  "webhook-fixture-secret-cccccccccccccccccccccccc",
		Events:  []EventType{EventUserCreated},
		Enabled: true,
	}
	if err := store.CreateEndpoint(context.Background(), e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	got, err := store.GetEndpoint(context.Background(), e.ID)
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if got.URL != e.URL {
		t.Errorf("URL = %q, want %q", got.URL, e.URL)
	}
	if got.Secret != e.Secret {
		t.Errorf("Secret mismatch")
	}
	if len(got.Events) != 1 || got.Events[0] != EventUserCreated {
		t.Errorf("Events = %v, want [%s]", got.Events, EventUserCreated)
	}
}

func TestMemoryStore_GetEndpoint_NotFound(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	_, err := store.GetEndpoint(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_DuplicateURL(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	e1 := &Endpoint{ID: uuid.New(), URL: "https://a.example.com/h", Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Events: nil, Enabled: true}
	e2 := &Endpoint{ID: uuid.New(), URL: "https://a.example.com/h", Secret: "webhook-fixture-secret-bbbbbbbbbbbbbbbbbbbbbbbbbb", Events: nil, Enabled: true}
	if err := store.CreateEndpoint(context.Background(), e1); err != nil {
		t.Fatalf("CreateEndpoint e1: %v", err)
	}
	err := store.CreateEndpoint(context.Background(), e2)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}

func TestMemoryStore_UpdateEndpoint_RenameURL(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	e := &Endpoint{ID: uuid.New(), URL: "https://a.example.com/h", Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Enabled: true}
	if err := store.CreateEndpoint(context.Background(), e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	e.URL = "https://b.example.com/h"
	if err := store.UpdateEndpoint(context.Background(), e); err != nil {
		t.Fatalf("UpdateEndpoint: %v", err)
	}
	// Re-fetch and check.
	got, err := store.GetEndpoint(context.Background(), e.ID)
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if got.URL != "https://b.example.com/h" {
		t.Errorf("URL = %q, want https://b.example.com/h", got.URL)
	}
}

func TestMemoryStore_UpdateEndpoint_URLCollision(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	e1 := &Endpoint{ID: uuid.New(), URL: "https://a.example.com/h", Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Enabled: true}
	e2 := &Endpoint{ID: uuid.New(), URL: "https://b.example.com/h", Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Enabled: true}
	if err := store.CreateEndpoint(context.Background(), e1); err != nil {
		t.Fatalf("CreateEndpoint e1: %v", err)
	}
	if err := store.CreateEndpoint(context.Background(), e2); err != nil {
		t.Fatalf("CreateEndpoint e2: %v", err)
	}
	// Try to rename e2 to e1's URL — should ErrDuplicate.
	e2.URL = e1.URL
	err := store.UpdateEndpoint(context.Background(), e2)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}

func TestMemoryStore_DeleteEndpoint_CascadesDeliveries(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	e := &Endpoint{ID: uuid.New(), URL: "https://a.example.com/h", Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Enabled: true}
	if err := store.CreateEndpoint(context.Background(), e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	d := &Delivery{ID: uuid.New(), EndpointID: e.ID, EventType: EventUserCreated, Attempt: 1}
	if err := store.CreateDelivery(context.Background(), d); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	if err := store.DeleteEndpoint(context.Background(), e.ID); err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}
	// Endpoint gone.
	if _, err := store.GetEndpoint(context.Background(), e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// Delivery cascade-deleted.
	deliveries, err := store.ListDeliveriesByEndpoint(context.Background(), e.ID, 0)
	if err != nil {
		t.Fatalf("ListDeliveriesByEndpoint: %v", err)
	}
	if len(deliveries) != 0 {
		t.Errorf("expected 0 deliveries after cascade, got %d", len(deliveries))
	}
}

func TestMemoryStore_ListEndpoints_Ordering(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	now := timeStub()
	store.SetClock(now)
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, id := range ids {
		e := &Endpoint{
			ID:        id,
			URL:       "https://e" + string(rune('a'+i)) + ".example.com/h",
			Secret:    "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
			CreatedAt: now(),
			UpdatedAt: now(),
		}
		if err := store.CreateEndpoint(context.Background(), e); err != nil {
			t.Fatalf("CreateEndpoint %d: %v", i, err)
		}
		// Bump the clock so each row gets a
		// distinct CreatedAt.
		now = clockAdvance(now, time.Millisecond)
		store.SetClock(now)
	}
	out, err := store.ListEndpoints(context.Background())
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 endpoints, got %d", len(out))
	}
	for i, want := range ids {
		if out[i].ID != want {
			t.Errorf("out[%d].ID = %s, want %s", i, out[i].ID, want)
		}
	}
}

func TestMemoryStore_DLQ_RoundTrip(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	entry := &DLQEntry{
		ID:          uuid.New(),
		EndpointID:  uuid.New(),
		EndpointURL: "https://a.example.com/h",
		EventType:   EventUserCreated,
		Payload:     []byte(`{"hello":"world"}`),
		LastError:   "http 500",
		Attempts:    6,
	}
	if err := store.EnqueueDLQ(context.Background(), entry); err != nil {
		t.Fatalf("EnqueueDLQ: %v", err)
	}
	got, err := store.GetDLQ(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("GetDLQ: %v", err)
	}
	if got.LastError != "http 500" {
		t.Errorf("LastError = %q, want %q", got.LastError, "http 500")
	}
	if string(got.Payload) != `{"hello":"world"}` {
		t.Errorf("Payload = %q", got.Payload)
	}
	if err := store.DeleteDLQ(context.Background(), entry.ID); err != nil {
		t.Fatalf("DeleteDLQ: %v", err)
	}
	if _, err := store.GetDLQ(context.Background(), entry.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestMemoryStore_DeliveryDefensiveCopy(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	e := &Endpoint{ID: uuid.New(), URL: "https://a.example.com/h", Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Enabled: true}
	if err := store.CreateEndpoint(context.Background(), e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	original := []byte(`{"k":"v"}`)
	d := &Delivery{ID: uuid.New(), EndpointID: e.ID, EventType: EventUserCreated, Payload: original, RequestBody: original, Attempt: 1}
	if err := store.CreateDelivery(context.Background(), d); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	// Mutate the slice the caller still holds.
	original[0] = 'X'
	deliveries, err := store.ListDeliveriesByEndpoint(context.Background(), e.ID, 0)
	if err != nil {
		t.Fatalf("ListDeliveriesByEndpoint: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(deliveries))
	}
	if !strings.HasPrefix(string(deliveries[0].Payload), `{"k"`) {
		t.Errorf("payload was mutated through caller reference: %q", deliveries[0].Payload)
	}
}

// --- test helpers ------------------------------------------------------

// timeStub returns a clock that increments by 1ms
// per call. The MemoryStore uses the clock to
// stamp CreatedAt / UpdatedAt; tests that need
// deterministic ordering inject it via SetClock.
func timeStub() func() time.Time {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var n int
	return func() time.Time {
		n++
		return base.Add(time.Duration(n) * time.Millisecond)
	}
}

// clockAdvance shifts the inner base time of a
// timeStub. Used between Create calls to
// guarantee distinct CreatedAt values.
func clockAdvance(prev func() time.Time, d time.Duration) func() time.Time {
	// Capture the latest emitted time and
	// return a new stub that starts there.
	latest := prev()
	var n int
	return func() time.Time {
		n++
		return latest.Add(time.Duration(n)*time.Millisecond + d)
	}
}
