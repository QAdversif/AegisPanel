// SPDX-License-Identifier: AGPL-3.0-or-later
//
// HTTP handler tests for the v0.8.31 deprecation
// warning header on `GET /api/v1/nodes`. The header
// fires when at least one node is still on
// `agent_transport=http`; once the operator rotates
// every node to `grpc`, the header disappears.
//
// # Why a dedicated test file
//
// The deprecation warning has four assertions
// (Deprecation, X-Aegis-Deprecation-Notice,
// X-Aegis-Deprecation-Sunset, "no headers when
// all-grpc"). The four cases share the
// `handleList` + httptest harness and the
// MemoryStore + create-helper, so keeping them
// in one file matches the v0.8.7
// `handler_refresh_bearer_test.go` pattern
// (one feature, one file).
//
// The tests call the handler directly (not
// through a chi router) because the
// deprecation-warning surface has no
// auth-scope / route-conditional complexity —
// the chi wrapper would only add noise.

package nodes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleList_DeprecationHeader_AnyHTTPNodes(t *testing.T) {
	// The canonical "operator has not started
	// the migration" state: every node is on
	// http. The header MUST fire; the body
	// MUST still include the per-node
	// `agent_transport: "http"`.
	svc, _ := newSvc(t)
	ctx := context.Background()
	// Three nodes, all on http (the
	// Service.Create default).
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if _, err := svc.Create(ctx, CreateInput{
			Name:    name,
			Region:  "eu",
			Address: "10.0.0.1:22",
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	rr := httptest.NewRecorder()
	svc.handleList()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Deprecation"); got != "true" {
		t.Fatalf("Deprecation = %q, want %q (RFC 8594)", got, "true")
	}
	notice := rr.Header().Get("X-Aegis-Deprecation-Notice")
	if notice == "" {
		t.Fatal("X-Aegis-Deprecation-Notice must be set when any node is on http")
	}
	for _, want := range []string{
		"agent_transport=http",
		"deprecated",
		"rotate-transport",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("X-Aegis-Deprecation-Notice = %q, expected to contain %q", notice, want)
		}
	}
	if got := rr.Header().Get("X-Aegis-Deprecation-Sunset"); got != "v0.8.32" {
		t.Errorf("X-Aegis-Deprecation-Sunset = %q, want %q", got, "v0.8.32")
	}
	// Body must still include the per-node
	// agent_transport so the operator's
	// existing tooling does not need to learn
	// the header.
	if !strings.Contains(rr.Body.String(), `"agent_transport":"http"`) {
		t.Errorf("body should still include per-node agent_transport; got %s", rr.Body.String())
	}
}

func TestHandleList_DeprecationHeader_PartialHTTPNodes(t *testing.T) {
	// The realistic "operator is mid-migration"
	// state: some nodes are on grpc, some
	// still on http. The header fires because
	// ANY node is on http; the operator sees
	// the migration is not done.
	svc, _ := newSvc(t)
	ctx := context.Background()
	a, _ := svc.Create(ctx, CreateInput{Name: "alpha", Region: "eu", Address: "10.0.0.1:22"})
	b, _ := svc.Create(ctx, CreateInput{Name: "bravo", Region: "eu", Address: "10.0.0.2:22"})
	if _, err := svc.RotateTransport(ctx, a.ID, AgentTransportGRPC); err != nil {
		t.Fatalf("rotate alpha: %v", err)
	}
	_ = b // bravo stays on http

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	rr := httptest.NewRecorder()
	svc.handleList()(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Deprecation"); got != "true" {
		t.Errorf("Deprecation = %q, want %q (partial-http must fire)", got, "true")
	}
}

func TestHandleList_DeprecationHeader_AllGRPC(t *testing.T) {
	// The "migration complete" state: every
	// node is on grpc. The header MUST NOT
	// fire; the operator's daily check
	// returns a clean 200 with no deprecation
	// signal.
	svc, _ := newSvc(t)
	ctx := context.Background()
	for _, name := range []string{"alpha", "bravo"} {
		n, err := svc.Create(ctx, CreateInput{
			Name:    name,
			Region:  "eu",
			Address: "10.0.0.1:22",
		})
		if err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		if _, err := svc.RotateTransport(ctx, n.ID, AgentTransportGRPC); err != nil {
			t.Fatalf("rotate %s: %v", name, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	rr := httptest.NewRecorder()
	svc.handleList()(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	for _, h := range []string{"Deprecation", "X-Aegis-Deprecation-Notice", "X-Aegis-Deprecation-Sunset"} {
		if got := rr.Header().Get(h); got != "" {
			t.Errorf("%s = %q, want empty (all-grpc must clear deprecation)", h, got)
		}
	}
}

func TestHandleList_DeprecationHeader_EmptyList(t *testing.T) {
	// Edge case: zero nodes. The migration
	// status is "n/a" — there is no node to
	// rotate, so the deprecation warning is
	// not meaningful. The header MUST NOT
	// fire (the loop in handleList has zero
	// iterations; the Deprecation header is
	// never set).
	svc, _ := newSvc(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	rr := httptest.NewRecorder()
	svc.handleList()(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Deprecation"); got != "" {
		t.Errorf("Deprecation = %q, want empty (no nodes, no deprecation)", got)
	}
}
