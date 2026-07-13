// SPDX-License-Identifier: AGPL-3.0-or-later

package cores

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestMount_EmptyRegistry_ReturnsEmptyList(t *testing.T) {
	// An empty registry must return 200 with an empty
	// `cores` array — not a 404 or null. The UI relies on
	// the shape to render "no providers wired in" cleanly.
	r := NewRegistry()
	rr := serveCores(t, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body struct {
		Cores []any `json:"cores"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Cores == nil {
		t.Fatal("cores should be [] (empty), not null")
	}
	if len(body.Cores) != 0 {
		t.Fatalf("len(cores) = %d, want 0", len(body.Cores))
	}
}

func TestMount_ListingSortedByName(t *testing.T) {
	r := NewRegistry()
	// Register out of order to make sure List() sorts.
	if err := r.Register(&fakeProvider{name: "charlie"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register(&fakeProvider{name: "alpha"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register(&fakeProvider{name: "bravo"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	rr := serveCores(t, r)
	var body struct {
		Cores []struct {
			Name string `json:"name"`
		} `json:"cores"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"alpha", "bravo", "charlie"}
	if len(body.Cores) != len(want) {
		t.Fatalf("len = %d, want %d", len(body.Cores), len(want))
	}
	for i, n := range want {
		if body.Cores[i].Name != n {
			t.Fatalf("cores[%d].name = %q, want %q", i, body.Cores[i].Name, n)
		}
	}
}

func TestMount_ExposesCapabilities(t *testing.T) {
	r := NewRegistry()
	// fakeProvider's Capabilities() returns Flags=nil. To
	// exercise the wire format we register a richer fake.
	if err := r.Register(&capProvider{name: "x"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	rr := serveCores(t, r)
	var body struct {
		Cores []struct {
			Name         string   `json:"name"`
			Version      string   `json:"version"`
			Capabilities []string `json:"capabilities"`
		} `json:"cores"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Cores) != 1 {
		t.Fatalf("len = %d, want 1", len(body.Cores))
	}
	got := body.Cores[0]
	if got.Name != "x" {
		t.Fatalf("name = %q, want x", got.Name)
	}
	wantCaps := []string{"HY2", "SHADOWSOCKS", "VLESS", "VLESS_REALITY"}
	if len(got.Capabilities) != len(wantCaps) {
		t.Fatalf("len(capabilities) = %d, want %d (%v)", len(got.Capabilities), len(wantCaps), got.Capabilities)
	}
	for i, c := range wantCaps {
		if got.Capabilities[i] != c {
			t.Fatalf("capabilities[%d] = %q, want %q (full: %v)", i, got.Capabilities[i], c, got.Capabilities)
		}
	}
}

func TestMount_ContentType(t *testing.T) {
	r := NewRegistry()
	rr := serveCores(t, r)
	got := rr.Header().Get("Content-Type")
	if got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want application/json; charset=utf-8", got)
	}
}

// serveCores mounts the handler on a fresh router and runs a
// GET /cores against it. Each test gets its own registry so
// they do not leak state.
func serveCores(t *testing.T, r *Registry) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	Mount(router)

	// Swap the default registry for the per-test one.
	// We use a package-private helper to avoid exposing
	// reset logic on the public Default registry.
	prev := Default
	Default = r
	t.Cleanup(func() { Default = prev })

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/cores", nil)
	router.ServeHTTP(rr, req)
	return rr
}

// capProvider is a fake that exposes a non-empty capability
// set so we can test that the JSON wire format is sorted and
// deduped.
type capProvider struct{ name string }

func (p *capProvider) Name() string    { return p.name }
func (p *capProvider) Version() string { return "1.0" }
func (p *capProvider) Capabilities() Capabilities {
	return Capabilities{
		Name:    p.name,
		Version: "1.0",
		Flags: []Capability{
			CapVLESS, CapShadowsocks, CapVLESS, // duplicate on purpose
			CapHY2, CapVLESSReality,
		},
	}
}
func (p *capProvider) RenderConfig(_ context.Context, _ CoreConfig) (string, error) {
	return "{}", nil
}
func (p *capProvider) ValidateConfig(_ context.Context, _ []byte) error  { return nil }
func (p *capProvider) Diff(_, _ []byte) (string, error)                  { return "", nil }
func (p *capProvider) Apply(_ context.Context, _ string, _ []byte) error { return nil }
func (p *capProvider) ParseStatus(_ []byte) (CoreStatus, error)          { return CoreStatus{Status: "x"}, nil }
func (p *capProvider) ParseStats(_ []byte) ([]UserStat, error)           { return nil, nil }
