// SPDX-License-Identifier: AGPL-3.0-or-later

package cores

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeProvider is a test double — a noop with a different
// name. Used to populate the registry with several distinct
// entries.
type fakeProvider struct{ name string }

func (f *fakeProvider) Name() string    { return f.name }
func (f *fakeProvider) Version() string { return "test" }
func (f *fakeProvider) Capabilities() Capabilities {
	return Capabilities{Name: f.name, Version: "test"}
}
func (f *fakeProvider) RenderConfig(_ context.Context, _ CoreConfig) (string, error) {
	return "{}", nil
}
func (f *fakeProvider) ValidateConfig(_ context.Context, _ []byte) error  { return nil }
func (f *fakeProvider) Diff(_, _ []byte) (string, error)                  { return "", nil }
func (f *fakeProvider) Apply(_ context.Context, _ string, _ []byte) error { return nil }
func (f *fakeProvider) ParseStatus(_ []byte) (CoreStatus, error) {
	return CoreStatus{Status: "fake"}, nil
}
func (f *fakeProvider) ParseStats(_ []byte) ([]UserStat, error) { return nil, nil }

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	p := &fakeProvider{name: "alpha"}
	if err := r.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, err := r.Get("alpha")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// CoreProvider is an interface — compare by Name() and
	// pointer identity to be sure we got the same instance
	// back, not a re-registered copy.
	if got.Name() != p.Name() {
		t.Fatalf("get returned a different provider: %q vs %q", got.Name(), p.Name())
	}
}

func TestRegistry_DuplicateName(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeProvider{name: "dup"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := r.Register(&fakeProvider{name: "dup"})
	if !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("err = %v, want ErrDuplicateName", err)
	}
}

func TestRegistry_NilProvider(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Fatal("expected error for nil provider, got nil")
	}
}

func TestRegistry_EmptyName(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeProvider{name: ""}); err == nil {
		t.Fatal("expected error for empty name, got nil")
	}
}

func TestRegistry_NotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get("missing")
	if err == nil {
		t.Fatal("expected error for missing provider, got nil")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("err = %v, want it to mention the name", err)
	}
}

func TestRegistry_ListSortedByName(t *testing.T) {
	r := NewRegistry()
	want := []string{"bravo", "alpha", "charlie"}
	for _, n := range want {
		if err := r.Register(&fakeProvider{name: n}); err != nil {
			t.Fatalf("register %s: %v", n, err)
		}
	}
	got := r.Names()
	wantSorted := []string{"alpha", "bravo", "charlie"}
	if len(got) != len(wantSorted) {
		t.Fatalf("len = %d, want %d", len(got), len(wantSorted))
	}
	for i, n := range wantSorted {
		if got[i] != n {
			t.Fatalf("Names()[%d] = %q, want %q", i, got[i], n)
		}
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeProvider{name: "temp"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Unregister("temp"); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if _, err := r.Get("temp"); err == nil {
		t.Fatal("Get after Unregister should fail")
	}
	// Unregister of an unknown name is a no-op, not an
	// error — this matches the "delete is idempotent"
	// convention used by other registries in the project.
	if err := r.Unregister("temp"); err != nil {
		t.Fatalf("unregister twice: %v", err)
	}
}

func TestRegistry_ConcurrentRegisterAndGet(t *testing.T) {
	// The registry is the first thing a server hits on
	// boot (every provider self-registers from init()), so
	// the concurrent path is the one that matters.
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			p := &fakeProvider{name: "p-" + string(rune('a'+i%26)) + string(rune('0'+i/26))}
			_ = r.Register(p)
		}(i)
		go func() {
			defer wg.Done()
			_ = r.List()
		}()
	}
	wg.Wait()
}

func TestCapabilities_Supports(t *testing.T) {
	caps := Capabilities{
		Name:    "x",
		Version: "1.0",
		Flags:   []Capability{CapVLESS, CapVLESSReality, CapShadowsocks},
	}
	if !caps.Supports(CapVLESS) {
		t.Fatal("VLESS not supported")
	}
	if !caps.Supports(CapVLESSReality) {
		t.Fatal("VLESS_REALITY not supported")
	}
	if caps.Supports(CapHY2) {
		t.Fatal("HY2 reported as supported but is not in flags")
	}
	var nilCaps *Capabilities
	if nilCaps.Supports(CapVLESS) {
		t.Fatal("nil receiver must return false")
	}
}

func TestCapabilities_MarshalJSON_SortedAndDeduped(t *testing.T) {
	// Capability lists in JSON are sorted alphabetically and
	// deduped — both so the wire format is stable across
	// providers and so the panel can diff capability
	// matrices without false positives.
	caps := Capabilities{
		Name:    "x",
		Version: "1.0",
		Flags:   []Capability{CapVLESS, CapShadowsocks, CapVLESS, CapHY2},
	}
	out, err := caps.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(out)
	want := `{"name":"x","version":"1.0","capabilities":["HY2","SHADOWSOCKS","VLESS"]}`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
