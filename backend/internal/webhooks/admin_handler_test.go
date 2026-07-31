// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// adminRequest is the JSON shape the handler
// reads from the request body for Create / Update.
type adminRequest struct {
	URL     string   `json:"url"`
	Secret  string   `json:"secret"`
	Events  []string `json:"events,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"`
}

// newAdminSvc wires a Service + the in-process
// auth middleware so the handler tests can hit
// the admin router end-to-end. The token is
// signed with the test secret; the auth claims
// carry ScopeWebhooks so the RequireScope gate
// passes.
func newAdminSvc(t *testing.T) (*Service, func(http.Handler) http.Handler) {
	t.Helper()
	signer := auth.NewSigner("test-secret-very-long-and-very-secret-32+")
	store := auth.NewMemoryStore()
	adminID := uuid.New()
	store.WithUser(&auth.User{
		ID:           adminID.String(),
		Username:     "test-admin",
		Email:        "admin@test.local",
		PasswordHash: "x",
		Role:         "super-admin",
		Enabled:      true,
		Scopes:       auth.Scopes{auth.ScopeWebhooks},
		CreatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	authSvc := auth.NewService(signer, store)
	mw := authSvc.Middleware()

	svc := NewService(NewMemoryStore())
	return svc, mw
}

// newAdminRouter wires the full admin router
// for the test. Returns a ready-to-call
// http.Handler and a token to put in the
// Authorization header.
func newAdminRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	svc, mw := newAdminSvc(t)
	signer := auth.NewSigner("test-secret-very-long-and-very-secret-32+")
	tok, err := signer.Issue("test-admin-id", auth.Scopes{auth.ScopeWebhooks}, "test-jti")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return AdminRouter(svc, mw), tok
}

// doRequest is a tiny helper that wraps the
// router + token + method + path + body into
// one call.
func doRequest(t *testing.T, h http.Handler, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// TestAdminRouter_RequiresAuth — every endpoint
// must reject unauthenticated requests with 401.
func TestAdminRouter_RequiresAuth(t *testing.T) {
	t.Parallel()
	h, _ := newAdminRouter(t)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		w := doRequest(t, h, "", method, "/", nil)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s / (no auth): status = %d, want 401", method, w.Code)
		}
	}
}

// TestAdminRouter_CreateAndGet — full create/get
// round-trip via the HTTP surface.
func TestAdminRouter_CreateAndGet(t *testing.T) {
	t.Parallel()
	h, tok := newAdminRouter(t)
	body := adminRequest{
		URL:    "https://example.com/h",
		Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Events: []string{"user.created", "user.deleted"},
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST: status = %d, body = %s", w.Code, w.Body.String())
	}
	// The Create response returns the secret
	// verbatim (the one-time redaction policy).
	var created endpointView
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Secret != body.Secret {
		t.Errorf("Create secret = %q, want %q (verbatim)", created.Secret, body.Secret)
	}
	if created.ID == uuid.Nil {
		t.Errorf("Create id is zero")
	}
	if !created.Enabled {
		t.Errorf("Create enabled = false, want true (default)")
	}
	// GET the same endpoint. The secret must be
	// redacted now.
	w = doRequest(t, h, tok, http.MethodGet, "/"+created.ID.String(), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET: status = %d, body = %s", w.Code, w.Body.String())
	}
	var got endpointView
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if got.Secret != SecretRedacted {
		t.Errorf("GET secret = %q, want %q (redacted)", got.Secret, SecretRedacted)
	}
	if got.URL != body.URL {
		t.Errorf("GET url = %q, want %q", got.URL, body.URL)
	}
}

// TestAdminRouter_RejectsInvalidURL — POST with a
// non-http(s) URL is rejected with 400.
func TestAdminRouter_RejectsInvalidURL(t *testing.T) {
	t.Parallel()
	h, tok := newAdminRouter(t)
	body := adminRequest{
		URL:    "ftp://example.com/h",
		Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST: status = %d, want 400", w.Code)
	}
}

// TestAdminRouter_RejectsShortSecret — POST with a
// short secret is rejected with 400.
func TestAdminRouter_RejectsShortSecret(t *testing.T) {
	t.Parallel()
	h, tok := newAdminRouter(t)
	body := adminRequest{
		URL:    "https://example.com/h",
		Secret: "short",
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST: status = %d, want 400", w.Code)
	}
}

// TestAdminRouter_RejectsUnknownEvent — POST with
// an event outside the closed enum is rejected
// with 400.
func TestAdminRouter_RejectsUnknownEvent(t *testing.T) {
	t.Parallel()
	h, tok := newAdminRouter(t)
	body := adminRequest{
		URL:    "https://example.com/h",
		Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Events: []string{"totally.fake"},
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST: status = %d, want 400", w.Code)
	}
}

// TestAdminRouter_Update — PATCH with a subset of
// fields updates only those fields.
func TestAdminRouter_Update(t *testing.T) {
	t.Parallel()
	h, tok := newAdminRouter(t)
	body := adminRequest{
		URL:    "https://example.com/h",
		Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST: status = %d", w.Code)
	}
	var created endpointView
	_ = json.NewDecoder(w.Body).Decode(&created)
	// PATCH the URL.
	patch := map[string]any{"url": "https://other.example.com/h"}
	w = doRequest(t, h, tok, http.MethodPatch, "/"+created.ID.String(), patch)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH: status = %d, body = %s", w.Code, w.Body.String())
	}
	var updated endpointView
	_ = json.NewDecoder(w.Body).Decode(&updated)
	if updated.URL != "https://other.example.com/h" {
		t.Errorf("Updated url = %q, want %q", updated.URL, "https://other.example.com/h")
	}
	if updated.Secret != SecretRedacted {
		t.Errorf("Updated secret = %q, want %q (redacted)", updated.Secret, SecretRedacted)
	}
}

// TestAdminRouter_DeleteAndGetNotFound — DELETE
// removes the endpoint; subsequent GET returns
// 404.
func TestAdminRouter_DeleteAndGetNotFound(t *testing.T) {
	t.Parallel()
	h, tok := newAdminRouter(t)
	body := adminRequest{
		URL:    "https://example.com/h",
		Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	var created endpointView
	_ = json.NewDecoder(w.Body).Decode(&created)
	// DELETE.
	w = doRequest(t, h, tok, http.MethodDelete, "/"+created.ID.String(), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE: status = %d, want 204", w.Code)
	}
	// GET 404.
	w = doRequest(t, h, tok, http.MethodGet, "/"+created.ID.String(), nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET after delete: status = %d, want 404", w.Code)
	}
}

// TestAdminRouter_List — POST 2 endpoints, GET
// returns both.
func TestAdminRouter_List(t *testing.T) {
	t.Parallel()
	h, tok := newAdminRouter(t)
	for i, url := range []string{"https://a.example.com/h", "https://b.example.com/h"} {
		body := adminRequest{
			URL:    url,
			Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		}
		w := doRequest(t, h, tok, http.MethodPost, "/", body)
		if w.Code != http.StatusCreated {
			t.Fatalf("POST %d: status = %d", i, w.Code)
		}
	}
	w := doRequest(t, h, tok, http.MethodGet, "/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET: status = %d", w.Code)
	}
	var listResp struct {
		Endpoints []endpointView `json:"endpoints"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Endpoints) != 2 {
		t.Errorf("list len = %d, want 2", len(listResp.Endpoints))
	}
	// Every list entry must redact the secret.
	for i, e := range listResp.Endpoints {
		if e.Secret != SecretRedacted {
			t.Errorf("list[%d] secret = %q, want %q (redacted)", i, e.Secret, SecretRedacted)
		}
	}
}

// TestAdminRouter_RejectsDuplicateURL — two POSTs
// with the same URL return 201 / 409.
func TestAdminRouter_RejectsDuplicateURL(t *testing.T) {
	t.Parallel()
	h, tok := newAdminRouter(t)
	body := adminRequest{
		URL:    "https://example.com/h",
		Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("first POST: status = %d", w.Code)
	}
	w = doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusConflict {
		t.Fatalf("second POST: status = %d, want 409", w.Code)
	}
}

// TestAdminRouter_RejectsInvalidUUID — GET /<not-a-uuid>
// returns 400.
func TestAdminRouter_RejectsInvalidUUID(t *testing.T) {
	t.Parallel()
	h, tok := newAdminRouter(t)
	w := doRequest(t, h, tok, http.MethodGet, "/not-a-uuid", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("GET: status = %d, want 400", w.Code)
	}
}

// TestAdminRouter_SendTestEvent — POST /{id}/test
// dispatches a synthetic event to the endpoint
// and returns the dispatch result.
func TestAdminRouter_SendTestEvent(t *testing.T) {
	t.Parallel()
	svc, mw := newAdminSvc(t)
	// Use a fake HTTP client that returns 200.
	svc.SetHTTPClient(&fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200}})
	signer := auth.NewSigner("test-secret-very-long-and-very-secret-32+")
	tok, err := signer.Issue("test-admin-id", auth.Scopes{auth.ScopeWebhooks}, "test-jti")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	h := AdminRouter(svc, mw)
	// Create an endpoint.
	body := adminRequest{
		URL:    "https://example.com/h",
		Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	var created endpointView
	_ = json.NewDecoder(w.Body).Decode(&created)
	// POST /{id}/test.
	w = doRequest(t, h, tok, http.MethodPost, "/"+created.ID.String()+"/test", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("test: status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode test response: %v", err)
	}
	if resp["status"] != string(DeliveryStatusSuccess) {
		t.Errorf("status = %v, want success", resp["status"])
	}
}

// TestAdminRouter_ListDeliveries — POST an
// endpoint, send a test, then GET
// /{id}/deliveries and assert the row is there.
func TestAdminRouter_ListDeliveries(t *testing.T) {
	t.Parallel()
	svc, mw := newAdminSvc(t)
	svc.SetHTTPClient(&fakeHTTPDoer{Default: &fakeResponse{StatusCode: 200}})
	signer := auth.NewSigner("test-secret-very-long-and-very-secret-32+")
	tok, err := signer.Issue("test-admin-id", auth.Scopes{auth.ScopeWebhooks}, "test-jti")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	h := AdminRouter(svc, mw)
	body := adminRequest{
		URL:    "https://example.com/h",
		Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	var created endpointView
	_ = json.NewDecoder(w.Body).Decode(&created)
	// Send a test event to populate the delivery history.
	if _, err := svc.SendTestEvent(context.Background(), created.ID); err != nil {
		t.Fatalf("SendTestEvent: %v", err)
	}
	// GET /{id}/deliveries.
	w = doRequest(t, h, tok, http.MethodGet, "/"+created.ID.String()+"/deliveries", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET deliveries: status = %d", w.Code)
	}
	var resp struct {
		Deliveries []*Delivery `json:"deliveries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Deliveries) != 1 {
		t.Errorf("deliveries len = %d, want 1", len(resp.Deliveries))
	}
}

// TestAdminRouter_DLQFlow — POST an endpoint, send
// a failing test, force a DLQ row, then verify
// GET /dlq and DELETE /dlq/{id} work.
func TestAdminRouter_DLQFlow(t *testing.T) {
	t.Parallel()
	svc, mw := newAdminSvc(t)
	svc.SetHTTPClient(&fakeHTTPDoer{Default: &fakeResponse{StatusCode: 500, Body: "boom"}})
	signer := auth.NewSigner("test-secret-very-long-and-very-secret-32+")
	tok, err := signer.Issue("test-admin-id", auth.Scopes{auth.ScopeWebhooks}, "test-jti")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	h := AdminRouter(svc, mw)
	body := adminRequest{
		URL:    "https://example.com/h",
		Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	var created endpointView
	_ = json.NewDecoder(w.Body).Decode(&created)
	// Pre-seed a DLQ entry directly (the test
	// endpoint doesn't return a final-attempt
	// failure in one shot, so we use the Store
	// to plant the row the way the dispatcher
	// would after MaxAttempts).
	entry := &DLQEntry{
		ID:            uuid.New(),
		EndpointID:    created.ID,
		EndpointURL:   body.URL,
		EventType:     EventUserCreated,
		Payload:       []byte(`{"a":1}`),
		LastError:     "http 500",
		Attempts:      6,
		LastAttemptAt: time.Now().UTC(),
	}
	if err := svc.store.EnqueueDLQ(context.Background(), entry); err != nil {
		t.Fatalf("EnqueueDLQ: %v", err)
	}
	// GET /dlq.
	w = doRequest(t, h, tok, http.MethodGet, "/dlq", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /dlq: status = %d", w.Code)
	}
	var listResp struct {
		DLQ []*DLQEntry `json:"dlq"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listResp.DLQ) != 1 {
		t.Fatalf("dlq len = %d, want 1", len(listResp.DLQ))
	}
	// DELETE /dlq/{id}.
	w = doRequest(t, h, tok, http.MethodDelete, "/dlq/"+entry.ID.String(), nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("DELETE /dlq/{id}: status = %d, want 204", w.Code)
	}
	// GET /dlq/{id} now 404.
	w = doRequest(t, h, tok, http.MethodGet, "/dlq/"+entry.ID.String(), nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /dlq/{id} after delete: status = %d, want 404", w.Code)
	}
}
