// SPDX-License-Identifier: AGPL-3.0-or-later

package panelcfg

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// newTestRouter wires the panelcfg HTTP surface
// against a fresh MemoryStore, with the auth
// middleware pre-seeded with an admin claims. The
// returned handler is the panelcfg subrouter; the
// test sends requests directly to it.
func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	svc := NewService(NewMemoryStore())
	// Pin a deterministic clock so the active row's
	// CreatedAt is stable across runs.
	svc.SetClock(func() time.Time { return time.Unix(1700000000, 0).UTC() })

	// The auth middleware in tests: bypass the JWT
	// check by attaching admin claims directly.
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{
				Scopes: auth.Scopes{auth.ScopeAdmin, auth.ScopeRead, auth.ScopeWrite},
			}
			ctx := auth.WithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	return Router(svc, mw)
}

func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func TestHandler_GetDefault(t *testing.T) {
	r := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := decodeBody(t, w.Result())
	if got, _ := body["sub_path"].(string); got != "" {
		t.Errorf("sub_path = %q, want empty (default)", got)
	}
	if active, _ := body["is_active"].(bool); !active {
		t.Errorf("is_active = %v, want true", active)
	}
}

func TestHandler_RotateToExplicit(t *testing.T) {
	r := newTestRouter(t)

	body, _ := json.Marshal(rotateToRequest{SubPath: "aegis-prod-2026"})
	req := httptest.NewRequest(http.MethodPost, "/rotate-to", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	got := decodeBody(t, w.Result())
	if sp, _ := got["sub_path"].(string); sp != "aegis-prod-2026" {
		t.Errorf("sub_path = %q, want aegis-prod-2026", sp)
	}
}

func TestHandler_RotateToRejectsInvalidPath(t *testing.T) {
	r := newTestRouter(t)

	// Empty path: 400.
	{
		body, _ := json.Marshal(rotateToRequest{SubPath: ""})
		req := httptest.NewRequest(http.MethodPost, "/rotate-to", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("empty path: status = %d, want 400", w.Code)
		}
	}
	// Too short: 400.
	{
		body, _ := json.Marshal(rotateToRequest{SubPath: "ab"})
		req := httptest.NewRequest(http.MethodPost, "/rotate-to", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("too short: status = %d, want 400", w.Code)
		}
	}
	// Uppercase: 400 (charset is [a-z0-9-]).
	{
		body, _ := json.Marshal(rotateToRequest{SubPath: "Aegis-Prod"})
		req := httptest.NewRequest(http.MethodPost, "/rotate-to", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("uppercase: status = %d, want 400", w.Code)
		}
	}
	// Slash: 400 (single URL segment).
	{
		body, _ := json.Marshal(rotateToRequest{SubPath: "foo/bar"})
		req := httptest.NewRequest(http.MethodPost, "/rotate-to", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("slash: status = %d, want 400", w.Code)
		}
	}
}

func TestHandler_RotateRandom(t *testing.T) {
	r := newTestRouter(t)

	// Pre-set the active path to a known value so the
	// random rotation must produce something different.
	{
		body, _ := json.Marshal(rotateToRequest{SubPath: "before-rotate"})
		req := httptest.NewRequest(http.MethodPost, "/rotate-to", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("pre-set: status = %d, want 200", w.Code)
		}
	}

	// Now POST /rotate with no body.
	req := httptest.NewRequest(http.MethodPost, "/rotate", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	got := decodeBody(t, w.Result())
	sp, _ := got["sub_path"].(string)
	if sp == "" {
		t.Errorf("sub_path = empty, want 16-char random")
	}
	if sp == "before-rotate" {
		t.Errorf("sub_path = %q, want something other than the pre-set value", sp)
	}
	if len(sp) != 16 {
		t.Errorf("sub_path = %q (len %d), want 16 hex chars", sp, len(sp))
	}
}

func TestHandler_Reset(t *testing.T) {
	r := newTestRouter(t)

	// Rotate first.
	{
		body, _ := json.Marshal(rotateToRequest{SubPath: "aegis-prod-2026"})
		req := httptest.NewRequest(http.MethodPost, "/rotate-to", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("rotate: status = %d, want 200", w.Code)
		}
	}

	// Now reset.
	req := httptest.NewRequest(http.MethodPost, "/reset", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	got := decodeBody(t, w.Result())
	if sp, _ := got["sub_path"].(string); sp != "" {
		t.Errorf("sub_path after reset = %q, want empty (default)", sp)
	}
}

func TestHandler_RejectsNonAdmin(t *testing.T) {
	// Wire a router where the middleware attaches
	// read+write scopes but no admin. The RequireScope
	// middleware should reject every endpoint with 403.
	svc := NewService(NewMemoryStore())
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{
				Scopes: auth.Scopes{auth.ScopeRead, auth.ScopeWrite},
			}
			ctx := auth.WithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	r := Router(svc, mw)

	for _, path := range []string{"/", "/rotate", "/reset"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403 (no admin scope)", path, w.Code)
		}
	}
}
