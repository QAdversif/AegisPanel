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

// --- pending retries (v0.7.x) ----------------------------------------

func TestMemoryStore_EnqueueRetry_Idempotent(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	d1 := uuid.New()
	t1 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if err := store.EnqueueRetry(context.Background(), d1, t1); err != nil {
		t.Fatalf("EnqueueRetry #1: %v", err)
	}
	// Re-enqueue the same id with a new
	// timestamp — the (delivery_id) PK upsert
	// updates the row in place.
	t2 := t1.Add(10 * time.Second)
	if err := store.EnqueueRetry(context.Background(), d1, t2); err != nil {
		t.Fatalf("EnqueueRetry #2: %v", err)
	}
	// ListDueRetries at t1+5s must return the row
	// (because t2 is in the future). At t1+15s
	// the row is due.
	before := time.Date(2026, 7, 1, 12, 0, 5, 0, time.UTC)
	gotBefore, err := store.ListDueRetries(context.Background(), before, 0)
	if err != nil {
		t.Fatalf("ListDueRetries before: %v", err)
	}
	if len(gotBefore) != 0 {
		t.Errorf("expected 0 due rows at t1+5s, got %d", len(gotBefore))
	}
	after := time.Date(2026, 7, 1, 12, 0, 15, 0, time.UTC)
	gotAfter, err := store.ListDueRetries(context.Background(), after, 0)
	if err != nil {
		t.Fatalf("ListDueRetries after: %v", err)
	}
	if len(gotAfter) != 1 || gotAfter[0] != d1 {
		t.Errorf("ListDueRetries after = %v, want [%s]", gotAfter, d1)
	}
}

func TestMemoryStore_DequeueRetry_Idempotent(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	d1 := uuid.New()
	// Dequeue on a missing id must succeed.
	if err := store.DequeueRetry(context.Background(), d1); err != nil {
		t.Errorf("DequeueRetry on missing id: %v", err)
	}
	// Enqueue then dequeue.
	if err := store.EnqueueRetry(context.Background(), d1, time.Now().UTC()); err != nil {
		t.Fatalf("EnqueueRetry: %v", err)
	}
	if err := store.DequeueRetry(context.Background(), d1); err != nil {
		t.Errorf("DequeueRetry: %v", err)
	}
	// Re-dequeue must still be a no-op.
	if err := store.DequeueRetry(context.Background(), d1); err != nil {
		t.Errorf("DequeueRetry #2: %v", err)
	}
	now := time.Now().UTC()
	got, err := store.ListDueRetries(context.Background(), now, 0)
	if err != nil {
		t.Fatalf("ListDueRetries: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 due rows after dequeue, got %d", len(got))
	}
}

func TestMemoryStore_ListDueRetries_Ordering(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	// Three rows with distinct next_attempt_at.
	// We want the ListDueRetries output ordered
	// by time asc, then by id asc on tie.
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	d1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	d2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	d3 := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	// d2 is due first, d1 second, d3 in the future.
	if err := store.EnqueueRetry(context.Background(), d1, base.Add(5*time.Second)); err != nil {
		t.Fatalf("EnqueueRetry d1: %v", err)
	}
	if err := store.EnqueueRetry(context.Background(), d2, base.Add(1*time.Second)); err != nil {
		t.Fatalf("EnqueueRetry d2: %v", err)
	}
	if err := store.EnqueueRetry(context.Background(), d3, base.Add(60*time.Second)); err != nil {
		t.Fatalf("EnqueueRetry d3: %v", err)
	}
	// now = base + 10s — d1 and d2 are due, d3
	// is not.
	now := base.Add(10 * time.Second)
	got, err := store.ListDueRetries(context.Background(), now, 0)
	if err != nil {
		t.Fatalf("ListDueRetries: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 due rows, got %d", len(got))
	}
	if got[0] != d2 || got[1] != d1 {
		t.Errorf("ListDueRetries = [%s, %s], want [%s, %s]",
			got[0], got[1], d2, d1)
	}
}

func TestMemoryStore_ListDueRetries_LimitClamping(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	// Enqueue 5 due rows.
	for i := 0; i < 5; i++ {
		if err := store.EnqueueRetry(context.Background(), uuid.New(), now); err != nil {
			t.Fatalf("EnqueueRetry #%d: %v", i, err)
		}
	}
	// limit=0 returns all 5 (default 100).
	got0, err := store.ListDueRetries(context.Background(), now, 0)
	if err != nil {
		t.Fatalf("ListDueRetries(0): %v", err)
	}
	if len(got0) != 5 {
		t.Errorf("ListDueRetries(0) = %d, want 5", len(got0))
	}
	// limit=2 returns 2.
	got2, err := store.ListDueRetries(context.Background(), now, 2)
	if err != nil {
		t.Fatalf("ListDueRetries(2): %v", err)
	}
	if len(got2) != 2 {
		t.Errorf("ListDueRetries(2) = %d, want 2", len(got2))
	}
	// limit=MaxListLimit+1 clamps to MaxListLimit;
	// we have 5 rows so the result is 5.
	gotBig, err := store.ListDueRetries(context.Background(), now, MaxListLimit+1)
	if err != nil {
		t.Fatalf("ListDueRetries(big): %v", err)
	}
	if len(gotBig) != 5 {
		t.Errorf("ListDueRetries(big) = %d, want 5", len(gotBig))
	}
}

func TestMemoryStore_EndpointDelete_CascadesPendingRetries(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	e := &Endpoint{
		ID:      uuid.New(),
		URL:     "https://a.example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}
	if err := store.CreateEndpoint(context.Background(), e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	d := &Delivery{ID: uuid.New(), EndpointID: e.ID, EventType: EventUserCreated, Attempt: 1}
	if err := store.CreateDelivery(context.Background(), d); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	// The in-memory store does not auto-cascade
	// pending retries on endpoint delete (only
	// deliveries). But the worker is robust to
	// dangling ids: ListDueRetries will surface
	// them and Service.RetryDelivery returns
	// ErrNotFound, which is logged and skipped.
	// We document the expected in-memory
	// behaviour here.
	if err := store.EnqueueRetry(context.Background(), d.ID, time.Now().UTC()); err != nil {
		t.Fatalf("EnqueueRetry: %v", err)
	}
	if err := store.DeleteEndpoint(context.Background(), e.ID); err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}
	got, err := store.ListDueRetries(context.Background(), time.Now().UTC(), 0)
	if err != nil {
		t.Fatalf("ListDueRetries: %v", err)
	}
	// In-memory store leaves the row in the
	// queue (no cascade). The pg store cascades
	// via ON DELETE CASCADE on the FK.
	if len(got) != 1 {
		t.Errorf("expected 1 dangling retry, got %d", len(got))
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
