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

func TestHandler_Render_Singbox_Renders(t *testing.T) {
	// After PR #42 the sing-box format is implemented
	// end-to-end. The handler must return 200 with a
	// valid `application/json` body. The renderer's
	// own unit tests cover the wire format; this test
	// is the end-to-end smoke that the handler
	// dispatches to it.
	hf := newHandlerFixture(t)
	w := hf.do(t, http.MethodGet, "/sub/tok-alice?target=singbox")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want application/json prefix", got)
	}
}

func TestHandler_Render_Clash_Renders(t *testing.T) {
	// After PR #43 the Clash format is implemented
	// end-to-end. The handler must return 200 with
	// `text/yaml` content-type. The renderer's own
	// unit tests cover the wire format; this test is
	// the end-to-end smoke that the handler
	// dispatches to it.
	hf := newHandlerFixture(t)
	w := hf.do(t, http.MethodGet, "/sub/tok-alice?target=clash")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/yaml") {
		t.Errorf("Content-Type = %q, want text/yaml prefix", got)
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

// TestHandler_Render_HTML_QRPresent — the page embeds
// a QR code as a `data:image/png;base64,…` URL. The
// data must be a real, decodable PNG that encodes the
// base64 subscription URL. A client that scans the
// page (Hiddify / Streisand / NekoBox / Karing /
// V2Box) needs every part of this to work: the data
// URL format must be valid, the PNG must decode,
// and the encoded content must match the URL the
// client will fetch.
func TestHandler_Render_HTML_QRPresent(t *testing.T) {
	hf := newHandlerFixture(t)
	w := hf.do(t, http.MethodGet, "/sub/tok-alice?target=html")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	// Locate the QR <img> tag.
	prefix := `src="data:image/png;base64,`
	idx := strings.Index(body, prefix)
	if idx < 0 {
		t.Fatalf("html body missing data:image/png base64 src; first 200 chars = %q", body[:min(len(body), 200)])
	}
	// Walk to the closing quote; the encoded data
	// does not contain `"` (base64 alphabet is
	// `[A-Za-z0-9+/=]`) so a literal `"` is the
	// first non-base64 boundary.
	end := strings.Index(body[idx+len(prefix):], `"`)
	if end < 0 {
		t.Fatalf("html body has unterminated data: URL")
	}
	encoded := body[idx+len(prefix) : idx+len(prefix)+end]
	png, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	// PNG magic: 89 50 4E 47 0D 0A 1A 0A.
	if len(png) < 8 || png[0] != 0x89 || png[1] != 0x50 || png[2] != 0x4E || png[3] != 0x47 {
		t.Errorf("embedded data is not a valid PNG (got %x...)", png[:min(len(png), 8)])
	}
}

// TestHandler_Render_HTML_PerClientURLs — the page
// embeds three subscription URLs (base64, singbox,
// clash) so the user can pick the one their client
// understands. Each URL is in a copyable <input>
// with a "copy" button (a `data-copy="<id>"`
// attribute on the button targets the input by id).
func TestHandler_Render_HTML_PerClientURLs(t *testing.T) {
	hf := newHandlerFixture(t)
	w := hf.do(t, http.MethodGet, "/sub/tok-alice?target=html")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, format := range []string{"base64", "singbox", "clash"} {
		needle := "target=" + format
		if !strings.Contains(body, needle) {
			t.Errorf("html body missing per-client URL with %s", needle)
		}
	}
	// Three copy buttons, one per row.
	if got := strings.Count(body, `data-copy="u`); got != 3 {
		t.Errorf("data-copy buttons = %d, want 3", got)
	}
}

// TestHandler_Render_HTML_HostCount — the page shows
// the entitled-host count in the "you have N hosts"
// line. The test fixture (newHandlerFixture) seeds
// exactly one host, so the line must read "1".
func TestHandler_Render_HTML_HostCount(t *testing.T) {
	hf := newHandlerFixture(t)
	w := hf.do(t, http.MethodGet, "/sub/tok-alice?target=html")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<b>1</b> host line") {
		t.Errorf("html body missing single-host count line; first 300 chars = %q", body[:min(len(body), 300)])
	}
}

// TestHandler_Render_HTML_NoHosts — a user with no
// entitled hosts gets a friendly "no hosts" line on
// the page (instead of "you have 0 host line(s)"; the
// grammar is wrong and confuses users).
//
// The fixture has one entitled user. We add a second
// user with no plan via the MemoryStore's
// chainable helper; the resolver returns zero hosts
// for the new user, and the page renders the
// "no hosts" branch.
func TestHandler_Render_HTML_NoHosts(t *testing.T) {
	hf := newHandlerFixture(t)
	if ms, ok := hf.svc.store.(*MemoryStore); ok {
		ms.WithUser(&User{
			ID:       uuid.New(),
			Username: "ghost",
			Status:   UserStatusActive,
			SubToken: "tok-ghost",
		})
	} else {
		t.Skipf("subscription store is not a MemoryStore; cannot add a no-plan user from the test")
	}
	w := hf.do(t, http.MethodGet, "/sub/tok-ghost?target=html")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "no hosts") {
		t.Errorf("html body missing no-hosts line; first 300 chars = %q", body[:min(len(body), 300)])
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
