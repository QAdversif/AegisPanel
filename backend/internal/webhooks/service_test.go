// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeHTTPDoer is a stub HTTP client that records
// every request and returns a scripted response.
// The Service calls Do(req) once per delivery.
type fakeHTTPDoer struct {
	mu        sync.Mutex
	Requests  []*http.Request
	Bodies    [][]byte
	Responses []fakeResponse
	// Default is the response returned when the
	// script is exhausted.
	Default *fakeResponse
	// FailWith is a transport error returned
	// when non-nil (overrides Responses).
	FailWith error
}

type fakeResponse struct {
	StatusCode int
	Body       string
}

func (f *fakeHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(req.Body)
	f.Requests = append(f.Requests, req)
	f.Bodies = append(f.Bodies, body)
	if f.FailWith != nil {
		return nil, f.FailWith
	}
	var resp fakeResponse
	if len(f.Responses) > 0 {
		resp = f.Responses[0]
		if len(f.Responses) > 1 {
			f.Responses = f.Responses[1:]
		}
	} else if f.Default != nil {
		resp = *f.Default
	} else {
		resp = fakeResponse{StatusCode: 200, Body: "ok"}
	}
	return &http.Response{
		StatusCode: resp.StatusCode,
		Body:       io.NopCloser(strings.NewReader(resp.Body)),
		Header:     http.Header{},
	}, nil
}

// --- Service.Create / Update / Delete / Get -----------------------------

func TestService_Create_ValidatesURL(t *testing.T) {
	t.Parallel()
	svc := NewService(NewMemoryStore())
	_, err := svc.Create(context.Background(), CreateInput{
		URL:     "ftp://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Events:  nil,
		Enabled: true,
	})
	var vErr *ValidationError
	if !errors.As(err, &vErr) || vErr.Field != "url" {
		t.Fatalf("expected ValidationError on url, got %v", err)
	}
}

func TestService_Create_ValidatesSecret(t *testing.T) {
	t.Parallel()
	svc := NewService(NewMemoryStore())
	_, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "short",
		Enabled: true,
	})
	var vErr *ValidationError
	if !errors.As(err, &vErr) || vErr.Field != "secret" {
		t.Fatalf("expected ValidationError on secret, got %v", err)
	}
}

func TestService_Create_ValidatesEventType(t *testing.T) {
	t.Parallel()
	svc := NewService(NewMemoryStore())
	_, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Events:  []EventType{"unknown.event"},
		Enabled: true,
	})
	var vErr *ValidationError
	if !errors.As(err, &vErr) || vErr.Field != "events" {
		t.Fatalf("expected ValidationError on events, got %v", err)
	}
}

func TestService_Create_AssignsIDAndTimestamps(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	now := timeStub()
	store.SetClock(now)
	svc := NewService(store)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ep.ID == uuid.Nil {
		t.Errorf("expected non-zero ID")
	}
	if ep.CreatedAt.IsZero() || ep.UpdatedAt.IsZero() {
		t.Errorf("expected non-zero timestamps")
	}
}

func TestService_Get_ZeroID(t *testing.T) {
	t.Parallel()
	svc := NewService(NewMemoryStore())
	_, err := svc.Get(context.Background(), uuid.Nil)
	var vErr *ValidationError
	if !errors.As(err, &vErr) || vErr.Field != "id" {
		t.Fatalf("expected ValidationError on id, got %v", err)
	}
}

func TestService_Update_PatchSemantics(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	newSecret := "rotated-webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa"
	disabled := false
	upd, err := svc.Update(context.Background(), ep.ID, UpdateInput{
		Secret:  &newSecret,
		Enabled: &disabled,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if upd.Secret != newSecret {
		t.Errorf("Secret = %q, want %q", upd.Secret, newSecret)
	}
	if upd.Enabled {
		t.Errorf("Enabled = true, want false")
	}
	if upd.URL != ep.URL {
		t.Errorf("URL should be unchanged; got %q, want %q", upd.URL, ep.URL)
	}
}

func TestService_Delete_RemovesEndpoint(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(context.Background(), ep.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(context.Background(), ep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

// --- Service.Dispatch --------------------------------------------------

func TestService_Dispatch_RejectsUnknownEvent(t *testing.T) {
	t.Parallel()
	svc := NewService(NewMemoryStore())
	_, err := svc.Dispatch(context.Background(), "unknown.event", map[string]any{"a": 1})
	var vErr *ValidationError
	if !errors.As(err, &vErr) || vErr.Field != "event_type" {
		t.Fatalf("expected ValidationError on event_type, got %v", err)
	}
}

func TestService_Dispatch_RejectsNilPayload(t *testing.T) {
	t.Parallel()
	svc := NewService(NewMemoryStore())
	_, err := svc.Dispatch(context.Background(), EventUserCreated, nil)
	var vErr *ValidationError
	if !errors.As(err, &vErr) || vErr.Field != "payload" {
		t.Fatalf("expected ValidationError on payload, got %v", err)
	}
}

func TestService_Dispatch_HappyPath(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{
		Default: &fakeResponse{StatusCode: 200, Body: "ok"},
	}
	svc.SetHTTPClient(fake)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != DeliveryStatusSuccess {
		t.Errorf("Status = %s, want success", results[0].Status)
	}
	if results[0].StatusCode == nil || *results[0].StatusCode != 200 {
		t.Errorf("StatusCode = %v, want 200", results[0].StatusCode)
	}
	// The fake recorded the request.
	if len(fake.Requests) != 1 {
		t.Fatalf("expected 1 HTTP request, got %d", len(fake.Requests))
	}
	req := fake.Requests[0]
	if req.Header.Get("X-Aegis-Event") != string(EventUserCreated) {
		t.Errorf("X-Aegis-Event = %q, want %q", req.Header.Get("X-Aegis-Event"), EventUserCreated)
	}
	if req.Header.Get("X-Aegis-Signature") == "" {
		t.Errorf("X-Aegis-Signature header missing")
	}
	// Verify the signature over the recorded body.
	if err := Verify(fake.Bodies[0], ep.Secret, req.Header.Get("X-Aegis-Signature")); err != nil {
		t.Errorf("Verify: %v", err)
	}
	// The body is valid JSON.
	var got map[string]any
	if err := json.Unmarshal(fake.Bodies[0], &got); err != nil {
		t.Errorf("body is not valid JSON: %v (raw=%q)", err, fake.Bodies[0])
	}
}

func TestService_Dispatch_FiltersByEvent(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200}}
	svc.SetHTTPClient(fake)
	// Endpoint subscribes only to plan events.
	if _, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Events:  []EventType{EventPlanCreated},
		Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Dispatch a user event — endpoint must NOT
	// receive it.
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "" {
		t.Errorf("Status = %s, want empty (endpoint was filtered out)", results[0].Status)
	}
	if len(fake.Requests) != 0 {
		t.Errorf("expected 0 HTTP requests (endpoint filtered), got %d", len(fake.Requests))
	}
}

func TestService_Dispatch_SkipsDisabled(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200}}
	svc.SetHTTPClient(fake)
	if _, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: false,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "" {
		t.Errorf("Status = %s, want empty (endpoint disabled)", results[0].Status)
	}
}

func TestService_Dispatch_TransportError(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{FailWith: errors.New("connection refused")}
	svc.SetHTTPClient(fake)
	if _, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != DeliveryStatusRetry {
		t.Errorf("Status = %s, want retry (transport error)", results[0].Status)
	}
	if !strings.Contains(results[0].Error, "connection refused") {
		t.Errorf("Error = %q, want substring %q", results[0].Error, "connection refused")
	}
}

func TestService_Dispatch_Non2xxStaysInRetry(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 503, Body: "down"}}
	svc.SetHTTPClient(fake)
	if _, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if results[0].Status != DeliveryStatusRetry {
		t.Errorf("Status = %s, want retry (non-2xx, attempts remaining)", results[0].Status)
	}
	if results[0].StatusCode == nil || *results[0].StatusCode != 503 {
		t.Errorf("StatusCode = %v, want 503", results[0].StatusCode)
	}
}

// --- Service.RetryDelivery / ReplayDLQEntry ----------------------------

func TestService_RetryDelivery_AdvancesAttempt(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200}}
	svc.SetHTTPClient(fake)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// First dispatch with 503 so the delivery
	// stays in retry state.
	fake.Responses = []fakeResponse{{StatusCode: 503}}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if results[0].Status != DeliveryStatusRetry {
		t.Fatalf("expected Retry status, got %s", results[0].Status)
	}
	firstDeliveryID := results[0].DeliveryID
	// Now retry — fake is back to 200.
	fake.Responses = nil
	retry, err := svc.RetryDelivery(context.Background(), firstDeliveryID)
	if err != nil {
		t.Fatalf("RetryDelivery: %v", err)
	}
	if retry.Status != DeliveryStatusSuccess {
		t.Errorf("Retry status = %s, want success", retry.Status)
	}
	if retry.Attempts != 2 {
		t.Errorf("Retry Attempts = %d, want 2", retry.Attempts)
	}
	// The endpoint URL is unchanged.
	if ep.URL == "" {
		t.Errorf("endpoint URL became empty")
	}
}

func TestService_RetryDelivery_MaxAttemptsBlocked(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200}}
	svc.SetHTTPClient(fake)
	if _, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Insert a delivery that has already hit
	// MaxAttempts.
	d := &Delivery{
		ID:          uuid.New(),
		EndpointID:  store.endpoints[mustFirstID(t, store)].ID,
		EventType:   EventUserCreated,
		Payload:     []byte(`{"a":1}`),
		RequestBody: []byte(`{"a":1}`),
		Attempt:     MaxAttempts,
	}
	if err := store.CreateDelivery(context.Background(), d); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	_, err := svc.RetryDelivery(context.Background(), d.ID)
	var vErr *ValidationError
	if !errors.As(err, &vErr) || vErr.Field != "delivery_id" {
		t.Fatalf("expected ValidationError on delivery_id, got %v", err)
	}
}

func TestService_ReplayDLQEntry_ResignsAndDispatches(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200}}
	svc.SetHTTPClient(fake)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	entry := &DLQEntry{
		ID:            uuid.New(),
		EndpointID:    ep.ID,
		EndpointURL:   ep.URL,
		EventType:     EventUserCreated,
		Payload:       []byte(`{"id":42}`),
		LastError:     "http 500",
		Attempts:      6,
		LastAttemptAt: time.Now().UTC(),
	}
	if err := store.EnqueueDLQ(context.Background(), entry); err != nil {
		t.Fatalf("EnqueueDLQ: %v", err)
	}
	result, err := svc.ReplayDLQEntry(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("ReplayDLQEntry: %v", err)
	}
	if result.Status != DeliveryStatusSuccess {
		t.Errorf("Status = %s, want success", result.Status)
	}
	// The fake recorded one request, signed
	// with the live secret.
	if len(fake.Requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(fake.Requests))
	}
	if err := Verify(fake.Bodies[0], ep.Secret, fake.Requests[0].Header.Get("X-Aegis-Signature")); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestService_ReplayDLQEntry_EndpointDeleted(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	entry := &DLQEntry{
		ID:            uuid.New(),
		EndpointID:    ep.ID,
		EndpointURL:   ep.URL,
		EventType:     EventUserCreated,
		Payload:       []byte(`{"id":42}`),
		LastError:     "http 500",
		Attempts:      6,
		LastAttemptAt: time.Now().UTC(),
	}
	if err := store.EnqueueDLQ(context.Background(), entry); err != nil {
		t.Fatalf("EnqueueDLQ: %v", err)
	}
	// Delete the endpoint — replay must surface
	// a clear validation error (the secret is
	// not stored on the DLQ row).
	if err := svc.Delete(context.Background(), ep.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = svc.ReplayDLQEntry(context.Background(), entry.ID)
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}

// --- Service.SendTestEvent ---------------------------------------------

func TestService_SendTestEvent_DispatchesSynthetic(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200}}
	svc.SetHTTPClient(fake)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	result, err := svc.SendTestEvent(context.Background(), ep.ID)
	if err != nil {
		t.Fatalf("SendTestEvent: %v", err)
	}
	if result.Status != DeliveryStatusSuccess {
		t.Errorf("Status = %s, want success", result.Status)
	}
	// The synthetic body has the test marker.
	if !bytes.Contains(fake.Bodies[0], []byte(`"test":true`)) {
		t.Errorf("body does not contain test marker: %q", fake.Bodies[0])
	}
	// The X-Aegis-Event header is "webhook.test".
	if fake.Requests[0].Header.Get("X-Aegis-Event") != "webhook.test" {
		t.Errorf("X-Aegis-Event = %q, want webhook.test",
			fake.Requests[0].Header.Get("X-Aegis-Event"))
	}
}

// --- delivery / DLQ list APIs ------------------------------------------

func TestService_ListDeliveries_LimitClamping(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Insert 5 deliveries.
	for i := 0; i < 5; i++ {
		d := &Delivery{ID: uuid.New(), EndpointID: ep.ID, EventType: EventUserCreated, Attempt: 1}
		if err := store.CreateDelivery(context.Background(), d); err != nil {
			t.Fatalf("CreateDelivery %d: %v", i, err)
		}
	}
	// limit=2 returns 2; limit=0 returns 5
	// (default 100 > 5).
	got2, err := svc.ListDeliveries(context.Background(), ep.ID, 2)
	if err != nil {
		t.Fatalf("ListDeliveries(2): %v", err)
	}
	if len(got2) != 2 {
		t.Errorf("ListDeliveries(2) = %d rows, want 2", len(got2))
	}
	got0, err := svc.ListDeliveries(context.Background(), ep.ID, 0)
	if err != nil {
		t.Fatalf("ListDeliveries(0): %v", err)
	}
	if len(got0) != 5 {
		t.Errorf("ListDeliveries(0) = %d rows, want 5", len(got0))
	}
}

func TestService_Dispatch_FailedFinalAttemptMovesToDLQ(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 500, Body: "boom"}}
	svc.SetHTTPClient(fake)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Pre-seed a delivery at attempt=MaxAttempts
	// for this endpoint, with a body. Then call
	// RetryDelivery — the next attempt (#6)
	// fails and moves to DLQ.
	d := &Delivery{
		ID:          uuid.New(),
		EndpointID:  ep.ID,
		EventType:   EventUserCreated,
		Payload:     []byte(`{"a":1}`),
		RequestBody: []byte(`{"a":1}`),
		RequestURL:  ep.URL,
		Signature:   "sha256=00",
		Timestamp:   time.Now().UTC(),
		Attempt:     MaxAttempts, // next retry is MaxAttempts+1
	}
	// We need attempt+1 to land at MaxAttempts
	// for the DLQ move. Adjust the seed to
	// attempt = MaxAttempts - 1 so the retry
	// hits attempt=MaxAttempts.
	d.Attempt = MaxAttempts - 1
	if err := store.CreateDelivery(context.Background(), d); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	result, err := svc.RetryDelivery(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("RetryDelivery: %v", err)
	}
	if result.Status != DeliveryStatusFailed {
		t.Errorf("Status = %s, want failed (max attempts exhausted)", result.Status)
	}
	// The DLQ has one entry.
	dlq, err := svc.ListDLQ(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListDLQ: %v", err)
	}
	if len(dlq) != 1 {
		t.Fatalf("expected 1 DLQ entry, got %d", len(dlq))
	}
	if dlq[0].Attempts != MaxAttempts {
		t.Errorf("DLQ attempts = %d, want %d", dlq[0].Attempts, MaxAttempts)
	}
}

// --- helpers ------------------------------------------------------------

// mustFirstID returns the ID of the first endpoint
// in the MemoryStore. Used by tests that need to
// seed a delivery without going through the
// Service.Create path.
func mustFirstID(t *testing.T, store *MemoryStore) uuid.UUID {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	for id := range store.endpoints {
		return id
	}
	t.Fatalf("no endpoints in store")
	return uuid.Nil
}

// --- v0.7.x: enqueue/dequeue wiring in deliverSync/RetryDelivery -------

func TestService_Dispatch_Failure_EnqueuesRetry(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 503, Body: "down"}}
	svc.SetHTTPClient(fake)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if results[0].Status != DeliveryStatusRetry {
		t.Fatalf("Status = %s, want retry", results[0].Status)
	}
	now := time.Now().UTC()
	store.mu.Lock()
	ts, ok := store.pendingRetries[results[0].DeliveryID]
	store.mu.Unlock()
	if !ok {
		t.Fatalf("expected enqueue for delivery %s, got none", results[0].DeliveryID)
	}
	delta := ts.Sub(now)
	if delta < 500*time.Millisecond || delta > 1500*time.Millisecond {
		t.Errorf("next_attempt_at delta = %v, want ~1s", delta)
	}
	_ = ep
}

func TestService_Dispatch_TransportError_EnqueuesRetry(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{FailWith: errors.New("connection refused")}
	svc.SetHTTPClient(fake)
	if _, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if results[0].Status != DeliveryStatusRetry {
		t.Fatalf("Status = %s, want retry", results[0].Status)
	}
	store.mu.Lock()
	_, ok := store.pendingRetries[results[0].DeliveryID]
	store.mu.Unlock()
	if !ok {
		t.Errorf("expected 1 pending retry, got 0")
	}
}

func TestService_Dispatch_Success_DoesNotEnqueue(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200, Body: "ok"}}
	svc.SetHTTPClient(fake)
	if _, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if results[0].Status != DeliveryStatusSuccess {
		t.Fatalf("Status = %s, want success", results[0].Status)
	}
	store.mu.Lock()
	n := len(store.pendingRetries)
	store.mu.Unlock()
	if n != 0 {
		t.Errorf("expected 0 pending retries on success, got %d", n)
	}
}

func TestService_Dispatch_MaxAttemptsFail_DoesNotEnqueue(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 500, Body: "boom"}}
	svc.SetHTTPClient(fake)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Pre-seed a delivery at attempt=MaxAttempts-1
	// so the very next RetryDelivery fires
	// attempt=MaxAttempts. That attempt fails
	// (the fake returns 500), exceeds the retry
	// budget, and is moved to the DLQ. The
	// deliverSync path inside RetryDelivery must
	// NOT enqueue a further retry (the budget is
	// exhausted).
	d := &Delivery{
		ID:          uuid.New(),
		EndpointID:  ep.ID,
		EventType:   EventUserCreated,
		Payload:     []byte(`{"a":1}`),
		RequestBody: []byte(`{"a":1}`),
		RequestURL:  ep.URL,
		Signature:   "sha256=00",
		Timestamp:   time.Now().UTC(),
		Attempt:     MaxAttempts - 1,
	}
	if err := store.CreateDelivery(context.Background(), d); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	result, err := svc.RetryDelivery(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("RetryDelivery: %v", err)
	}
	if result.Status != DeliveryStatusFailed {
		t.Errorf("Status = %s, want failed (max attempts exhausted)", result.Status)
	}
	// The DLQ has one entry.
	dlq, err := store.ListDLQ(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListDLQ: %v", err)
	}
	if len(dlq) != 1 {
		t.Fatalf("expected 1 DLQ entry, got %d", len(dlq))
	}
	// No pending retries: the final attempt was
	// the budget ceiling, so the dispatcher
	// must NOT have enqueued a follow-up.
	store.mu.Lock()
	n := len(store.pendingRetries)
	store.mu.Unlock()
	if n != 0 {
		t.Errorf("expected 0 pending retries at max attempts, got %d", n)
	}
}

func TestService_RetryDelivery_DequeuesOldRetry(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200, Body: "ok"}}
	svc.SetHTTPClient(fake)
	if _, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	fake.Responses = []fakeResponse{{StatusCode: 503}}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	oldID := results[0].DeliveryID
	store.mu.Lock()
	_, ok := store.pendingRetries[oldID]
	store.mu.Unlock()
	if !ok {
		t.Fatalf("expected enqueue for delivery %s, got none", oldID)
	}
	fake.Responses = nil
	if _, err := svc.RetryDelivery(context.Background(), oldID); err != nil {
		t.Fatalf("RetryDelivery: %v", err)
	}
	store.mu.Lock()
	n := len(store.pendingRetries)
	store.mu.Unlock()
	if n != 0 {
		t.Errorf("expected 0 pending retries after success, got %d", n)
	}
}

func TestService_RetryDelivery_Failure_DequeuesOldReenqueuesNew(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{}
	svc.SetHTTPClient(fake)
	if _, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	fake.Responses = []fakeResponse{{StatusCode: 503}}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	oldID := results[0].DeliveryID
	fake.Responses = []fakeResponse{{StatusCode: 503}}
	if _, err := svc.RetryDelivery(context.Background(), oldID); err != nil {
		t.Fatalf("RetryDelivery: %v", err)
	}
	store.mu.Lock()
	n := len(store.pendingRetries)
	var newID uuid.UUID
	for id := range store.pendingRetries {
		newID = id
	}
	store.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected 1 pending retry, got %d", n)
	}
	if newID == oldID {
		t.Errorf("old id %s still in queue (should have been dequeued)", oldID)
	}
}

func TestService_ProcessDueRetries_FiresDueRows(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200, Body: "ok"}}
	svc.SetHTTPClient(fake)
	ep, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fake.Responses = []fakeResponse{{StatusCode: 503}}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	id := results[0].DeliveryID
	store.mu.Lock()
	store.pendingRetries[id] = time.Now().UTC().Add(-1 * time.Second)
	store.mu.Unlock()
	fired, err := svc.ProcessDueRetries(context.Background(), time.Now().UTC(), 0)
	if err != nil {
		t.Fatalf("ProcessDueRetries: %v", err)
	}
	if fired != 1 {
		t.Errorf("fired = %d, want 1", fired)
	}
	if len(fake.Requests) != 2 {
		t.Errorf("HTTP requests = %d, want 2", len(fake.Requests))
	}
	due, err := store.ListDueRetries(context.Background(), time.Now().UTC(), 0)
	if err != nil {
		t.Fatalf("ListDueRetries: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("expected 0 pending retries, got %d", len(due))
	}
	_ = ep
}

func TestService_ProcessDueRetries_SkipsNotDue(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200}}
	svc.SetHTTPClient(fake)
	if _, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	fake.Responses = []fakeResponse{{StatusCode: 503}}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	id := results[0].DeliveryID
	store.mu.Lock()
	store.pendingRetries[id] = time.Now().UTC().Add(1 * time.Hour)
	store.mu.Unlock()
	fired, err := svc.ProcessDueRetries(context.Background(), time.Now().UTC(), 0)
	if err != nil {
		t.Fatalf("ProcessDueRetries: %v", err)
	}
	if fired != 0 {
		t.Errorf("fired = %d, want 0 (row not due)", fired)
	}
	store.mu.Lock()
	n := len(store.pendingRetries)
	store.mu.Unlock()
	if n != 1 {
		t.Errorf("expected 1 pending retry, got %d", n)
	}
}

func TestService_ProcessDueRetries_BadRowDoesNotBlock(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	svc := NewService(store)
	fake := &fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200}}
	svc.SetHTTPClient(fake)
	if _, err := svc.Create(context.Background(), CreateInput{
		URL:     "https://example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	bogusID := uuid.New()
	if err := store.EnqueueRetry(context.Background(), bogusID, time.Now().UTC().Add(-1*time.Second)); err != nil {
		t.Fatalf("EnqueueRetry: %v", err)
	}
	fake.Responses = []fakeResponse{{StatusCode: 503}}
	results, err := svc.Dispatch(context.Background(), EventUserCreated, map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	store.mu.Lock()
	store.pendingRetries[results[0].DeliveryID] = time.Now().UTC().Add(-1 * time.Second)
	store.mu.Unlock()
	fired, err := svc.ProcessDueRetries(context.Background(), time.Now().UTC(), 0)
	if err != nil {
		t.Fatalf("ProcessDueRetries: %v", err)
	}
	if fired != 1 {
		t.Errorf("fired = %d, want 1", fired)
	}
}
