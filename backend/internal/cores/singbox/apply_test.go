// SPDX-License-Identifier: AGPL-3.0-or-later

package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeNodeResolver returns a fixed (address, bearer) pair
// for any UUID. Tests use it to drive Apply without
// touching the real nodes package.
type fakeNodeResolver struct {
	addr   string
	bearer string
	// failWith, if non-nil, is returned from Resolve()
	// (overrides the addr/bearer return).
	failWith error
	// calls records the number of Resolve invocations.
	calls atomic.Int32
}

func (f *fakeNodeResolver) Resolve(_ context.Context, _ uuid.UUID) (string, string, error) {
	f.calls.Add(1)
	if f.failWith != nil {
		return "", "", f.failWith
	}
	return f.addr, f.bearer, nil
}

// TestApply_HappyPath verifies the v0.4.0 happy path:
//   - Apply POSTs to the node's address + /v1/apply.
//   - The body is {"config": <cfg>} (the shape the agent
//     expects).
//   - The Authorization: Bearer <bearer> header is set.
//   - A 2xx response yields nil.
func TestApply_HappyPath(t *testing.T) {
	const wantBearer = "test-bearer-do-not-use"
	var (
		gotMethod  string
		gotPath    string
		gotAuth    string
		gotCT      string
		gotBody    []byte
		serverHits atomic.Int32
	)
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverHits.Add(1)
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		// Match the v0.3.0 agent response shape (v0.4.0-b
		// extends with a `verify` field but keeps the
		// existing keys for backward compat).
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted":    true,
			"received_at": "2026-01-01T00:00:00Z",
			"bytes":       len(gotBody),
		})
	}))
	defer agent.Close()

	resolver := &fakeNodeResolver{addr: strings.TrimPrefix(agent.URL, "http://"), bearer: wantBearer}
	p := New()
	p.Configure(resolver, agent.Client())

	nodeID := uuid.New().String()
	cfg := []byte(`{"inbounds":[{"type":"vless","tag":"in-1"}]}`)
	if err := p.Apply(context.Background(), nodeID, cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if serverHits.Load() != 1 {
		t.Fatalf("server hit count = %d, want 1", serverHits.Load())
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/apply" {
		t.Errorf("path = %q, want /v1/apply", gotPath)
	}
	if gotAuth != "Bearer "+wantBearer {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer "+wantBearer)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("Content-Type = %q, want application/json…", gotCT)
	}
	// Body must be the envelope with the config inside.
	var env applyEnvelope
	if err := json.Unmarshal(gotBody, &env); err != nil {
		t.Fatalf("envelope unmarshal: %v (body=%q)", err, gotBody)
	}
	if string(env.Config) != string(cfg) {
		t.Errorf("envelope.Config = %q, want %q", env.Config, cfg)
	}
	if resolver.calls.Load() != 1 {
		t.Errorf("resolver calls = %d, want 1", resolver.calls.Load())
	}
}

// TestApply_AgentError4xx verifies that a 4xx response from
// the agent produces a wrapped error.
func TestApply_AgentError4xx(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"bad config"}`, http.StatusBadRequest)
	}))
	defer agent.Close()
	p := New()
	p.Configure(&fakeNodeResolver{addr: strings.TrimPrefix(agent.URL, "http://"), bearer: "x"}, agent.Client())
	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should fail on 4xx")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention status code, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "bad config") {
		t.Errorf("error should include body, got %q", err.Error())
	}
}

// TestApply_AgentError5xx verifies that a 5xx response
// surfaces as a wrapped error.
func TestApply_AgentError5xx(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `internal failure`, http.StatusInternalServerError)
	}))
	defer agent.Close()
	p := New()
	p.Configure(&fakeNodeResolver{addr: strings.TrimPrefix(agent.URL, "http://"), bearer: "x"}, agent.Client())
	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should fail on 5xx")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention 500, got %q", err.Error())
	}
}

// TestApply_NetworkError verifies that a network-level
// failure (connection refused) is reported.
func TestApply_NetworkError(t *testing.T) {
	p := New()
	// Use an address that will refuse connections. 127.0.0.1:1
	// is reserved for tcpmux and almost never listening.
	p.Configure(&fakeNodeResolver{addr: "127.0.0.1:1", bearer: "x"}, &http.Client{Timeout: 500 * time.Millisecond})
	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should fail on connection refused")
	}
	if !strings.Contains(err.Error(), "singbox apply: POST") {
		t.Errorf("error should be a POST error, got %q", err.Error())
	}
}

// TestApply_InvalidNodeID verifies that a non-UUID nodeID
// is rejected before any HTTP call.
func TestApply_InvalidNodeID(t *testing.T) {
	p := New()
	p.Configure(&fakeNodeResolver{addr: "127.0.0.1:1", bearer: "x"}, &http.Client{Timeout: 100 * time.Millisecond})
	err := p.Apply(context.Background(), "not-a-uuid", []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should reject non-UUID nodeID")
	}
	if !strings.Contains(err.Error(), "not a UUID") {
		t.Errorf("error should mention UUID parse, got %q", err.Error())
	}
}

// TestApply_ResolverError verifies that a resolver error
// is propagated without any HTTP call.
func TestApply_ResolverError(t *testing.T) {
	want := errors.New("node offline")
	p := New()
	p.Configure(&fakeNodeResolver{failWith: want}, &http.Client{Timeout: 100 * time.Millisecond})
	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should propagate resolver error")
	}
	if !errors.Is(err, want) {
		t.Errorf("error should wrap %v, got %v", want, err)
	}
}

// TestApply_NotConfigured verifies the no-Configure path.
func TestApply_NotConfigured(t *testing.T) {
	p := New()
	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if !errors.Is(err, ErrApplyNotConfigured) {
		t.Errorf("Apply without Configure: %v, want ErrApplyNotConfigured", err)
	}
}

// TestApply_EmptyAddress verifies the guard against
// "resolver returned an empty address" (a bug we
// specifically want to catch in tests, not in
// production).
func TestApply_EmptyAddress(t *testing.T) {
	p := New()
	p.Configure(&fakeNodeResolver{addr: "", bearer: "x"}, &http.Client{Timeout: 100 * time.Millisecond})
	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should fail on empty address")
	}
	if !strings.Contains(err.Error(), "empty address") {
		t.Errorf("error should mention empty address, got %q", err.Error())
	}
}

// TestApply_ContextCanceled verifies that a cancelled
// context aborts the request.
func TestApply_ContextCanceled(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Block until the client disconnects.
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer agent.Close()
	p := New()
	p.Configure(&fakeNodeResolver{addr: strings.TrimPrefix(agent.URL, "http://"), bearer: "x"}, &http.Client{Timeout: 5 * time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before Apply
	err := p.Apply(ctx, uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply with cancelled ctx should fail")
	}
}
