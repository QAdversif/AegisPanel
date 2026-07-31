// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build integration

// Integration tests for the pgx-backed webhook
// Store. Gated on the `integration` build tag and
// on INTEGRATION_DATABASE_URL via testutil.MustNewPool.
//
// The fixture is provided by testutil: it DROPs
// and re-CREATEs the database, then runs every
// migration in `backend/migrations/`. Each test
// truncates the three webhook tables (CASCADE so
// FK dependents are wiped too) so order does not
// matter; the test process is the only writer.

package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/testutil"
)

// newPgStore opens a fresh pgxpool via testutil
// and returns a *PgStore. The webhook tables are
// truncated (CASCADE) so the test starts from an
// empty state; the same truncate is re-applied on
// test exit.
func newPgStore(t *testing.T) *PgStore {
	t.Helper()
	pool := testutil.MustNewPool(t)
	if _, err := pool.Exec(context.Background(),
		`TRUNCATE TABLE webhook_dlq, webhook_deliveries, webhook_pending_retries, webhook_endpoints CASCADE`); err != nil {
		t.Fatalf("TRUNCATE webhook_*: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`TRUNCATE TABLE webhook_dlq, webhook_deliveries, webhook_pending_retries, webhook_endpoints CASCADE`)
	})
	return NewPgStore(pool)
}

// TestPgStore_EndpointRoundTrip covers the basic
// endpoint CRUD against the pgx-backed store.
func TestPgStore_EndpointRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	e := &Endpoint{
		ID:      uuid.New(),
		URL:     "https://pg.example.com/h",
		Secret:  "pg-webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Events:  []EventType{EventUserCreated, EventPlanCreated},
		Enabled: true,
	}
	if err := s.CreateEndpoint(ctx, e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	got, err := s.GetEndpoint(ctx, e.ID)
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if got.URL != e.URL {
		t.Errorf("URL = %q, want %q", got.URL, e.URL)
	}
	if len(got.Events) != 2 {
		t.Errorf("Events len = %d, want 2", len(got.Events))
	}
	// List.
	list, err := s.ListEndpoints(ctx)
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("ListEndpoints: len = %d, want 1", len(list))
	}
	// Delete.
	if err := s.DeleteEndpoint(ctx, e.ID); err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}
	if _, err := s.GetEndpoint(ctx, e.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetEndpoint after delete: err = %v, want ErrNotFound", err)
	}
}

// TestPgStore_DeliveryRoundTrip covers the
// delivery-history table: insert, read back,
// list per endpoint.
func TestPgStore_DeliveryRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	e := &Endpoint{
		ID:      uuid.New(),
		URL:     "https://pg2.example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}
	if err := s.CreateEndpoint(ctx, e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	sc := 200
	dm := 42
	d := &Delivery{
		ID:           uuid.New(),
		EndpointID:   e.ID,
		EventType:    EventUserCreated,
		Payload:      []byte(`{"x":1}`),
		RequestURL:   e.URL,
		RequestBody:  []byte(`{"x":1}`),
		Signature:    "sha256=deadbeef",
		StatusCode:   &sc,
		ResponseBody: "ok",
		Attempt:      1,
		DurationMs:   &dm,
	}
	if err := s.CreateDelivery(ctx, d); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	got, err := s.ListDeliveriesByEndpoint(ctx, e.ID, 0)
	if err != nil {
		t.Fatalf("ListDeliveriesByEndpoint: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(got))
	}
	jsonEqual(t, got[0].Payload, map[string]any{"x": float64(1)})
	if got[0].StatusCode == nil || *got[0].StatusCode != 200 {
		t.Errorf("StatusCode round-trip mismatch: got %v", got[0].StatusCode)
	}
}

// jsonEqual compares two JSON byte strings by
// parsing both into a generic structure. Postgres
// JSONB normalises whitespace on read-back
// (e.g. `{"x":1}` -> `{"x": 1}`), so byte-level
// comparison is unreliable; the parsed values
// must match instead.
func jsonEqual(t *testing.T, raw []byte, want any) {
	t.Helper()
	var got any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v (raw=%q)", err, raw)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Payload mismatch: got %v, want %v (raw=%q)", got, want, raw)
	}
}

// TestPgStore_DLQRoundTrip covers the DLQ table:
// enqueue, read back, list.
func TestPgStore_DLQRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	entry := &DLQEntry{
		ID:            uuid.New(),
		EndpointID:    uuid.New(),
		EndpointURL:   "https://dlq.example.com/h",
		EventType:     EventUserCreated,
		Payload:       []byte(`{"a":1}`),
		LastError:     "http 500",
		Attempts:      6,
		LastAttemptAt: time.Now().UTC(),
	}
	if err := s.EnqueueDLQ(ctx, entry); err != nil {
		t.Fatalf("EnqueueDLQ: %v", err)
	}
	got, err := s.GetDLQ(ctx, entry.ID)
	if err != nil {
		t.Fatalf("GetDLQ: %v", err)
	}
	if got.LastError != "http 500" {
		t.Errorf("LastError = %q, want %q", got.LastError, "http 500")
	}
	jsonEqual(t, got.Payload, map[string]any{"a": float64(1)})
	// List.
	list, err := s.ListDLQ(ctx, 0)
	if err != nil {
		t.Fatalf("ListDLQ: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("ListDLQ: len = %d, want 1", len(list))
	}
	// Delete.
	if err := s.DeleteDLQ(ctx, entry.ID); err != nil {
		t.Fatalf("DeleteDLQ: %v", err)
	}
	if _, err := s.GetDLQ(ctx, entry.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetDLQ after delete: err = %v, want ErrNotFound", err)
	}
}

// TestPgStore_EndpointDuplicateURL covers the
// (url) UNIQUE constraint. Two endpoints with the
// same URL must fail with ErrDuplicate (Postgres
// SQLSTATE 23505).
func TestPgStore_EndpointDuplicateURL(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	url := "https://dup.example.com/h"
	e1 := &Endpoint{ID: uuid.New(), URL: url, Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Enabled: true}
	e2 := &Endpoint{ID: uuid.New(), URL: url, Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Enabled: true}
	if err := s.CreateEndpoint(ctx, e1); err != nil {
		t.Fatalf("CreateEndpoint e1: %v", err)
	}
	err := s.CreateEndpoint(ctx, e2)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}

// TestPgStore_DeleteEndpointCascades covers the
// (endpoint_id) FK on webhook_deliveries with ON
// DELETE CASCADE. Deleting an endpoint must
// remove its delivery history.
func TestPgStore_DeleteEndpointCascades(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	e := &Endpoint{ID: uuid.New(), URL: "https://cascade.example.com/h", Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Enabled: true}
	if err := s.CreateEndpoint(ctx, e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	d := &Delivery{ID: uuid.New(), EndpointID: e.ID, EventType: EventUserCreated, Attempt: 1, Payload: []byte(`{}`), RequestBody: []byte(`{}`)}
	if err := s.CreateDelivery(ctx, d); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	if err := s.DeleteEndpoint(ctx, e.ID); err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}
	got, err := s.ListDeliveriesByEndpoint(ctx, e.ID, 0)
	if err != nil {
		t.Fatalf("ListDeliveriesByEndpoint: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 deliveries after cascade, got %d", len(got))
	}
}

// --- pending retries (v0.7.x) ----------------------------------------

// TestPgStore_PendingRetries_RoundTrip covers the
// enqueue / dequeue / list-due flow against the
// pgx-backed store. The migration 0017 must have
// created the `webhook_pending_retries` table;
// testutil runs every migration on MustNewPool.
func TestPgStore_PendingRetries_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	now := time.Now().UTC()
	d1 := uuid.New()
	d2 := uuid.New()
	d3 := uuid.New()
	if err := s.EnqueueRetry(ctx, d1, now.Add(1*time.Second)); err != nil {
		t.Fatalf("EnqueueRetry d1: %v", err)
	}
	if err := s.EnqueueRetry(ctx, d2, now.Add(5*time.Second)); err != nil {
		t.Fatalf("EnqueueRetry d2: %v", err)
	}
	if err := s.EnqueueRetry(ctx, d3, now.Add(1*time.Hour)); err != nil {
		t.Fatalf("EnqueueRetry d3: %v", err)
	}
	// At `now+10s`, d1 and d2 are due; d3 is not.
	due, err := s.ListDueRetries(ctx, now.Add(10*time.Second), 0)
	if err != nil {
		t.Fatalf("ListDueRetries: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("expected 2 due rows, got %d", len(due))
	}
	// Ordered by next_attempt_at asc: d1 then d2.
	if due[0] != d1 || due[1] != d2 {
		t.Errorf("ListDueRetries = [%s, %s], want [%s, %s]", due[0], due[1], d1, d2)
	}
	// Dequeue d1 and verify it falls out of the
	// list.
	if err := s.DequeueRetry(ctx, d1); err != nil {
		t.Fatalf("DequeueRetry d1: %v", err)
	}
	due, err = s.ListDueRetries(ctx, now.Add(10*time.Second), 0)
	if err != nil {
		t.Fatalf("ListDueRetries #2: %v", err)
	}
	if len(due) != 1 || due[0] != d2 {
		t.Errorf("after Dequeue: due = %v, want [%s]", due, d2)
	}
	// Idempotent: re-dequeueing d1 is a no-op.
	if err := s.DequeueRetry(ctx, d1); err != nil {
		t.Errorf("DequeueRetry idempotent: %v", err)
	}
}

// TestPgStore_EnqueueRetry_UpdatesOnConflict
// verifies the ON CONFLICT DO UPDATE semantic:
// re-enqueueing the same delivery_id updates the
// scheduled time in place.
func TestPgStore_EnqueueRetry_UpdatesOnConflict(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	d := uuid.New()
	t1 := time.Now().UTC()
	t2 := t1.Add(10 * time.Second)
	if err := s.EnqueueRetry(ctx, d, t1); err != nil {
		t.Fatalf("EnqueueRetry #1: %v", err)
	}
	if err := s.EnqueueRetry(ctx, d, t2); err != nil {
		t.Fatalf("EnqueueRetry #2: %v", err)
	}
	// At t1+5s, the original schedule (t1) is
	// already past — but the upsert replaced it
	// with t2, which is 10s in the future. So
	// the row is NOT due at t1+5s.
	got, err := s.ListDueRetries(ctx, t1.Add(5*time.Second), 0)
	if err != nil {
		t.Fatalf("ListDueRetries: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 due at t1+5s, got %d", len(got))
	}
	// At t2+1s, the row IS due.
	got, err = s.ListDueRetries(ctx, t2.Add(1*time.Second), 0)
	if err != nil {
		t.Fatalf("ListDueRetries #2: %v", err)
	}
	if len(got) != 1 || got[0] != d {
		t.Errorf("ListDueRetries at t2+1s = %v, want [%s]", got, d)
	}
}

// TestPgStore_EndpointDeleteCascades_PendingRetries
// verifies the ON DELETE CASCADE on the
// (delivery_id) FK: removing the underlying
// delivery row removes the pending retry too.
// The pg path is the production one; the
// in-memory store has no cascade (it leaves
// dangling rows that the worker logs and
// skips via Service.RetryDelivery returning
// ErrNotFound).
func TestPgStore_EndpointDeleteCascades_PendingRetries(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	e := &Endpoint{ID: uuid.New(), URL: "https://cascade2.example.com/h", Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Enabled: true}
	if err := s.CreateEndpoint(ctx, e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	d := &Delivery{ID: uuid.New(), EndpointID: e.ID, EventType: EventUserCreated, Attempt: 1, Payload: []byte(`{}`), RequestBody: []byte(`{}`)}
	if err := s.CreateDelivery(ctx, d); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	if err := s.EnqueueRetry(ctx, d.ID, time.Now().UTC()); err != nil {
		t.Fatalf("EnqueueRetry: %v", err)
	}
	if err := s.DeleteEndpoint(ctx, e.ID); err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}
	// The pending-retry row cascade-deleted
	// with the underlying delivery.
	got, err := s.ListDueRetries(ctx, time.Now().UTC(), 0)
	if err != nil {
		t.Fatalf("ListDueRetries: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 pending retries after cascade, got %d", len(got))
	}
}
