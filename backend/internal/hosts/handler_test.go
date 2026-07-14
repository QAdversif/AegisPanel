// SPDX-License-Identifier: AGPL-3.0-or-later

package hosts

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// --- helpers ------------------------------------------------------------

// newTestServer wires a Router around the testEnv's
// service. Auth middleware injects a claims set with
// the hosts scope so the RequireScope guard does not
// 403 every test; the one test that exercises the guard
// swaps in its own auth middleware.
func newTestServer(t *testing.T, env *testEnv) (*Service, http.Handler) {
	t.Helper()
	withScope := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ctx = auth.WithClaims(ctx, &auth.Claims{
				Scopes: auth.Scopes{auth.ScopeHosts},
			})
			h.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	r := Router(env.svc, withScope)
	return env.svc, r
}

// seedNodeAndInbound was a stub helper retained from an
// earlier draft; the handler tests now use makeSvc
// (defined in service_test.go) for the common case
// and do not need it. Kept the function name as a
// comment so the next reader who adds a test that
// needs a different seed shape knows where to start.

func do(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, w.Body.String())
	}
}

// --- list ---------------------------------------------------------------

func TestHandler_List_EmptyReturnsArrayNotNull(t *testing.T) {
	env := makeSvc(t) // no nodes seeded
	_, h := newTestServer(t, env)
	w := do(t, h, "GET", "/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	var resp struct {
		Hosts []*Host `json:"hosts"`
	}
	decode(t, w, &resp)
	if resp.Hosts == nil {
		t.Fatal("hosts should be [] not null")
	}
	if len(resp.Hosts) != 0 {
		t.Errorf("expected empty list, got %d", len(resp.Hosts))
	}
}

// --- create -------------------------------------------------------------

func TestHandler_Create_Success(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	_, h := newTestServer(t, env)
	body := createRequest{
		Remark: "Latvia",
		Type:   HostTypeDirect,
		Endpoints: []createEndpoint{
			{NodeID: nodeID, InboundID: env.inboundFor(nodeID), Weight: 1},
		},
	}
	w := do(t, h, "POST", "/", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var got Host
	decode(t, w, &got)
	if got.ID == uuid.Nil {
		t.Error("ID should be assigned")
	}
	if got.Remark != "Latvia" {
		t.Errorf("remark = %q", got.Remark)
	}
}

func TestHandler_Create_ValidationErrorReturns400(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	_, h := newTestServer(t, env)
	// type=chain is not allowed in Phase 1.
	body := createRequest{
		Remark: "x",
		Type:   HostType("chain"),
		Endpoints: []createEndpoint{
			{NodeID: nodeID, InboundID: env.inboundFor(nodeID), Weight: 1},
		},
	}
	w := do(t, h, "POST", "/", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "chain") {
		t.Errorf("error should mention offending value chain, got: %s", w.Body.String())
	}
}

func TestHandler_Create_MalformedBodyReturns400(t *testing.T) {
	env := makeSvc(t, uuid.New())
	_, h := newTestServer(t, env)
	req := httptest.NewRequest("POST", "/", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
}

func TestHandler_Create_UnknownInboundReturns400(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	_, h := newTestServer(t, env)
	body := createRequest{
		Remark: "x",
		Type:   HostTypeDirect,
		Endpoints: []createEndpoint{
			{NodeID: nodeID, InboundID: uuid.New(), Weight: 1},
		},
	}
	w := do(t, h, "POST", "/", body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
}

// --- get ----------------------------------------------------------------

func TestHandler_Get_NotFound(t *testing.T) {
	env := makeSvc(t)
	_, h := newTestServer(t, env)
	w := do(t, h, "GET", "/"+uuid.NewString(), nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}

func TestHandler_Get_BadIDReturns400(t *testing.T) {
	env := makeSvc(t)
	_, h := newTestServer(t, env)
	w := do(t, h, "GET", "/not-a-uuid", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
}

func TestHandler_Get_Found(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	svc, h := newTestServer(t, env)
	ctx := context.Background()
	host, err := svc.Create(ctx, validCreateInput(nodeID, env.inboundFor(nodeID)))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := do(t, h, "GET", "/"+host.ID.String(), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got Host
	decode(t, w, &got)
	if got.ID != host.ID {
		t.Errorf("id = %s, want %s", got.ID, host.ID)
	}
}

// --- update -------------------------------------------------------------

func TestHandler_Update_Success(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	svc, h := newTestServer(t, env)
	ctx := context.Background()
	host, err := svc.Create(ctx, validCreateInput(nodeID, env.inboundFor(nodeID)))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	displayName := "Netherlands"
	enabled := false
	w := do(t, h, "PUT", "/"+host.ID.String(), updateRequest{
		DisplayName: &displayName,
		Enabled:     &enabled,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got Host
	decode(t, w, &got)
	if got.DisplayName != "Netherlands" {
		t.Errorf("DisplayName = %q", got.DisplayName)
	}
	if got.Enabled {
		t.Error("Enabled should be false")
	}
}

func TestHandler_Update_NotFoundReturns404(t *testing.T) {
	env := makeSvc(t)
	_, h := newTestServer(t, env)
	w := do(t, h, "PUT", "/"+uuid.NewString(), updateRequest{})
	if w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}

func TestHandler_Update_ValidationErrorReturns400(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	svc, h := newTestServer(t, env)
	ctx := context.Background()
	host, err := svc.Create(ctx, validCreateInput(nodeID, env.inboundFor(nodeID)))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Try to change to type=chain — should fail
	// validation.
	bad := HostType("chain")
	w := do(t, h, "PUT", "/"+host.ID.String(), updateRequest{Type: &bad})
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
}

// --- delete -------------------------------------------------------------

func TestHandler_Delete_Success(t *testing.T) {
	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	svc, h := newTestServer(t, env)
	ctx := context.Background()
	host, err := svc.Create(ctx, validCreateInput(nodeID, env.inboundFor(nodeID)))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := do(t, h, "DELETE", "/"+host.ID.String(), nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("code = %d, want 204", w.Code)
	}
	if _, err := svc.Get(ctx, host.ID); err == nil {
		t.Error("host should be deleted")
	}
}

func TestHandler_Delete_NotFoundReturns404(t *testing.T) {
	env := makeSvc(t)
	_, h := newTestServer(t, env)
	w := do(t, h, "DELETE", "/"+uuid.NewString(), nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}

// --- middleware ---------------------------------------------------------

func TestHandler_RequiresScopeHosts(t *testing.T) {
	env := makeSvc(t, uuid.New())
	hostSvc := env.svc
	denyAuth := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{
				Scopes: auth.Scopes{auth.ScopeRead},
			}
			ctx := r.Context()
			ctx = auth.WithClaims(ctx, claims)
			h.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	r := Router(hostSvc, denyAuth)
	w := do(t, r, "GET", "/", nil)
	if w.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", w.Code)
	}
}
