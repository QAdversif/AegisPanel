//go:build integration

// SPDX-License-Identifier: AGPL-3.0-or-later
//
// End-to-end integration test for the BatchedApplier
// + FlushFn pipeline against a real Postgres + a
// httptest fake aegis-agent.
//
// # Why this lives behind `//go:build integration`
//
// The smoke test in flushfn_smoke_test.go exercises
// the panel→sing-box pipeline with the MemoryStore
// path; that runs on every `go test ./...` and
// catches the wiring regressions. This test adds
// the real-PgStore half:
//
//   1. testutil.MustNewPool gives us a real
//      *pgxpool.Pool against a real pg (CI's
//      backend job spins one up via the
//      `services: postgres` block in
//      .github/workflows/ci.yml).
//   2. *inbounds.Service + *users.Service run
//      against their PgStore variants.
//   3. The FlushFn reads through that PgStore on
//      every window, so the integration test is
//      the only place a "the panel wrote to pg
//      and the FlushFn picked it up via SELECT"
//      regression surfaces.
//
// The test stays deliberately short: one node,
// one vless-reality inbound, one user (created
// via Service.Create so the post-commit enqueue
// path runs), one BatchedApplier with a 200ms
// window. The Apply POST is captured by a
// httptest.Server and the assertion runs against
// its snapshot — no goroutine polling, no sleep
// loops beyond the natural window.

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

	"github.com/QAdversif/AegisPanel/internal/cores"
	"github.com/QAdversif/AegisPanel/internal/cores/singbox"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
	"github.com/QAdversif/AegisPanel/internal/users"

	"github.com/QAdversif/AegisPanel/testutil"
)

// fakeAgent records every Apply POST the panel
// makes. See flushfn_smoke_test.go for the broader
// rationale; the integration variant is identical
// because the agent wire contract is the only
// thing under test (the singbox.Provider.Apply
// path is the same against a real or fake agent).
type integrationFakeAgent struct {
	mu      sync.Mutex
	applies []integrationAppliedPayload
}

type integrationAppliedPayload struct {
	Path   string
	Body   []byte
	Auth   string
	Method string
}

func (a *integrationFakeAgent) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apply" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		a.mu.Lock()
		a.applies = append(a.applies, integrationAppliedPayload{
			Path:   r.URL.Path,
			Body:   body,
			Auth:   r.Header.Get("Authorization"),
			Method: r.Method,
		})
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

func (a *integrationFakeAgent) snapshot() []integrationAppliedPayload {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]integrationAppliedPayload, len(a.applies))
	copy(out, a.applies)
	return out
}

// hostPortFromURL is shared with the smoke test
// via a tiny re-declaration here to keep the
// integration build free of the smoke test's
// build-less symbols.
func integrationHostPort(t *testing.T, u string) string {
	t.Helper()
	const scheme = "http://"
	if len(u) < len(scheme) || u[:len(scheme)] != scheme {
		t.Fatalf("expected %q to start with %q", u, scheme)
	}
	return u[len(scheme):]
}

// stubResolver is the singbox.NodeResolver the
// integration test wires into Provider.Configure.
// Every node id resolves to the same fake agent;
// the test only ever has one node so the shared
// address is correct.
type integrationStubResolver struct {
	hostPort string
	bearer   string
}

func (r *integrationStubResolver) Resolve(_ context.Context, _ uuid.UUID) (string, string, error) {
	return r.hostPort, r.bearer, nil
}

// RefreshBearer is a no-op for the integration
// test: the fake agent never returns 401, so the
// BatchedApplier never enters the auto-refresh
// retry path. Returning the same bearer keeps
// `singbox.NodeResolver` satisfied without
// making the test async or pulling the SSH
// fixture into the integration scope.
func (r *integrationStubResolver) RefreshBearer(_ context.Context, _ uuid.UUID) (string, error) {
	return r.bearer, nil
}

// TestIntegration_EndToEnd_RealPgCreateUserTriggersApply
// is the headline integration test: the panel
// persists a user via users.Service.Create (real
// pg, real PgStore), the post-commit enqueue
// reaches the per-node BatchedApplier, the
// 200ms window fires, the FlushFn re-renders
// the sing-box config (reading through the
// inbounds PgStore), and the fake agent receives
// a POST /v1/apply with the correct envelope
// shape.
//
// A failure here means the end-to-end
// panel→agent pipeline is broken: a panel that
// ships with this test green but real nodes
// seeing no Apply POSTs would point at the
// agent-side diff (out of scope for this repo).
func TestIntegration_EndToEnd_RealPgCreateUserTriggersApply(t *testing.T) {
	pool := testutil.MustNewPool(t)
	defer pool.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Real services against the live pg.
	usersStore := users.NewPgStore(pool)
	inboundsStore := inbounds.NewPgStore(pool)
	nodesStore := nodes.NewPgStore(pool)
	nodesSvc := nodes.NewService(nodesStore)
	inbSvc := inbounds.NewService(inboundsStore, nodesSvc)
	usersSvc := users.NewService(usersStore)
	usersSvc.SetClock(func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) })

	// 2. Fake aegis-agent + singbox.Provider.
	agent := &integrationFakeAgent{}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()
	provider := singbox.New()
	provider.Configure(&integrationStubResolver{
		hostPort: integrationHostPort(t, srv.URL),
		bearer:   "integration-bearer-fixture-aaaaaaaaa",
	}, singbox.NewHTTPClient())

	// 3. Seed node + inbound.
	nodeID := uuid.New()
	if _, err := nodesSvc.Create(ctx, nodes.CreateInput{
		ID:      nodeID,
		Name:    "integration-node",
		Region:  "test",
		State:   nodes.StateOnline,
		Address: "127.0.0.1:22",
	}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	const (
		inboundTag  = "vless-integration"
		inboundUUID = "11111111-2222-3333-4444-555555555555"
	)
	enabled := true
	created, err := inbSvc.Create(ctx, inbounds.CreateInput{
		NodeID:     nodeID,
		Name:       inboundTag,
		Protocol:   inbounds.ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
		Enabled:    &enabled,
		Params: map[string]any{
			"port": 443,
			"uuid": inboundUUID,
			"flow": "xtls-rprx-vision",
			"tls":  map[string]any{"server_name": "cdn.example.com"},
		},
	})
	if err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("seed inbound returned zero id (PgStore likely failed to assign one)")
	}

	// 4. Per-node BatchedApplier + Wire WithBatchApplier.
	appliers := map[uuid.UUID]*cores.BatchedApplier{}
	applier := cores.NewBatchedApplier(
		200*time.Millisecond,
		100,
		NewFlushFn(inbSvc, nil, nil, nil, nil, provider, nodeID, "integration-node"),
	)
	appliers[nodeID] = applier
	usersSvc.WithBatchApplier(appliers)
	inbSvc.WithBatchApplier(appliers)

	applierCtx, applierCancel := context.WithCancel(ctx)
	defer applierCancel()
	go func() { _ = applier.Run(applierCtx) }()

	// 5. Drive a real Create through the panel-side
	// service. This is the entry point a real
	// HTTP handler would call: it persists the
	// user (real pg INSERT) and Enqueues a
	// DeltaAddUser via the post-commit hook.
	const userName = "alice-integration"
	created2, err := usersSvc.Create(ctx, users.CreateInput{
		Username: userName,
		Email:    "alice@example.com",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if created2.Username != userName {
		t.Fatalf("created user username = %q, want %q", created2.Username, userName)
	}

	// 6. Wait for the BatchedApplier window to
	// fire and the FlushFn to land on the agent.
	// 5s is generous on a busy CI runner; on a
	// dev laptop this completes in <300ms.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := agent.snapshot(); len(got) > 0 {
			// The wire contract: POST /v1/apply,
			// Bearer auth, JSON envelope
			// {"config": {<sing-box config>}}.
			payload := got[0]
			if payload.Method != http.MethodPost {
				t.Errorf("Apply method = %q, want POST", payload.Method)
			}
			if !strings.HasPrefix(payload.Auth, "Bearer ") {
				t.Errorf("Authorization = %q, want Bearer prefix", payload.Auth)
			}
			var envelope struct {
				Config struct {
					Inbounds []struct {
						Tag   string           `json:"tag"`
						Type  string           `json:"type"`
						Users []map[string]any `json:"users"`
					} `json:"inbounds"`
				} `json:"config"`
			}
			if err := json.Unmarshal(payload.Body, &envelope); err != nil {
				t.Fatalf("Apply body is not valid JSON: %v\nbody=%s", err, string(payload.Body))
			}
			if len(envelope.Config.Inbounds) != 1 {
				t.Fatalf("Apply inbounds len = %d, want 1 (the vless-reality inbound we seeded); body=%s", len(envelope.Config.Inbounds), string(payload.Body))
			}
			inb := envelope.Config.Inbounds[0]
			if inb.Tag != inboundTag {
				t.Errorf("Apply inbound tag = %q, want %q", inb.Tag, inboundTag)
			}
			if inb.Type != "vless" {
				t.Errorf("Apply inbound type = %q, want vless", inb.Type)
			}
			if len(inb.Users) != 1 {
				t.Fatalf("Apply inbound users len = %d, want 1 (Phase 1 single-user); body=%s", len(inb.Users), string(payload.Body))
			}
			if inb.Users[0]["uuid"] != inboundUUID {
				t.Errorf("Apply inbound user uuid = %v, want %q (from inb.Params)", inb.Users[0]["uuid"], inboundUUID)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no /v1/apply POST received within 5s; agent snapshot = %+v", agent.snapshot())
}
