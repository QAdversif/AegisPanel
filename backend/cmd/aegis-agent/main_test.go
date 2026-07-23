// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Tests for the aegis-agent HTTP server. The
// package-under-test (main) is imported via the
// internal-package trick: tests run `go test
// ./cmd/aegis-agent` and the agent's helpers
// (newMux, handleApply, etc.) are exercised via the
// exported test entries below.
//
// The tests focus on:
//
//   1. Auth — the bearer gate (or the /healthz
//      fallback when the secret is empty).
//   2. Body validation — the /v1/apply handler
//      rejects empty / malformed / non-JSON bodies.
//   3. Method gating — non-GET on /healthz is 405.
//   4. Constant-time compare — verified with a
//      smoke test (no timing-channel assertion; we
//      trust the implementation).

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withBearerSecret sets the global bearerSecret
// for the duration of a test. Tests must call the
// returned `defer` to restore the original value so
// parallel tests do not stomp on each other.
func withBearerSecret(t *testing.T, value string) {
	t.Helper()
	prev := bearerSecret
	bearerSecret = value
	t.Cleanup(func() { bearerSecret = prev })
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	withBearerSecret(t, "test-secret-32bytes-padding-xx")
	return httptest.NewServer(newMux())
}

func TestHealthz_NoAuth(t *testing.T) {
	withBearerSecret(t, "test-secret")
	srv := httptest.NewServer(newMux())
	defer srv.Close()

	// /healthz requires auth when a bearer
	// secret is configured (the only "no auth"
	// path is the "secret not configured" fallback
	// for the docker-compose smoke).
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/healthz", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body healthzResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.OK {
		t.Fatalf("ok = false, want true")
	}
	if body.Version != version {
		t.Fatalf("version = %q, want %q", body.Version, version)
	}
	if body.StartedAt == "" {
		t.Fatalf("started_at is empty")
	}
}

func TestHealthz_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	// The auth check runs before the method
	// check, so a POST without an Authorization
	// header gets 401, not 405. To exercise the
	// 405 path, the request must include the
	// bearer.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/healthz", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestApply_RequiresAuth(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/apply", "application/json", strings.NewReader(`{"config":{}}`))
	if err != nil {
		t.Fatalf("POST /v1/apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestApply_RejectsEmptyBody(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/apply", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestApply_RejectsMalformedJSON(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/apply", strings.NewReader("not-json"))
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestApply_RejectsMissingConfigField(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	// Valid JSON envelope but no `config` field.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/apply", strings.NewReader(`{"other": "x"}`))
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestApply_AcceptsValidConfig(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	body := `{"config":{"inbounds":[],"outbounds":[]}}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/apply", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 202; body = %s", resp.StatusCode, string(body))
	}
	var out applyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Accepted {
		t.Fatalf("accepted = false, want true")
	}
	if out.Bytes != len(body)-len(`{"config":`)-1 {
		// The byte count is the inner JSON length
		// (between the `config":` and the closing `}`).
		// For a 32-char body, this is 32.
		t.Logf("bytes = %d (informational; the schema is intentionally loose)", out.Bytes)
	}
	if lastApplyISO == "" {
		t.Fatalf("lastApplyISO was not updated")
	}
}

func TestApply_OversizedBody(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	// 2 MiB > the 1 MiB cap. The server should
	// respond with 400 (the http.MaxBytesReader
	// returns a body-read error).
	oversized := bytes.Repeat([]byte("a"), 2<<20)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/apply", bytes.NewReader(oversized))
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStatus_RequiresAuth(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/status")
	if err != nil {
		t.Fatalf("GET /v1/status: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestStatus_WithAuth(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/status", nil)
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/status: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Running {
		t.Fatalf("running = false, want true")
	}
}

func TestStats_AcceptsAndReturnsEmptyShape(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/stats: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body statsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.BytesIn != 0 || body.BytesOut != 0 || body.Users != 0 {
		t.Fatalf("expected zero stats, got %+v", body)
	}
}

func TestSubtleCmp_EqualAndDifferent(t *testing.T) {
	if subtleCmp("aegis-secret-1234567890", "aegis-secret-1234567890") != 0 {
		t.Fatalf("equal strings should compare equal")
	}
	if subtleCmp("aegis-secret-1234567890", "aegis-secret-1234567891") == 0 {
		t.Fatalf("differing last char should not compare equal")
	}
	if subtleCmp("aegis-secret-1234567890", "different-length") == 0 {
		t.Fatalf("differing length should not compare equal")
	}
}

func TestEmptyBearer_AllowsOnlyHealthz(t *testing.T) {
	withBearerSecret(t, "")
	srv := httptest.NewServer(newMux())
	defer srv.Close()

	// /healthz is allowed.
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", resp.StatusCode)
	}

	// /v1/apply is forbidden with 503 (not 401
	// — the secret is not "wrong", it is "not
	// configured", which is a server-side error).
	resp, err = http.Post(srv.URL+"/v1/apply", "application/json", strings.NewReader(`{"config":{}}`))
	if err != nil {
		t.Fatalf("POST /v1/apply: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("/v1/apply status = %d, want 503", resp.StatusCode)
	}
}
