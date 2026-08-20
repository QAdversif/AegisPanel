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
	// Mirror the main.go mount for the panel-wide
	// /api/v1/inbounds collection endpoint.
	mux.Mount("/api/v1/inbounds", TopLevelRouter(inbSvc, withScope))
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

// --- list-all (panel-wide) ----------------------------------------------

// TestHandler_ListAll_NoAuthReturns401 confirms the
// top-level /api/v1/inbounds endpoint enforces auth
// the same way the per-node router does: no token
// means 401. The auth middleware used here is the
// real auth.Service.Middleware so the test exercises
// the same path the production router does.
func TestHandler_ListAll_NoAuthReturns401(t *testing.T) {
	inbSvc := NewService(NewMemoryStore(), nil)
	signer := auth.NewSigner("test-secret-very-long-and-very-secret-32+")
	authSvc := auth.NewService(signer, auth.NewMemoryStore())
	r := TopLevelRouter(inbSvc, authSvc.Middleware())
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

// TestHandler_ListAll_ReturnsInboundsAcrossNodes
// confirms the panel-wide endpoint returns a flat
// array carrying nodeId on each record. Seed two
// nodes, create one inbound per node, GET the
// top-level endpoint, and verify:
//
//   - the response shape is { inbounds: [...] } with
//     a non-nil array (matches the per-node endpoint);
//   - the array contains every inbound;
//   - each record's NodeID matches one of the two
//     seeded nodes (the contract the UI relies on
//     to group without a second round-trip).
func TestHandler_ListAll_ReturnsInboundsAcrossNodes(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	inbSvc, h, node1 := buildMux(t, nodesSvc)
	ctx := context.Background()
	// Add a second node + an inbound on it.
	node2, err := seedNodeSvcWith(nodesSvc)
	if err != nil {
		t.Fatalf("seed node2: %v", err)
	}
	in1, err := inbSvc.Create(ctx, validCreateInput(node1))
	if err != nil {
		t.Fatalf("seed node1 inbound: %v", err)
	}
	in2, err := inbSvc.Create(ctx, validCreateInput(node2))
	if err != nil {
		t.Fatalf("seed node2 inbound: %v", err)
	}
	w := do(t, h, "GET", "/api/v1/inbounds/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Inbounds []*Inbound `json:"inbounds"`
	}
	decode(t, w, &resp)
	if resp.Inbounds == nil {
		t.Fatal("inbounds should be [] not null")
	}
	if len(resp.Inbounds) != 2 {
		t.Fatalf("got %d inbounds, want 2", len(resp.Inbounds))
	}
	// Both records must carry their NodeID so the
	// UI can group without re-fetching. The two
	// inbounds share the same Name / Port (they come
	// from validCreateInput), so we look up by ID
	// rather than by position.
	byID := map[uuid.UUID]uuid.UUID{
		in1.ID: uuid.Nil,
		in2.ID: uuid.Nil,
	}
	for _, got := range resp.Inbounds {
		expectedNode, ok := byID[got.ID]
		if !ok {
			t.Errorf("unexpected inbound id %s in response", got.ID)
			continue
		}
		switch got.ID {
		case in1.ID:
			expectedNode = node1
		case in2.ID:
			expectedNode = node2
		}
		if got.NodeID != expectedNode {
			t.Errorf("inbound %s: NodeID = %s, want %s", got.ID, got.NodeID, expectedNode)
		}
	}
}

// TestHandler_ListAll_SortedForStableDiffs confirms
// the response is sorted (NodeID then ListenPort)
// so the UI's diffing / keying logic stays stable
// across requests. Seeded inbounds share the same
// port and the same name, so the secondary sort key
// (Name) is not exercised here — that's covered by
// the MemoryStore test.
func TestHandler_ListAll_SortedForStableDiffs(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	inbSvc, h, node1 := buildMux(t, nodesSvc)
	ctx := context.Background()
	node2, err := seedNodeSvcWith(nodesSvc)
	if err != nil {
		t.Fatalf("seed node2: %v", err)
	}
	if _, err := inbSvc.Create(ctx, validCreateInput(node1)); err != nil {
		t.Fatalf("seed node1: %v", err)
	}
	if _, err := inbSvc.Create(ctx, validCreateInput(node2)); err != nil {
		t.Fatalf("seed node2: %v", err)
	}
	w := do(t, h, "GET", "/api/v1/inbounds/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	var resp struct {
		Inbounds []*Inbound `json:"inbounds"`
	}
	decode(t, w, &resp)
	if len(resp.Inbounds) < 2 {
		t.Fatalf("need >=2 inbounds, got %d", len(resp.Inbounds))
	}
	for i := 1; i < len(resp.Inbounds); i++ {
		prev, cur := resp.Inbounds[i-1], resp.Inbounds[i]
		if prev.NodeID.String() > cur.NodeID.String() {
			t.Errorf("position %d: NodeID %s > %s (not sorted)", i, prev.NodeID, cur.NodeID)
		}
	}
}

// TestHandler_ListAll_EmptyReturnsArrayNotNull mirrors
// the per-node /api/v1/nodes/{id}/inbounds test: an
// empty store must serialize as an array, not JSON
// `null`, so the frontend can iterate without a guard.
func TestHandler_ListAll_EmptyReturnsArrayNotNull(t *testing.T) {
	nodesSvc, _ := seedNodeSvc(t)
	_, h, _ := buildMux(t, nodesSvc)
	w := do(t, h, "GET", "/api/v1/inbounds/", nil)
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
