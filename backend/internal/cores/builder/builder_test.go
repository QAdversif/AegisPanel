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

	"github.com/QAdversif/AegisPanel/internal/credentials"
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

// fakeCredentialsSource satisfies
// ListCredentialsByInbound for the tests. The
// `creds` map is keyed by inbound ID; `err` is
// what ListByInbound returns so the per-inbound
// error path can be exercised. An empty entry
// (empty slice) is a valid "no credentials" state
// — the builder skips the per-tag entry, and the
// sing-box renderer falls back to the Phase 1
// single-user path.
//
// The return type is `[]*credentials.Credential`
// to match the credentials.Store / Service API.
type fakeCredentialsSource struct {
	creds map[uuid.UUID][]*credentials.Credential
	err   error
	calls int
}

func (f *fakeCredentialsSource) ListByInbound(_ context.Context, id uuid.UUID) ([]*credentials.Credential, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.creds[id], nil
}

func TestBuildCoreConfigForNode_NoInbounds(t *testing.T) {
	src := &fakeInboundsSource{}
	creds := &fakeCredentialsSource{}
	got, err := BuildCoreConfigForNode(context.Background(), src, creds, uuid.New())
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
	if _, ok := got.Experimental["inbound_credentials"]; !ok {
		t.Errorf("Experimental[inbound_credentials] missing")
	}
	if src.calls != 1 {
		t.Errorf("ListByNode calls = %d, want 1", src.calls)
	}
}

func TestBuildCoreConfigForNode_SourceError(t *testing.T) {
	src := &fakeInboundsSource{err: errors.New("pg: connection refused")}
	creds := &fakeCredentialsSource{}
	_, err := BuildCoreConfigForNode(context.Background(), src, creds, uuid.New())
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

	got, err := BuildCoreConfigForNode(context.Background(), src, &fakeCredentialsSource{}, nodeID)
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
	got, err := BuildCoreConfigForNode(context.Background(), src, &fakeCredentialsSource{}, uuid.New())
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

// TestBuildCoreConfigForNode_WithCredentials is the
// Phase 2 multi-user headline test: a node with one
// enabled VLESS inbound + two credentials per inbound
// (two users) populates the inbound_credentials
// Experimental key with the per-tag typed slice, in
// the `map[string]any` shape the sing-box renderer
// expects per the PR 2 contract.
func TestBuildCoreConfigForNode_WithCredentials(t *testing.T) {
	nodeID := uuid.New()
	vlessInbound := &inbounds.Inbound{
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
		},
	}
	u1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	u2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	src := &fakeInboundsSource{inbounds: []*inbounds.Inbound{vlessInbound}}
	creds := &fakeCredentialsSource{
		creds: map[uuid.UUID][]*credentials.Credential{
			vlessInbound.ID: {
				{ID: uuid.New(), UserID: u1, InboundID: vlessInbound.ID, CredentialValue: u1.String()},
				{ID: uuid.New(), UserID: u2, InboundID: vlessInbound.ID, CredentialValue: u2.String()},
			},
		},
	}

	got, err := BuildCoreConfigForNode(context.Background(), src, creds, nodeID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, ok := got.Experimental["inbound_credentials"]
	if !ok {
		t.Fatal("Experimental[inbound_credentials] missing")
	}
	credsByTag, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("Experimental[inbound_credentials] type = %T, want map[string]any", raw)
	}
	entry, ok := credsByTag["vless-in"]
	if !ok {
		t.Fatal("credsByTag[vless-in] missing; Phase 2 path did not populate the per-tag entry")
	}
	slice, ok := entry.([]credentials.Credential)
	if !ok {
		t.Fatalf("credsByTag[vless-in] type = %T, want []credentials.Credential", entry)
	}
	if len(slice) != 2 {
		t.Errorf("credsByTag[vless-in] len = %d, want 2", len(slice))
	}
	if creds.calls != 1 {
		t.Errorf("ListByInbound calls = %d, want 1 (one per enabled inbound)", creds.calls)
	}
}

// TestBuildCoreConfigForNode_NilCredentialsSource is a
// defensive path: the builder does not nil-deref
// when the credentials source is nil. An inbound
// without a credential source emits no entry in
// credsByTag (the sing-box renderer's Phase 1
// fallback path takes over).
func TestBuildCoreConfigForNode_NilCredentialsSource(t *testing.T) {
	src := &fakeInboundsSource{
		inbounds: []*inbounds.Inbound{
			{
				ID:         uuid.New(),
				NodeID:     uuid.New(),
				Name:       "vless-in",
				Protocol:   inbounds.ProtocolVLESS,
				Listen:     "::",
				ListenPort: 443,
				Enabled:    true,
				Params:     map[string]any{"port": 443, "uuid": "u"},
			},
		},
	}
	got, err := BuildCoreConfigForNode(context.Background(), src, nil, uuid.New())
	if err != nil {
		t.Fatalf("nil credSrc must not fail the build: %v", err)
	}
	credsByTag, ok := got.Experimental["inbound_credentials"].(map[string]any)
	if !ok {
		t.Fatalf("credsByTag type = %T", got.Experimental["inbound_credentials"])
	}
	if _, has := credsByTag["vless-in"]; has {
		t.Errorf("credsByTag[vless-in] present; nil credSrc must skip the entry")
	}
}

// TestBuildCoreConfigForNode_CredentialsError is a
// defensive path: a per-inbound query failure is
// logged and treated as "no credentials for this
// inbound" — the sing-box renderer's Phase 1
// fallback path takes over. A fatal error here
// would prevent any node from rendering during a
// transient pg blip.
func TestBuildCoreConfigForNode_CredentialsError(t *testing.T) {
	vlessInbound := &inbounds.Inbound{
		ID:         uuid.New(),
		NodeID:     uuid.New(),
		Name:       "vless-in",
		Protocol:   inbounds.ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
		Enabled:    true,
		Params:     map[string]any{"port": 443, "uuid": "u"},
	}
	src := &fakeInboundsSource{inbounds: []*inbounds.Inbound{vlessInbound}}
	creds := &fakeCredentialsSource{err: errors.New("pg: transient blip")}

	got, err := BuildCoreConfigForNode(context.Background(), src, creds, uuid.New())
	if err != nil {
		t.Fatalf("per-inbound credSrc error must not fail the build: %v", err)
	}
	credsByTag, ok := got.Experimental["inbound_credentials"].(map[string]any)
	if !ok {
		t.Fatalf("credsByTag type = %T", got.Experimental["inbound_credentials"])
	}
	if _, has := credsByTag["vless-in"]; has {
		t.Errorf("credsByTag[vless-in] present despite per-inbound error; the entry must be skipped")
	}
}

// TestBuildCoreConfigForNode_EmptyCredentialsIsFallback
// pins the Phase 1 fallback: an inbound with no
// credentials (empty slice from ListByInbound)
// emits no entry in credsByTag, the sing-box
// renderer falls back to params-based single-user.
func TestBuildCoreConfigForNode_EmptyCredentialsIsFallback(t *testing.T) {
	vlessInbound := &inbounds.Inbound{
		ID:         uuid.New(),
		NodeID:     uuid.New(),
		Name:       "vless-in",
		Protocol:   inbounds.ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
		Enabled:    true,
		Params:     map[string]any{"port": 443, "uuid": "u"},
	}
	src := &fakeInboundsSource{inbounds: []*inbounds.Inbound{vlessInbound}}
	creds := &fakeCredentialsSource{
		creds: map[uuid.UUID][]*credentials.Credential{
			vlessInbound.ID: {}, // explicitly empty
		},
	}
	got, err := BuildCoreConfigForNode(context.Background(), src, creds, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	credsByTag := got.Experimental["inbound_credentials"].(map[string]any)
	if _, has := credsByTag["vless-in"]; has {
		t.Errorf("credsByTag[vless-in] present; an empty slice must be skipped (Phase 1 fallback)")
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
