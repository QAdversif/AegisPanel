// SPDX-License-Identifier: AGPL-3.0-or-later

package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/QAdversif/AegisPanel/internal/cores"
)

func TestProvider_NameAndVersion(t *testing.T) {
	p := New()
	if p.Name() != ProviderName {
		t.Fatalf("Name = %q, want %q", p.Name(), ProviderName)
	}
	if p.Version() != ProviderVersion {
		t.Fatalf("Version = %q, want %q", p.Version(), ProviderVersion)
	}
}

func TestProvider_CapabilitiesContainExpectedFlags(t *testing.T) {
	p := New()
	caps := p.Capabilities()
	if caps.Name != ProviderName {
		t.Fatalf("Capabilities.Name = %q, want %q", caps.Name, ProviderName)
	}
	// We do not assert the full flag set (it grows over
	// time) — just the protocol-level guarantees that
	// Phase 1 commits to in the PR description.
	for _, want := range []cores.Capability{
		cores.CapVLESS,
		cores.CapVLESSReality,
		cores.CapVLESSXTLSVision,
		cores.CapHY2,
		cores.CapShadowsocks,
		cores.CapTrojan,
	} {
		if !caps.Supports(want) {
			t.Errorf("Capabilities missing %q (Phase 1 commitment)", want)
		}
	}
	// WIREGUARD is explicitly Phase 4+; the sing-box
	// provider should not advertise it.
	for _, not := range []cores.Capability{cores.CapTUIC} {
		// TUIC is not in Phase 1 commit; deliberately
		// not advertised. CapTUIC is left in the enum so
		// a later PR can add it without a code change
		// outside this package.
		if caps.Supports(not) {
			t.Errorf("Capabilities should not advertise %q in Phase 1", not)
		}
	}
}

func TestProvider_CapabilitiesMarshalJSON_IsSortedAndDeduped(t *testing.T) {
	p := New()
	caps := p.Capabilities()
	// Caps.MarshalJSON is the wire format the panel UI
	// diffs; it must be deterministic. The dedup +
	// sort is in cores.Capabilities.MarshalJSON, not in
	// the provider — this test guards against a future
	// refactor that bypasses it.
	raw, err := caps.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	// The JSON is {"name":...,"version":...,"capabilities":[...]}
	// — parse the capabilities slice to inspect order.
	var doc struct {
		Name         string   `json:"name"`
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Name != ProviderName {
		t.Fatalf("name = %q, want %q", doc.Name, ProviderName)
	}
	for i := 1; i < len(doc.Capabilities); i++ {
		if doc.Capabilities[i-1] >= doc.Capabilities[i] {
			t.Fatalf("capabilities not sorted: %v", doc.Capabilities)
		}
	}
}

// TestInit_RegisteredInDefaultRegistry confirms that the
// package init() side-effect landed. The init runs when
// the test binary loads the package; if it panics, this
// test never executes (the test runner reports the panic
// up front). If init silently fails to register, the
// Get below returns an error.
func TestInit_RegisteredInDefaultRegistry(t *testing.T) {
	provider, err := cores.Get(ProviderName)
	if err != nil {
		t.Fatalf("init did not register %q in the default registry: %v", ProviderName, err)
	}
	if provider.Name() != ProviderName {
		t.Fatalf("registry returned provider %q, want %q", provider.Name(), ProviderName)
	}
}

func TestProvider_Diff_EmptyWhenEqual(t *testing.T) {
	p := New()
	raw := []byte(`{"inbounds":[]}` + "\n")
	d, err := p.Diff(raw, raw)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if d != "" {
		t.Fatalf("expected empty diff for equal configs, got %q", d)
	}
}

func TestProvider_Diff_NonEmptyWhenDifferent(t *testing.T) {
	p := New()
	prev := []byte("{\"inbounds\":[]}\n")
	next := []byte("{\"inbounds\":[{\"type\":\"direct\"}]}\n")
	d, err := p.Diff(prev, next)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if d == "" {
		t.Fatal("expected non-empty diff for different configs")
	}
	// The diff should be a unified diff — it carries the
	// "--- prev" / "+++ next" headers.
	if !strings.Contains(d, "--- prev") || !strings.Contains(d, "+++ next") {
		t.Fatalf("diff is not a unified diff: %q", d)
	}
}

func TestProvider_Apply_ReturnsNotImplemented(t *testing.T) {
	p := New()
	err := p.Apply(context.Background(), "node-1", []byte("{}"))
	if err == nil {
		t.Fatal("Apply should return an error in Phase 1")
	}
	if !errors.Is(err, ErrApplyNotImplemented) {
		t.Fatalf("Apply error = %v, want wraps ErrApplyNotImplemented", err)
	}
	// The error message should also surface the node ID
	// so an operator reading a panel log can see which
	// node was being targeted.
	if !strings.Contains(err.Error(), "node-1") {
		t.Fatalf("Apply error should mention node ID, got %q", err.Error())
	}
}

func TestProvider_ParseStatus_UnknownStub(t *testing.T) {
	p := New()
	s, err := p.ParseStatus(nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Status != "unknown" {
		t.Fatalf("status = %q, want unknown", s.Status)
	}
	if s.Version != ProviderVersion {
		t.Fatalf("version = %q, want %q", s.Version, ProviderVersion)
	}
}

func TestProvider_ParseStats_EmptyStub(t *testing.T) {
	p := New()
	stats, err := p.ParseStats([]byte(`{"anything":1}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("stats should be empty in Phase 1 stub, got %v", stats)
	}
}
