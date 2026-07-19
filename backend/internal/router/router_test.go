// SPDX-License-Identifier: AGPL-3.0-or-later

package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
	"github.com/QAdversif/AegisPanel/internal/panelcfg"
	"github.com/QAdversif/AegisPanel/internal/subscription"
)

// knownToken is the sub_token of the test user seeded
// by buildRouterForTest. Tests that want a "the
// handler ran" assertion use this token; tests that
// want a "the router 404s" assertion use a different
// (unknown) token, and the routed-vs-default
// distinction is made on the response body.
const knownToken = "tok-known"

// TestRouter_DefaultPathAlwaysServed — the
// documented `/api/v1/sub/<token>` path is the
// canonical mount and is always live. We seed a
// known user; a request to the default path with
// that user's token returns 200 (the handler ran
// and produced a subscription body).
func TestRouter_DefaultPathAlwaysServed(t *testing.T) {
	r := buildRouterForTest(t, "")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sub/"+knownToken, nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("default /api/v1/sub/ returned %d, want 200 (handler did not run)", w.Code)
	}
}

// TestRouter_RotatedPathServed — when the panel's
// active sub_path is set, the router also mounts the
// subscription handler at `/<sub_path>/sub/<token>`.
// A request to that path with the known token returns
// 200.
func TestRouter_RotatedPathServed(t *testing.T) {
	r := buildRouterForTest(t, "path-aabbccdd1234")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/path-aabbccdd1234/sub/"+knownToken, nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("rotated /<sub_path>/sub/ returned %d, want 200 (handler did not run); body=%q", w.Code, w.Body.String())
	}
}

// TestRouter_RotatedPath_DoesNotShadowDefault — the
// two mounts are independent. A request to the
// default path with the known token still returns
// 200 after a rotation (the two mounts coexist).
func TestRouter_RotatedPath_DoesNotShadowDefault(t *testing.T) {
	r := buildRouterForTest(t, "path-eeff0011")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sub/"+knownToken, nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("default mount shadowed by rotated mount; got %d want 200", w.Code)
	}
}

// TestRouter_NoSubPathMeansNoSecondMount — the empty
// sub_path is the default "no rotation" signal. The
// router must NOT mount at `/<empty>/sub/<token>`
// (which would be wrong). A request to `/sub/sub/
// <token>` returns the default 404 — the chi
// router's no-match handler, not the subscription
// handler's NotFound.
func TestRouter_NoSubPathMeansNoSecondMount(t *testing.T) {
	r := buildRouterForTest(t, "")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sub/sub/"+knownToken, nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("/sub/sub/ returned %d, want 404 (no second mount when sub_path is empty)", w.Code)
	}
	// The chi default 404 page is plain text "404
	// page not found"; the subscription handler
	// (when routed) writes a different body. Use
	// the body as a sanity check that we did NOT
	// hit the subscription handler's NotFound
	// branch.
	if got := w.Body.String(); !contains(got, "page not found") && !contains(got, "404") {
		t.Errorf("/sub/sub/ body = %q, want default 404 page (not the subscription handler)", got)
	}
}

// buildRouterForTest wires a minimal router with
// the panelcfg service pre-set to the given
// sub_path. The other services (auth / nodes /
// hosts / inbounds / subscription) are wired with
// the same MemoryStore defaults. The subscription
// store is seeded with a single live user whose
// sub_token is `knownToken`; the rotation
// tests can use this user without further
// setup.
func buildRouterForTest(t *testing.T, subPath string) http.Handler {
	t.Helper()
	authSigner := auth.NewSigner("test-secret")
	authSvc := auth.NewService(authSigner, auth.NewMemoryStore())
	authSvc.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	hostsStore := hosts.NewMemoryStore()
	hostsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	nodesStore := nodes.NewMemoryStore()
	nodesStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	inboundsStore := inbounds.NewMemoryStore()
	inboundsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	subscriptionStore := subscription.NewMemoryStore()
	subscriptionStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	// Seed a live user. The plan / pool / host
	// graph is the empty default — the renderer
	// emits an empty body, but the handler still
	// returns 200.
	subscriptionStore.WithUser(&subscription.User{
		ID:       uuid.New(),
		Username: "alice",
		Status:   subscription.UserStatusActive,
		SubToken: knownToken,
	})

	panelCfgStore := panelcfg.NewMemoryStore()
	panelCfgStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	// If the test wants a non-default sub_path, set
	// it. The empty default is the "no rotation"
	// signal; SetActive with the test path activates
	// the second mount.
	if subPath != "" {
		if _, err := panelCfgStore.SetActive(context.Background(), subPath, 0); err != nil {
			t.Fatalf("panelcfg.SetActive: %v", err)
		}
	}
	panelCfgSvc := panelcfg.NewService(panelCfgStore)

	hostsSvc := hosts.NewService(hostsStore, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))
	nodesSvc := nodes.NewService(nodesStore)
	inboundsSvc := inbounds.NewService(inboundsStore, nodesSvc)
	subscriptionSvc := subscription.NewService(subscriptionStore, hostsSvc, nodesSvc, inboundsSvc)

	return Build(nil, authSvc, nodesSvc, hostsSvc, inboundsSvc, subscriptionSvc, panelCfgSvc, nil)
}

// contains is a small strings.Contains alias to keep
// the test assertions terse. We don't import
// "strings" directly because the only call site is
// here.
func contains(s, substr string) bool {
	if substr == "" {
		return true
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
