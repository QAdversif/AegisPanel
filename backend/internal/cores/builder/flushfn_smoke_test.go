// SPDX-License-Identifier: AGPL-3.0-or-later
//
// FlushFn end-to-end smoke. The unit test in
// builder_test.go exercises BuildCoreConfigForNode
// in isolation; this one runs the whole pipeline
// the panel's main() runs at boot:
//
//   1. Start a fake aegis-agent on httptest.Server.
//   2. Build a real *singbox.Provider, configure
//      it with a NodeResolver that points at the
//      fake agent.
//   3. Seed an inbounds.MemoryStore with one
//      vless-reality inbound.
//   4. Build a cores.BatchedApplier whose FlushFn
//      is builder.NewFlushFn.
//   5. Enqueue a Delta, wait for the window, assert
//      the fake agent received a POST /v1/apply
//      with a JSON body that contains the inbound
//      tag we seeded.
//
// A failure here means the panel cannot push a
// sing-box config to a real node. The test does
// NOT cover the agent-side apply (the agent is
// not in this repo); it only covers the panel's
// side of the wire.

package builder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/agentgrpc"
	"github.com/QAdversif/AegisPanel/internal/cores"
	"github.com/QAdversif/AegisPanel/internal/cores/singbox"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// fakeAgent records every Apply POST the panel
// makes. The handler returns 200 + a stable ack
// body so singbox.Apply's check passes.
type fakeAgent struct {
	mu      sync.Mutex
	applies []appliedPayload
}

type appliedPayload struct {
	Path        string
	AuthHeader  string
	ContentType string
	Body        []byte
}

func (a *fakeAgent) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apply" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		a.mu.Lock()
		a.applies = append(a.applies, appliedPayload{
			Path:        r.URL.Path,
			AuthHeader:  r.Header.Get("Authorization"),
			ContentType: r.Header.Get("Content-Type"),
			Body:        body,
		})
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

func (a *fakeAgent) snapshot() []appliedPayload {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]appliedPayload, len(a.applies))
	copy(out, a.applies)
	return out
}

// stubResolver is the singbox.NodeResolver that
// the smoke wires into Provider.Configure. The
// resolver returns the host:port of the httptest
// server (the singbox.Apply implementation
// prepends "http://" itself, so the resolver
// returns just the host:port).
//
// v0.8.8: also implements the
// `RefreshBearer` half of the
// `singbox.NodeResolver` interface so
// the smoke compiles after the
// 401-auto-refresh work. The
// RefreshBearer method is a no-op (it
// returns the same bearer) because
// the smoke test does not exercise
// the 401-retry path; the dedicated
// auto-refresh tests in
// `singbox/apply_test.go` cover
// that path with a richer fake.
//
// v0.8.29 (PR 3): the resolver shape changed from
// `singbox.NodeResolver` (Resolve + RefreshBearer) to
// `agentgrpc.NodeResolver` (ResolveAddr + GetBearer +
// Refresh). The stub mirrors the new contract; the
// smoke test does not exercise the 401-auto-refresh
// path so `Refresh` is a no-op.
type stubResolver struct {
	hostPort string
	bearer   string
}

func (r *stubResolver) ResolveAddr(_ context.Context, _ uuid.UUID) (string, error) {
	return r.hostPort, nil
}

func (r *stubResolver) GetBearer(_ context.Context, _ uuid.UUID) (string, error) {
	return r.bearer, nil
}

func (r *stubResolver) Refresh(_ context.Context, _ uuid.UUID) (string, error) {
	return r.bearer, nil
}

// LoadMTLS returns ErrMTLSNotConfigured so the
// gRPC transport falls back to plaintext. v0.8.30
// PR 2b: the mTLS path lands in v0.8.30 PR 2c
// (bootstrap cert-push); the smoke test runs
// against the HTTP+bearer transport.
func (r *stubResolver) LoadMTLS(_ context.Context, _ uuid.UUID) ([]byte, []byte, []byte, error) {
	return nil, nil, nil, agentgrpc.ErrMTLSNotConfigured
}

// hostPortFromURL extracts the host:port from a
// httptest.Server URL (which is of the form
// "http://127.0.0.1:57124"). Stripping the
// "http://" prefix is the work singbox.Apply
// would do at runtime; the smoke mirrors the
// production wiring, so the resolver returns
// what the production nodes table carries.
func hostPortFromURL(t *testing.T, u string) string {
	t.Helper()
	const scheme = "http://"
	if len(u) < len(scheme) || u[:len(scheme)] != scheme {
		t.Fatalf("expected %q to start with %q", u, scheme)
	}
	return u[len(scheme):]
}

func TestFlushFn_AppliesConfigToAgent(t *testing.T) {
	// 1. Fake agent.
	agent := &fakeAgent{}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()

	// 2. Real singbox provider, configured with the
	// fake resolver + the agentgrpc test client.
	// The test client wraps the production
	// httpTransport so the smoke covers the same
	// 401 -> BearerRefresher -> one-retry path
	// the BatchedApplier uses in prod.
	provider := singbox.New()
	resolver := &stubResolver{
		hostPort: hostPortFromURL(t, srv.URL),
		bearer:   "test-bearer-fixture-aaaaaaaaaaaaa",
	}
	client, teardown := agentgrpc.NewTestClient(srv, resolver)
	defer teardown()
	provider.Configure(client)

	// 3. Seed inbounds. We need a real
	// *nodes.Service (inbounds.Service.Create
	// validates the inbound's NodeID against
	// nodes) and a real *inbounds.Service
	// against the MemoryStore so the FlushFn
	// sees a non-empty inbounds table at the
	// node.
	nodeStore := nodes.NewMemoryStore()
	nodesSvc := nodes.NewService(nodeStore)
	nodeID := uuid.New()
	if _, err := nodesSvc.Create(context.Background(), nodes.CreateInput{
		ID:      nodeID,
		Name:    "smoke-node",
		Region:  "local",
		State:   nodes.StateOnline,
		Address: "127.0.0.1:22",
	}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	inbStore := inbounds.NewMemoryStore()
	inbSvc := inbounds.NewService(inbStore, nodesSvc)
	const tag = "vless-in"
	enabled := true
	if _, err := inbSvc.Create(context.Background(), inbounds.CreateInput{
		NodeID:     nodeID,
		Name:       tag,
		Protocol:   inbounds.ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
		Enabled:    &enabled,
		Params: map[string]any{
			"port": 443,
			"uuid": "00000000-0000-0000-0000-000000000001",
			"flow": "xtls-rprx-vision",
			"tls":  map[string]any{"server_name": "cdn.example.com"},
		},
	}); err != nil {
		t.Fatalf("seed inbounds: %v", err)
	}

	// 4. Build the BatchedApplier with a 200ms
	// window so the test does not sleep for the
	// default 20s. The FlushFn is the one the
	// real panel will use.
	applier := cores.NewBatchedApplier(
		200*time.Millisecond,
		100,
		NewFlushFn(inbSvc, nil, nil, nil, nil, provider, nodeID, "smoke-node"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = applier.Run(ctx) }()

	// 5. Enqueue a single delta; the window will
	// fire ~200ms later.
	applier.Enqueue(cores.Delta{
		Kind:   cores.DeltaAddUser,
		UserID: uuid.New(),
	})

	// Poll for the agent-side receive. A 5s ceiling
	// is generous on a busy CI runner; on a
	// developer laptop this completes in <300ms.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := agent.snapshot(); len(got) > 0 {
			// Validate the captured payload.
			payload := got[0]
			if payload.Path != "/v1/apply" {
				t.Errorf("Apply path = %q, want /v1/apply", payload.Path)
			}
			if !strings.HasPrefix(payload.AuthHeader, "Bearer ") {
				t.Errorf("Authorization = %q, want Bearer prefix", payload.AuthHeader)
			}
			if !strings.Contains(payload.AuthHeader, "test-bearer") {
				t.Errorf("Authorization = %q, want the fixture bearer", payload.AuthHeader)
			}
			// The body is the agent's
			// `applyEnvelope` (`{"config": ...}`),
			// not the bare sing-box config. The
			// envelope is the wire contract
			// (see singbox.applyEnvelope in
			// apply.go). Spot-check that the
			// inner config contains the inbound
			// tag and is a valid JSON document.
			if !strings.Contains(string(payload.Body), tag) {
				t.Errorf("Apply body does not contain tag %q; body=%s", tag, string(payload.Body))
			}
			if !strings.Contains(string(payload.Body), `"type":"vless"`) {
				t.Errorf("Apply body does not contain vless type; body=%s", string(payload.Body))
			}
			var envelope struct {
				Config struct {
					Inbounds []map[string]any `json:"inbounds"`
				} `json:"config"`
			}
			if err := json.Unmarshal(payload.Body, &envelope); err != nil {
				t.Fatalf("Apply body is not valid JSON: %v\nbody=%s", err, string(payload.Body))
			}
			if len(envelope.Config.Inbounds) != 1 {
				t.Errorf("Apply inbounds len = %d, want 1", len(envelope.Config.Inbounds))
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no /v1/apply POST received within 5s; agent snapshot = %+v", agent.snapshot())
}

// TestFlushFn_EmptyNodeStillApplies pins the
// "no inbounds → still POST" behaviour. An empty
// node must still produce a renderable config
// (the sing-box renderer emits an outbounds-only
// document) and the FlushFn must still POST it.
// The agent-side diff is the one that decides
// "no-op reload" — the panel cannot skip the
// POST without breaking the agent's invariant
// that every render fires an apply.
func TestFlushFn_EmptyNodeStillApplies(t *testing.T) {
	agent := &fakeAgent{}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()

	provider := singbox.New()
	resolver := &stubResolver{
		hostPort: hostPortFromURL(t, srv.URL),
		bearer:   "test-bearer-fixture-bbbbbbbbbbbbb",
	}
	client, teardown := agentgrpc.NewTestClient(srv, resolver)
	defer teardown()
	provider.Configure(client)

	// For the empty-node smoke we still need a
	// non-nil *nodes.Service so inbounds.NewService
	// does not panic. We do not create any nodes
	// because the FlushFn is supposed to render an
	// outbounds-only config and the test's only
	// assertion is "the Apply POST arrived".
	inbSvc := inbounds.NewService(inbounds.NewMemoryStore(), nodes.NewService(nodes.NewMemoryStore()))

	applier := cores.NewBatchedApplier(
		200*time.Millisecond,
		100,
		NewFlushFn(inbSvc, nil, nil, nil, nil, provider, uuid.New(), "empty-node"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = applier.Run(ctx) }()

	applier.Enqueue(cores.Delta{Kind: cores.DeltaAddUser, UserID: uuid.Nil})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := agent.snapshot(); len(got) > 0 {
			// The body is the applyEnvelope
			// `{"config": {...}}`; the inner
			// config should be a valid JSON
			// document (no inbounds, just the
			// default outbounds/route).
			var envelope struct {
				Config map[string]any `json:"config"`
			}
			if err := json.Unmarshal(got[0].Body, &envelope); err != nil {
				t.Fatalf("body is not valid JSON: %v\n%s", err, string(got[0].Body))
			}
			if _, ok := envelope.Config["outbounds"]; !ok {
				t.Errorf("empty-node apply missing outbounds: %s", string(got[0].Body))
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no /v1/apply POST received for empty node within 5s")
}
