// SPDX-License-Identifier: AGPL-3.0-or-later

package users

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// newAdminTestRouter wires the users.AdminRouter with
// a stub auth middleware that injects a ScopeUsers
// claim. The test scope behaviour, not JWT
// verification.
func newAdminTestRouter(t *testing.T) (http.Handler, *Service) {
	t.Helper()
	store := NewMemoryStore(nil)
	svc := NewService(store)
	// auth middleware bypass — the tests assert
	// scope behaviour, not JWT verification.
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{
				Scopes: auth.Scopes{auth.ScopeUsers},
			}
			ctx := auth.WithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	return AdminRouter(svc, mw), svc
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func decodeJSONBytes(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode bytes: %v", err)
	}
	return body
}

func TestAdminHandler_ListEmpty(t *testing.T) {
	r, _ := newAdminTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := decodeJSON(t, w.Result())
	rows, _ := got["users"].([]any)
	if len(rows) != 0 {
		t.Errorf("users = %d, want 0", len(rows))
	}
}

func TestAdminHandler_CreateAndGet(t *testing.T) {
	r, svc := newAdminTestRouter(t)

	body, _ := json.Marshal(createUserRequest{
		Username:          "alice",
		Status:            StatusActive,
		TrafficLimitBytes: 1024 * 1024 * 1024,
		DeviceLimit:       3,
	})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201; body = %s", w.Code, w.Body.String())
	}
	created := decodeJSONBytes(t, w.Body.Bytes())
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create: id = empty, want uuid")
	}
	// sub_token must be 64 hex chars (32 bytes of
	// random from the canonical d.1 users.Service
	// token mint).
	if tok, _ := created["sub_token"].(string); len(tok) != 64 {
		t.Errorf("sub_token = %q (len %d), want 64", tok, len(tok))
	}

	// GET /{id}
	{
		req := httptest.NewRequest(http.MethodGet, "/"+id, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get: status = %d, want 200", w.Code)
		}
		got := decodeJSONBytes(t, w.Body.Bytes())
		if got["username"] != "alice" {
			t.Errorf("username = %v, want alice", got["username"])
		}
	}

	// Verify the Service-side read-back matches.
	u, err := svc.Get(context.TODO(), uuid.MustParse(id))
	if err != nil {
		t.Fatalf("users: %v", err)
	}
	if u.Username != "alice" {
		t.Errorf("users username = %q, want alice", u.Username)
	}
}

func TestAdminHandler_CreateDuplicateUsername(t *testing.T) {
	r, _ := newAdminTestRouter(t)
	body, _ := json.Marshal(createUserRequest{Username: "alice"})

	// First insert: 201.
	{
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("first: status = %d, want 201", w.Code)
		}
	}
	// Second insert: 409.
	{
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusConflict {
			t.Errorf("second: status = %d, want 409", w.Code)
		}
	}
}

func TestAdminHandler_CreateMissingUsername(t *testing.T) {
	r, _ := newAdminTestRouter(t)
	body, _ := json.Marshal(createUserRequest{Username: ""})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestAdminHandler_PatchAndRotate(t *testing.T) {
	r, _ := newAdminTestRouter(t)

	// Seed.
	body, _ := json.Marshal(createUserRequest{Username: "bob", DeviceLimit: 3})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed: status = %d, want 201", w.Code)
	}
	seed := decodeJSONBytes(t, w.Body.Bytes())
	id, _ := seed["id"].(string)
	originalToken, _ := seed["sub_token"].(string)

	// PATCH: bump device limit + change status.
	{
		patch, _ := json.Marshal(updateUserRequest{
			DeviceLimit: intPtr(5),
			Status:      statusPtr(StatusGrace),
		})
		req := httptest.NewRequest(http.MethodPatch, "/"+id, bytes.NewReader(patch))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("patch: status = %d, want 200; body = %s", w.Code, w.Body.String())
		}
		got := decodeJSONBytes(t, w.Body.Bytes())
		if int(got["device_limit"].(float64)) != 5 {
			t.Errorf("device_limit = %v, want 5", got["device_limit"])
		}
		if got["status"] != string(StatusGrace) {
			t.Errorf("status = %v, want grace", got["status"])
		}
	}

	// ROTATE TOKEN: 0-second grace.
	{
		rotBody, _ := json.Marshal(rotateTokenRequest{GraceWindowSeconds: intPtr(0)})
		req := httptest.NewRequest(http.MethodPost, "/"+id+"/rotate-token", bytes.NewReader(rotBody))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("rotate: status = %d, want 200; body = %s", w.Code, w.Body.String())
		}
		got := decodeJSONBytes(t, w.Body.Bytes())
		newToken, _ := got["sub_token"].(string)
		prevToken, _ := got["sub_token_prev"].(string)
		if newToken == originalToken {
			t.Errorf("sub_token unchanged after rotate: %q", newToken)
		}
		if prevToken != originalToken {
			t.Errorf("sub_token_prev = %q, want %q", prevToken, originalToken)
		}
		// 0-second grace from the API boundary is
		// mapped to the users.Service default
		// (24h, per the d.1 design — see
		// `users.Service.RotateSubToken`).
	}
}

func TestAdminHandler_GetMissing(t *testing.T) {
	r, _ := newAdminTestRouter(t)
	id := uuid.NewString()
	req := httptest.NewRequest(http.MethodGet, "/"+id, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestAdminHandler_RotateTokenNotFound(t *testing.T) {
	r, _ := newAdminTestRouter(t)
	id := uuid.NewString()
	body, _ := json.Marshal(rotateTokenRequest{})
	req := httptest.NewRequest(http.MethodPost, "/"+id+"/rotate-token", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestAdminHandler_RejectsNonUsersScope(t *testing.T) {
	store := NewMemoryStore(nil)
	svc := NewService(store)
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{
				Scopes: auth.Scopes{auth.ScopeRead, auth.ScopeWrite},
			}
			ctx := auth.WithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	r := AdminRouter(svc, mw)

	for _, path := range []string{"/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403 (no users scope)", path, w.Code)
		}
	}
}

func intPtr(v int) *int { return &v }
func statusPtr(v Status) *Status {
	return &v
}
