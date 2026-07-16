// SPDX-License-Identifier: AGPL-3.0-or-later

package subscription

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// handlerFixture is the same as newFixture from
// service_test.go but exposes a *Service that the
// handler tests can use to build a Router directly.
// The router is mounted under /sub to mirror the
// production layout (router.Build mounts at
// /api/v1/sub; the test mirror uses /sub to keep
// the test target short).
type handlerFixture struct {
	svc    *Service
	router http.Handler
}

func newHandlerFixture(t *testing.T) *handlerFixture {
	t.Helper()
	f := newFixture(t)
	mux := chi.NewRouter()
	mux.Mount("/sub", Router(f.sub))
	return &handlerFixture{
		svc:    f.sub,
		router: mux,
	}
}

// do runs an HTTP request through the router and
// returns the recorder. `target` is the full URL path
// the router should see, e.g. "/sub/tok-alice".
func (hf *handlerFixture) do(t *testing.T, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	w := httptest.NewRecorder()
	hf.router.ServeHTTP(w, req)
	return w
}

// --- handleRender --------------------------------------------------

func TestHandler_RenderBase64_Default(t *testing.T) {
	hf := newHandlerFixture(t)
	w := hf.do(t, http.MethodGet, "/sub/tok-alice")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/plain", got)
	}
	if got := w.Header().Get("Profile-Title"); got != "AegisPanel" {
		t.Errorf("Profile-Title = %q, want AegisPanel", got)
	}
	if got := w.Header().Get("Profile-Update-Interval"); got != "24" {
		t.Errorf("Profile-Update-Interval = %q, want 24", got)
	}
	if !strings.Contains(w.Header().Get("Subscription-Userinfo"), "upload=") {
		t.Errorf("Subscription-Userinfo = %q, want upload=…", w.Header().Get("Subscription-Userinfo"))
	}
	// Body should be valid base64 of a single vless URI.
	body, err := base64.StdEncoding.DecodeString(w.Body.String())
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if !strings.HasPrefix(string(body), "vless://") {
		t.Errorf("decoded = %q, want vless:// prefix", string(body))
	}
}

func TestHandler_RenderExplicitTarget(t *testing.T) {
	hf := newHandlerFixture(t)
	w := hf.do(t, http.MethodGet, "/sub/tok-alice?target=base64")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestHandler_Render_NotFound(t *testing.T) {
	hf := newHandlerFixture(t)
	w := hf.do(t, http.MethodGet, "/sub/tok-does-not-exist")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandler_Render_NotLive(t *testing.T) {
	// Take the live fixture user and force-expire it,
	// then re-fetch through the store so the cached
	// user pointer is not stale. The path is: live ->
	// Service.GetUserBySubToken returns the user ->
	// Service.ResolveEndpointsForUser returns
	// UserNotLiveError -> handler maps to 403.
	hf := newHandlerFixture(t)
	user, err := hf.svc.GetUserBySubToken(context.TODO(), "tok-alice")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	user.Status = UserStatusExpired
	// The store has the live copy; mutate it so the
	// next GetUserBySubToken returns the expired one.
	if ms, ok := hf.svc.store.(*MemoryStore); ok {
		ms.WithUser(user)
	}
	w := hf.do(t, http.MethodGet, "/sub/tok-alice")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestHandler_Render_UnsupportedTarget(t *testing.T) {
	hf := newHandlerFixture(t)
	w := hf.do(t, http.MethodGet, "/sub/tok-alice?target=garbage")
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", w.Code)
	}
}

func TestHandler_Render_SingboxNotImplemented(t *testing.T) {
	hf := newHandlerFixture(t)
	w := hf.do(t, http.MethodGet, "/sub/tok-alice?target=singbox")
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", w.Code)
	}
}

func TestHandler_Render_ClashNotImplemented(t *testing.T) {
	hf := newHandlerFixture(t)
	w := hf.do(t, http.MethodGet, "/sub/tok-alice?target=clash")
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", w.Code)
	}
}

func TestHandler_Render_HTML(t *testing.T) {
	hf := newHandlerFixture(t)
	w := hf.do(t, http.MethodGet, "/sub/tok-alice?target=html")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, "AegisPanel subscription") {
		t.Errorf("html body missing title; first 100 chars = %q", body[:min(len(body), 100)])
	}
	if !strings.Contains(body, "alice") {
		t.Errorf("html body missing username")
	}
	if !strings.Contains(body, "target=base64") {
		t.Errorf("html body missing subscription URL with target=base64")
	}
}

// --- auto-detect ----------------------------------------------------

func TestDetectFormat_ExplicitTarget(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?target=clash", nil)
	if got := detectFormat(r); got != FormatClash {
		t.Errorf("detectFormat = %s, want clash", got)
	}
}

func TestDetectFormat_AcceptYAML(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("Accept", "text/yaml")
	if got := detectFormat(r); got != FormatClash {
		t.Errorf("detectFormat = %s, want clash", got)
	}
}

func TestDetectFormat_AcceptJSON(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("Accept", "application/json")
	if got := detectFormat(r); got != FormatSingbox {
		t.Errorf("detectFormat = %s, want singbox", got)
	}
}

func TestDetectFormat_UserAgentClash(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("User-Agent", "ClashforWindows/0.20")
	if got := detectFormat(r); got != FormatClash {
		t.Errorf("detectFormat = %s, want clash", got)
	}
}

func TestDetectFormat_UserAgentHiddify(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("User-Agent", "HiddifyNG/2.5")
	if got := detectFormat(r); got != FormatSingbox {
		t.Errorf("detectFormat = %s, want singbox", got)
	}
}

func TestDetectFormat_Default(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	// No Accept, no User-Agent, no ?target=.
	if got := detectFormat(r); got != FormatBase64 {
		t.Errorf("detectFormat = %s, want base64", got)
	}
}

// --- helpers --------------------------------------------------------

// TestBuildUserInfoHeader exercises the small builder
// that the standard subscription headers rely on. It
// is included here (not in service_test) because the
// helper is HTTP-shaped (header string) and the
// service test does not need it.
func TestBuildUserInfoHeader(t *testing.T) {
	expire := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	got := buildUserInfoHeader(&User{
		TrafficUsedBytes:  42,
		TrafficLimitBytes: 100,
		ExpireAt:          &expire,
	})
	for _, want := range []string{
		"upload=42",
		"download=42",
		"total=100",
		"expire=1893456000", // 2030-01-01 UTC
	} {
		if !strings.Contains(got, want) {
			t.Errorf("buildUserInfoHeader = %q, want contains %q", got, want)
		}
	}
}

func TestBuildUserInfoHeader_NilExpire(t *testing.T) {
	// A user with no expire_at must not panic; the
	// header carries expire=0.
	got := buildUserInfoHeader(&User{TrafficLimitBytes: 1})
	if !strings.Contains(got, "expire=0") {
		t.Errorf("buildUserInfoHeader = %q, want expire=0", got)
	}
}

// --- extra: ensure the router is reachable end-to-end through chi ---

func TestHandler_Router_RejectsPost(t *testing.T) {
	// Router only registers GET; POST should 405.
	hf := newHandlerFixture(t)
	w := hf.do(t, http.MethodPost, "/sub/tok-alice")
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

// --- avoid a "declared and not used" failure for the
//
//	inbounds / nodes / hosts imports in this file ---
var _ = inbounds.ProtocolVLESS
var _ = nodes.StateNew
var _ = hosts.HostTypeDirect
var _ = uuid.Nil
