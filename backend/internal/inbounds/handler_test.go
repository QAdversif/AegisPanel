// SPDX-License-Identifier: AGPL-3.0-or-later

package inbounds

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// --- helpers ------------------------------------------------------------

// buildMux wires the inbounds router under a chi
// parent route so the {nodeId} URL param is available
// to the handlers. Returns the mux and the seed
// service for use in the tests.
func buildMux(t *testing.T, nodeSvc *nodes.Service) (*Service, http.Handler, uuid.UUID) {
	t.Helper()
	inbSvc := NewService(NewMemoryStore(), nodeSvc)
	withScope := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ctx = auth.WithClaims(ctx, &auth.Claims{
				Scopes: auth.Scopes{auth.ScopeNodes},
			})
			h.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	inner := Router(inbSvc, withScope)
	mux := chi.NewRouter()
	// Mirror the main.go mount: the parent route
	// sets the {nodeId} URL param; the inbounds
	// router reads it via chi.URLParam.
	mux.Mount("/api/v1/nodes/{nodeId}/inbounds", inner)
	// Seed a node so the test bodies can reference it.
	nodeID, err := seedNodeSvcWith(nodeSvc)
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return inbSvc, mux, nodeID
}

// seedNodeSvcWith uses an existing nodes service and
// returns a fresh node id.
func seedNodeSvcWith(svc *nodes.Service) (uuid.UUID, error) {
	id := uuid.New()
	_, err := svc.Create(context.Background(), nodes.CreateInput{
		ID:      id,
		Name:    "node-" + id.String()[:8],
		Region:  "eu",
		State:   nodes.StateOnline,
		Address: "1.2.3.4:22",
	})
	return id, err
}

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

func base(nodeID uuid.UUID) string {
	return "/api/v1/nodes/" + nodeID.String() + "/inbounds"
}

// --- list ---------------------------------------------------------------

func TestHandler_List_EmptyReturnsArrayNotNull(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	_, h, nodeID := buildMux(t, nodesSvc)
	w := do(t, h, "GET", base(nodeID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	var resp struct {
		Inbounds []*Inbound `json:"inbounds"`
	}
	decode(t, w, &resp)
	if resp.Inbounds == nil {
		t.Fatal("inbounds should be [] not null")
	}
	if len(resp.Inbounds) != 0 {
		t.Errorf("expected empty list, got %d", len(resp.Inbounds))
	}
}

func TestHandler_List_OnlyReturnsInboundsForThisNode(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	inbSvc, h, node1 := buildMux(t, nodesSvc)
	// Seed a second node directly via the nodes
	// service and create an inbound on it.
	node2, err := seedNodeSvcWith(nodesSvc)
	if err != nil {
		t.Fatalf("seed node2: %v", err)
	}
	ctx := context.Background()
	if _, err := inbSvc.Create(ctx, validCreateInput(node1)); err != nil {
		t.Fatalf("seed node1 inbound: %v", err)
	}
	if _, err := inbSvc.Create(ctx, validCreateInput(node2)); err != nil {
		t.Fatalf("seed node2 inbound: %v", err)
	}
	w := do(t, h, "GET", base(node1), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	var resp struct {
		Inbounds []*Inbound `json:"inbounds"`
	}
	decode(t, w, &resp)
	if len(resp.Inbounds) != 1 {
		t.Fatalf("got %d inbounds, want 1 (only node1's)", len(resp.Inbounds))
	}
	if resp.Inbounds[0].NodeID != node1 {
		t.Errorf("got node %s, want %s", resp.Inbounds[0].NodeID, node1)
	}
}

// --- create -------------------------------------------------------------

func TestHandler_Create_Success(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	_, h, nodeID := buildMux(t, nodesSvc)
	body := createRequest{
		Name:       "vless-main",
		Protocol:   ProtocolVLESS,
		ListenPort: 443,
	}
	w := do(t, h, "POST", base(nodeID), body)
	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var got Inbound
	decode(t, w, &got)
	if got.ID == uuid.Nil {
		t.Error("ID should be assigned")
	}
	if got.NodeID != nodeID {
		t.Errorf("NodeID = %s, want %s", got.NodeID, nodeID)
	}
}

func TestHandler_Create_ValidationErrorReturns400(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	_, h, nodeID := buildMux(t, nodesSvc)
	body := createRequest{
		Name:       "x",
		Protocol:   Protocol("wireguard"),
		ListenPort: 443,
	}
	w := do(t, h, "POST", base(nodeID), body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestHandler_Create_MalformedBodyReturns400(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	_, h, nodeID := buildMux(t, nodesSvc)
	req := httptest.NewRequest("POST", base(nodeID), strings.NewReader("not json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
}

func TestHandler_Create_DuplicateReturns409(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	_, h, nodeID := buildMux(t, nodesSvc)
	body := createRequest{
		Name:       "vless-main",
		Protocol:   ProtocolVLESS,
		ListenPort: 443,
	}
	if w := do(t, h, "POST", base(nodeID), body); w.Code != http.StatusCreated {
		t.Fatalf("first: code = %d, want 201", w.Code)
	}
	w := do(t, h, "POST", base(nodeID), body)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate: code = %d, want 409", w.Code)
	}
}

// --- get ----------------------------------------------------------------

func TestHandler_Get_NotFound(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	_, h, nodeID := buildMux(t, nodesSvc)
	w := do(t, h, "GET", base(nodeID)+"/"+uuid.NewString(), nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}

func TestHandler_Get_BadIDReturns400(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	_, h, nodeID := buildMux(t, nodesSvc)
	w := do(t, h, "GET", base(nodeID)+"/not-a-uuid", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
}

func TestHandler_Get_Found(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	inbSvc, h, nodeID := buildMux(t, nodesSvc)
	ctx := context.Background()
	item, err := inbSvc.Create(ctx, validCreateInput(nodeID))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := do(t, h, "GET", base(nodeID)+"/"+item.ID.String(), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got Inbound
	decode(t, w, &got)
	if got.ID != item.ID {
		t.Errorf("id = %s, want %s", got.ID, item.ID)
	}
}

// --- update -------------------------------------------------------------

func TestHandler_Update_Success(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	inbSvc, h, nodeID := buildMux(t, nodesSvc)
	ctx := context.Background()
	item, err := inbSvc.Create(ctx, validCreateInput(nodeID))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	enabled := false
	w := do(t, h, "PUT", base(nodeID)+"/"+item.ID.String(), updateRequest{
		Enabled: &enabled,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got Inbound
	decode(t, w, &got)
	if got.Enabled {
		t.Error("Enabled should be false")
	}
}

func TestHandler_Update_NotFoundReturns404(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	_, h, nodeID := buildMux(t, nodesSvc)
	w := do(t, h, "PUT", base(nodeID)+"/"+uuid.NewString(), updateRequest{})
	if w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}

// --- delete -------------------------------------------------------------

func TestHandler_Delete_Success(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	inbSvc, h, nodeID := buildMux(t, nodesSvc)
	ctx := context.Background()
	item, err := inbSvc.Create(ctx, validCreateInput(nodeID))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := do(t, h, "DELETE", base(nodeID)+"/"+item.ID.String(), nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("code = %d, want 204", w.Code)
	}
	if _, err := inbSvc.Get(ctx, item.ID); err == nil {
		t.Error("inbound should be deleted")
	}
}

func TestHandler_Delete_NotFoundReturns404(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	_, h, nodeID := buildMux(t, nodesSvc)
	w := do(t, h, "DELETE", base(nodeID)+"/"+uuid.NewString(), nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}
