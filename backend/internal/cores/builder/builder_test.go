// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Unit tests for the panel-state → CoreConfig builder.
// The builder is the seam where the inbounds table
// turns into a renderable DTO; the unit tests pin
// every branch of `BuildCoreConfigForNode` against a
// fake `ListInboundsByNode` so a future regression
// in the field mapping surfaces here, not in
// integration.

package builder

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/inbounds"
)

// fakeInboundsSource satisfies ListInboundsByNode for
// the tests. The `inbounds` field is what
// BuildCoreConfigForNode sees; `err` is what
// ListByNode returns so the error path can be
// exercised without standing up a real store.
type fakeInboundsSource struct {
	inbounds []*inbounds.Inbound
	err      error
	// calls tracks how many times the builder read
	// the source. Useful for the "no I/O when input
	// is empty" assertion.
	calls int
}

func (f *fakeInboundsSource) ListByNode(_ context.Context, _ uuid.UUID) ([]*inbounds.Inbound, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.inbounds, nil
}

func TestBuildCoreConfigForNode_NoInbounds(t *testing.T) {
	src := &fakeInboundsSource{}
	got, err := BuildCoreConfigForNode(context.Background(), src, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Inbounds) != 0 {
		t.Errorf("Inbounds len = %d, want 0", len(got.Inbounds))
	}
	// Experimental must still be non-nil so the sing-box
	// renderer finds the key and emits an empty config
	// (rather than erroring on missing key).
	if got.Experimental == nil {
		t.Fatal("Experimental = nil, want non-nil empty map (so the renderer can find the key)")
	}
	if _, ok := got.Experimental["inbound_params"]; !ok {
		t.Errorf("Experimental[inbound_params] missing")
	}
	if src.calls != 1 {
		t.Errorf("ListByNode calls = %d, want 1", src.calls)
	}
}

func TestBuildCoreConfigForNode_SourceError(t *testing.T) {
	src := &fakeInboundsSource{err: errors.New("pg: connection refused")}
	_, err := BuildCoreConfigForNode(context.Background(), src, uuid.New())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The error should be wrapped with the node ID so
	// the BatchedApplier log line shows which node's
	// render failed.
	if !contains(err.Error(), "list inbounds") {
		t.Errorf("error = %q, want it to mention the list operation", err.Error())
	}
}

// TestBuildCoreConfigForNode_Mapping is the headline
// test: a node with one of each protocol type renders
// into the CoreConfig shape the sing-box renderer
// expects. A regression here breaks the entire
// panel-side → singbox pipeline.
func TestBuildCoreConfigForNode_Mapping(t *testing.T) {
	nodeID := uuid.New()
	src := &fakeInboundsSource{
		inbounds: []*inbounds.Inbound{
			{
				ID:         uuid.New(),
				NodeID:     nodeID,
				Name:       "vless-in",
				Protocol:   inbounds.ProtocolVLESS,
				Listen:     "::",
				ListenPort: 443,
				Enabled:    true,
				Params: map[string]any{
					"port": 443,
					"uuid": "00000000-0000-0000-0000-000000000001",
					"flow": "xtls-rprx-vision",
					"tls":  map[string]any{"server_name": "cdn.example.com"},
				},
			},
			{
				ID:         uuid.New(),
				NodeID:     nodeID,
				Name:       "hy2-in",
				Protocol:   inbounds.ProtocolHysteria2,
				Listen:     "::",
				ListenPort: 443,
				Enabled:    true,
				Params: map[string]any{
					"port":     443,
					"password": "hy2-pass",
				},
			},
			{
				// Disabled inbound — must be excluded
				// from the CoreConfig so the agent
				// does not render an active listener
				// for it.
				ID:         uuid.New(),
				NodeID:     nodeID,
				Name:       "disabled-in",
				Protocol:   inbounds.ProtocolTrojan,
				Listen:     "::",
				ListenPort: 8443,
				Enabled:    false,
				Params:     map[string]any{"port": 8443, "password": "x"},
			},
		},
	}

	got, err := BuildCoreConfigForNode(context.Background(), src, nodeID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Two enabled inbounds, one disabled.
	if len(got.Inbounds) != 2 {
		t.Fatalf("Inbounds len = %d, want 2 (disabled excluded)", len(got.Inbounds))
	}

	// The order of specs is the order the source
	// returned; pin both names.
	wantTags := []string{"vless-in", "hy2-in"}
	for i, want := range wantTags {
		if got.Inbounds[i].Tag != want {
			t.Errorf("Inbounds[%d].Tag = %q, want %q", i, got.Inbounds[i].Tag, want)
		}
		if got.Inbounds[i].Type != string(inbounds.ProtocolVLESS) && got.Inbounds[i].Type != string(inbounds.ProtocolHysteria2) {
			t.Errorf("Inbounds[%d].Type = %q, want a Phase 1 protocol", i, got.Inbounds[i].Type)
		}
	}

	params, ok := got.Experimental["inbound_params"].(map[string]any)
	if !ok {
		t.Fatalf("Experimental[inbound_params] type = %T, want map[string]any", got.Experimental["inbound_params"])
	}
	if _, ok := params["vless-in"].(map[string]any); !ok {
		t.Errorf("params[vless-in] missing or wrong type")
	}
	if _, ok := params["hy2-in"].(map[string]any); !ok {
		t.Errorf("params[hy2-in] missing or wrong type")
	}
	if _, hasDisabled := params["disabled-in"]; hasDisabled {
		t.Errorf("params[disabled-in] present; disabled inbounds must not be passed to the renderer")
	}
}

func TestBuildCoreConfigForNode_NilParams(t *testing.T) {
	// A defensive path: an inbound with nil Params
	// must not nil-deref the builder and must not
	// leak a nil entry into Experimental. The
	// renderer is responsible for the
	// missing-uuid / missing-password error.
	src := &fakeInboundsSource{
		inbounds: []*inbounds.Inbound{
			{
				ID:         uuid.New(),
				NodeID:     uuid.New(),
				Name:       "broken-in",
				Protocol:   inbounds.ProtocolVLESS,
				Listen:     "::",
				ListenPort: 443,
				Enabled:    true,
				Params:     nil,
			},
		},
	}
	got, err := BuildCoreConfigForNode(context.Background(), src, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Inbounds) != 1 {
		t.Fatalf("Inbounds len = %d, want 1", len(got.Inbounds))
	}
	params, ok := got.Experimental["inbound_params"].(map[string]any)
	if !ok {
		t.Fatalf("params type = %T", got.Experimental["inbound_params"])
	}
	raw, ok := params["broken-in"].(map[string]any)
	if !ok {
		t.Fatalf("params[broken-in] type = %T, want map[string]any (nil-safe)", params["broken-in"])
	}
	if len(raw) != 0 {
		t.Errorf("params[broken-in] = %v, want empty map", raw)
	}
}

// contains is a small strings.Contains alias to keep
// the test assertions terse and avoid pulling the
// strings package for one call site.
func contains(s, substr string) bool {
	if substr == "" {
		return true
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
