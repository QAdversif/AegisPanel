// SPDX-License-Identifier: AGPL-3.0-or-later

package audits

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// newTestRouter wires the audits HTTP surface
// against a fresh MemoryStore, with the auth
// middleware pre-seeded with admin claims.
func newTestRouter(t *testing.T) (http.Handler, *Service) {
	t.Helper()
	store := NewMemoryStore()
	clock := func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }
	store.SetClock(clock)
	svc := NewService(store)
	svc.SetClock(clock)

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{
				Scopes: auth.Scopes{auth.ScopeAdmin, auth.ScopeAudits},
			}
			ctx := auth.WithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	return Router(svc, mw), svc
}

// seedEntries is a tiny helper that inserts a few
// entries via the service (so the IDs and timestamps
// are the real ones the Store would mint).
func seedEntries(t *testing.T, svc *Service) {
	t.Helper()
	for _, e := range []Entry{
		{Action: "user.create", ResourceType: "user", ActorID: "u-1", ActorUsername: "admin"},
		{Action: "user.update", ResourceType: "user", ActorID: "u-1", ActorUsername: "admin"},
		{Action: "host.create", ResourceType: "host", ActorID: "u-1", ActorUsername: "admin"},
	} {
		_, err := svc.Record(context.Background(), e)
		if err != nil {
			t.Fatalf("seed Record: %v", err)
		}
	}
}

func TestHandler_List_Empty(t *testing.T) {
	r, _ := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Audits []*AuditEntry `json:"audits"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Audits == nil {
		t.Error("audits field should be an empty array, not null")
	}
	if len(body.Audits) != 0 {
		t.Errorf("len(audits) = %d, want 0", len(body.Audits))
	}
}

func TestHandler_List_WithEntries(t *testing.T) {
	r, svc := newTestRouter(t)
	seedEntries(t, svc)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Audits []*AuditEntry `json:"audits"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Audits) != 3 {
		t.Errorf("len(audits) = %d, want 3", len(body.Audits))
	}
	// The list path must elide Before/After to
	// keep responses compact.
	for i, e := range body.Audits {
		if e.Before != nil {
			t.Errorf("audits[%d].Before = %v, want nil", i, e.Before)
		}
		if e.After != nil {
			t.Errorf("audits[%d].After = %v, want nil", i, e.After)
		}
	}
}

func TestHandler_List_FilterByAction(t *testing.T) {
	r, svc := newTestRouter(t)
	seedEntries(t, svc)

	req := httptest.NewRequest(http.MethodGet, "/?action=user.create", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Audits []*AuditEntry `json:"audits"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Audits) != 1 {
		t.Errorf("len(audits) = %d, want 1", len(body.Audits))
	}
	if body.Audits[0].Action != "user.create" {
		t.Errorf("action = %q, want user.create", body.Audits[0].Action)
	}
}

func TestHandler_List_BadSinceRejected(t *testing.T) {
	r, _ := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/?since=not-a-time", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandler_List_UntilBeforeSinceRejected(t *testing.T) {
	r, _ := newTestRouter(t)
	// until=2026-01-01T00:00:00Z, since=2027-01-01T00:00:00Z
	req := httptest.NewRequest(http.MethodGet, "/?since=2027-01-01T00:00:00Z&until=2026-01-01T00:00:00Z", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandler_Get_ReturnsFullEntry(t *testing.T) {
	r, svc := newTestRouter(t)
	// Insert one entry with before/after, so we can
	// verify the /{id} path returns them in full.
	row, err := svc.Record(context.Background(), Entry{
		Action: "user.update", ResourceType: "user", ResourceID: "u-42",
		Before: map[string]any{"name": "alice"},
		After:  map[string]any{"name": "bob"},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/"+row.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var got AuditEntry
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != row.ID {
		t.Errorf("id = %q, want %q", got.ID, row.ID)
	}
	if got.Before == nil {
		t.Error("Before should be populated on /{id}")
	}
	if got.After == nil {
		t.Error("After should be populated on /{id}")
	}
	if got.ResourceID != "u-42" {
		t.Errorf("ResourceID = %q, want u-42", got.ResourceID)
	}
}

func TestHandler_Get_NotFound(t *testing.T) {
	r, _ := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/9999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// TestHandler_RequiresScope is a regression test —
// the RequireScope middleware is in front of every
// route, so a request without the `audits` scope is
// rejected with 403.
func TestHandler_RequiresScope(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	// Claims without ScopeAudits.
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{
				Scopes: auth.Scopes{auth.ScopeRead},
			}
			ctx := auth.WithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	r := Router(svc, mw)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}
