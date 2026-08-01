// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Spy is a v0.7.x test helper that wires an
// in-memory webhooks.Service with a no-op
// HTTP dialer. Cross-package tests (plans,
// users, etc.) construct a Spy, subscribe an
// endpoint to the event type they care about,
// run the mutating operation, and assert on
// the recorded Delivery row.
//
// The pattern is intentionally minimal — a real
// httptest.Server would be more thorough but
// the wire format is already exercised by the
// v0.7.0 webhooks-package tests (the
// fakeHTTPDoer). Here we just want to prove
// the dispatch CALL was made with the right
// event type.

package webhooks

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// Spy is the cross-package test helper.
type Spy struct {
	store *MemoryStore
	svc   *Service

	mu2    sync.Mutex
	calls  []spyCall
	bodies [][]byte
}

// spyCall is one observed dispatch.
type spyCall struct {
	EventType EventType
	Endpoint  uuid.UUID
}

// NewSpy builds a Spy backed by an in-memory
// Store. The Service is wired with a dialer
// that records every dispatch the Service
// makes. The dialer does NOT actually post —
// the Service writes the Delivery row before
// the HTTP exchange, so the Spy asserts on
// the store state.
func NewSpy() *Spy {
	store := NewMemoryStore()
	svc := NewService(store)
	svc.SetHTTPClient(spyDialer{spy: nil}) // wired below
	spy := &Spy{store: store, svc: svc}
	svc.SetHTTPClient(spyDialer{spy: spy})
	return spy
}

// Svc returns the *Service the test can pass
// to the package under test via WithWebhooks.
func (s *Spy) Svc() *Service { return s.svc }

// Subscribe creates a wildcard endpoint that
// receives every event of the given type.
// Returns the endpoint ID so the test can
// scope the AssertDeliveredFor assertion.
func (s *Spy) Subscribe(t *testing.T, evt EventType) uuid.UUID {
	t.Helper()
	ep := &Endpoint{
		ID:      uuid.New(),
		URL:     "https://spy-" + string(evt) + ".example.com/h",
		Secret:  "spy-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", // #nosec G101 -- low-entropy test fixture
		Events:  []EventType{evt},
		Enabled: true,
	}
	if err := s.store.CreateEndpoint(context.Background(), ep); err != nil {
		t.Fatalf("spy: CreateEndpoint: %v", err)
	}
	return ep.ID
}

// Calls returns the list of dispatches the Spy
// observed, in dispatch order. The slice is a
// copy; mutating it is safe.
func (s *Spy) Calls() []spyCall {
	s.mu2.Lock()
	defer s.mu2.Unlock()
	out := make([]spyCall, len(s.calls))
	copy(out, s.calls)
	return out
}

// AssertDeliveredFor asserts the Spy observed
// at least one dispatch of `evt` to the given
// endpoint id.
func (s *Spy) AssertDeliveredFor(t *testing.T, endpointID uuid.UUID, evt EventType) {
	t.Helper()
	deliveries, err := s.store.ListDeliveriesByEndpoint(context.Background(), endpointID, 0)
	if err != nil {
		t.Fatalf("spy: ListDeliveriesByEndpoint: %v", err)
	}
	if len(deliveries) == 0 {
		t.Fatalf("spy: no deliveries for endpoint %s, expected at least one for %s", endpointID, evt)
	}
	for _, d := range deliveries {
		if d.EventType == evt {
			return
		}
	}
	gotTypes := make([]EventType, 0, len(deliveries))
	for _, d := range deliveries {
		gotTypes = append(gotTypes, d.EventType)
	}
	t.Errorf("spy: no delivery for event %s on endpoint %s (got %d deliveries: %v)", evt, endpointID, len(deliveries), gotTypes)
}

// AssertNoDelivery asserts the Spy observed
// NO dispatch of `evt` to the given endpoint.
func (s *Spy) AssertNoDelivery(t *testing.T, endpointID uuid.UUID, evt EventType) {
	t.Helper()
	deliveries, err := s.store.ListDeliveriesByEndpoint(context.Background(), endpointID, 0)
	if err != nil {
		t.Fatalf("spy: ListDeliveriesByEndpoint: %v", err)
	}
	for _, d := range deliveries {
		if d.EventType == evt {
			t.Errorf("spy: unexpected delivery for event %s on endpoint %s", evt, endpointID)
		}
	}
}

// spyDialer is the dialer the Spy uses. It
// records the dispatch and returns a 200 OK
// without ever opening a network connection.
// The Service still records a Delivery row
// before calling Do, so the Spy can assert on
// the store state.
type spyDialer struct {
	spy *Spy
}

func (d spyDialer) Do(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	if d.spy != nil {
		d.spy.mu2.Lock()
		d.spy.calls = append(d.spy.calls, spyCall{
			EventType: EventType(req.Header.Get("X-Aegis-Event")),
		})
		d.spy.bodies = append(d.spy.bodies, body)
		d.spy.mu2.Unlock()
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Header:     http.Header{},
	}, nil
}
