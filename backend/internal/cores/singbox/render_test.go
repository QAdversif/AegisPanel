// SPDX-License-Identifier: AGPL-3.0-or-later

package singbox

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/cores"
	"github.com/QAdversif/AegisPanel/internal/credentials"
)

// TestRenderConfig_AllProtocols is the headline test: it
// renders a CoreConfig with one inbound per supported
// protocol, then unmarshals the output and asserts the
// per-protocol structure matches what sing-box 1.8 expects.
// A failure here means a sing-box upgrade has changed the
// field names; the test will guide the fix.
func TestRenderConfig_AllProtocols(t *testing.T) {
	p := New()
	cfg := cores.CoreConfig{
		Inbounds: []cores.InboundSpec{
			{Tag: "vless-in", Type: "vless", HostID: "h-lv"},
			{Tag: "hy2-in", Type: "hysteria2", HostID: "h-lv"},
			{Tag: "ss-in", Type: "shadowsocks", HostID: "h-lv"},
			{Tag: "trojan-in", Type: "trojan", HostID: "h-lv"},
		},
		Experimental: map[string]any{
			ExperimentalInboundParamsKey: map[string]any{
				"vless-in": map[string]any{
					"port": 443,
					"uuid": "00000000-0000-0000-0000-000000000001",
					"flow": "xtls-rprx-vision",
					"tls": map[string]any{
						"server_name": "cdn.example.com",
						"reality": map[string]any{
							"private_key": "PRIVKEY",
							"short_ids":   []string{"01ab"},
						},
					},
				},
				"hy2-in": map[string]any{
					"port":     443,
					"password": "hy2-pass",
					"tls": map[string]any{
						"server_name": "cdn.example.com",
					},
				},
				"ss-in": map[string]any{
					"port":     8388,
					"method":   "2022-blake3-aes-128-gcm",
					"password": "ss-pass",
				},
				"trojan-in": map[string]any{
					"port":     443,
					"password": "trojan-pass",
					"tls": map[string]any{
						"server_name": "cdn.example.com",
					},
				},
			},
		},
	}

	out, err := p.RenderConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// Round-trip through the panel's own validator.
	if err := p.ValidateConfig(context.Background(), []byte(out)); err != nil {
		t.Fatalf("validate round-trip: %v", err)
	}

	// Now inspect the actual JSON structure: the test
	// fails with a useful diff if a field name has
	// drifted (vs. a "the whole file is wrong" panic).
	var doc struct {
		Inbounds  []map[string]any `json:"inbounds"`
		Outbounds []map[string]any `json:"outbounds"`
		Route     map[string]any   `json:"route"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal: %v\n--- output ---\n%s", err, out)
	}
	if len(doc.Inbounds) != 4 {
		t.Fatalf("expected 4 inbounds, got %d", len(doc.Inbounds))
	}

	// Default outbounds + minimal route.
	if len(doc.Outbounds) != 2 {
		t.Errorf("expected 2 default outbounds, got %d", len(doc.Outbounds))
	}
	if doc.Route["final"] != "direct" {
		t.Errorf("route.final = %v, want direct", doc.Route["final"])
	}

	// Per-inbound spot checks.
	byTag := map[string]map[string]any{}
	for _, in := range doc.Inbounds {
		byTag[in["tag"].(string)] = in
	}

	if byTag["vless-in"]["type"] != "vless" {
		t.Errorf("vless-in.type = %v", byTag["vless-in"]["type"])
	}
	if users, ok := byTag["vless-in"]["users"].([]any); !ok || len(users) != 1 {
		t.Errorf("vless-in.users = %v", byTag["vless-in"]["users"])
	} else {
		u := users[0].(map[string]any)
		if u["flow"] != "xtls-rprx-vision" {
			t.Errorf("vless user.flow = %v, want xtls-rprx-vision", u["flow"])
		}
	}
	if _, ok := byTag["vless-in"]["tls"].(map[string]any); !ok {
		t.Errorf("vless-in.tls missing")
	}

	if byTag["hy2-in"]["type"] != "hysteria2" {
		t.Errorf("hy2-in.type = %v", byTag["hy2-in"]["type"])
	}
	if byTag["ss-in"]["method"] != "2022-blake3-aes-128-gcm" {
		t.Errorf("ss-in.method = %v", byTag["ss-in"]["method"])
	}
	if byTag["trojan-in"]["type"] != "trojan" {
		t.Errorf("trojan-in.type = %v", byTag["trojan-in"]["type"])
	}
}

func TestRenderConfig_MissingParams(t *testing.T) {
	p := New()
	cfg := cores.CoreConfig{
		Inbounds: []cores.InboundSpec{{Tag: "vless-in", Type: "vless"}},
		Experimental: map[string]any{
			ExperimentalInboundParamsKey: map[string]any{
				// Empty: no parameters for vless-in.
			},
		},
	}
	_, err := p.RenderConfig(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for missing inbound parameters")
	}
	if !strings.Contains(err.Error(), "vless-in") {
		t.Fatalf("error should mention missing tag, got %q", err.Error())
	}
}

func TestRenderConfig_UnknownType(t *testing.T) {
	p := New()
	cfg := cores.CoreConfig{
		Inbounds: []cores.InboundSpec{{Tag: "x", Type: "wireguard"}},
		Experimental: map[string]any{
			ExperimentalInboundParamsKey: map[string]any{
				"x": map[string]any{"port": 443},
			},
		},
	}
	_, err := p.RenderConfig(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for unsupported inbound type")
	}
	if !strings.Contains(err.Error(), "wireguard") {
		t.Fatalf("error should mention the unsupported type, got %q", err.Error())
	}
}

func TestRenderConfig_TrojanRequiresTLS(t *testing.T) {
	p := New()
	cfg := cores.CoreConfig{
		Inbounds: []cores.InboundSpec{{Tag: "t-in", Type: "trojan"}},
		Experimental: map[string]any{
			ExperimentalInboundParamsKey: map[string]any{
				"t-in": map[string]any{
					"port":     443,
					"password": "p",
					// no tls block
				},
			},
		},
	}
	_, err := p.RenderConfig(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for trojan without TLS")
	}
	if !strings.Contains(err.Error(), "tls") {
		t.Fatalf("error should mention tls, got %q", err.Error())
	}
}

func TestRenderConfig_ExperimentalKeyMissing(t *testing.T) {
	p := New()
	_, err := p.RenderConfig(context.Background(), cores.CoreConfig{
		Inbounds: []cores.InboundSpec{{Tag: "vless-in", Type: "vless"}},
		// no Experimental at all
	})
	if err == nil {
		t.Fatal("expected error for missing Experimental block")
	}
}

func TestRenderConfig_OutputEndsWithNewline(t *testing.T) {
	p := New()
	out, err := p.RenderConfig(context.Background(), cores.CoreConfig{
		Inbounds: []cores.InboundSpec{{Tag: "ss-in", Type: "shadowsocks"}},
		Experimental: map[string]any{
			ExperimentalInboundParamsKey: map[string]any{
				"ss-in": map[string]any{
					"port":     8388,
					"method":   "2022-blake3-aes-128-gcm",
					"password": "p",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatal("rendered config should end with a newline")
	}
}

func TestValidateConfig_RejectsEmpty(t *testing.T) {
	p := New()
	if err := p.ValidateConfig(context.Background(), nil); err == nil {
		t.Fatal("expected error for empty config")
	}
}

func TestValidateConfig_RejectsNonJSON(t *testing.T) {
	p := New()
	if err := p.ValidateConfig(context.Background(), []byte("not json")); err == nil {
		t.Fatal("expected error for non-JSON config")
	}
}

func TestValidateConfig_RejectsMissingInbounds(t *testing.T) {
	p := New()
	if err := p.ValidateConfig(context.Background(), []byte(`{"outbounds":[]}`)); err == nil {
		t.Fatal("expected error for config without inbounds")
	}
}

func TestValidateConfig_RejectsNonArrayInbounds(t *testing.T) {
	p := New()
	if err := p.ValidateConfig(context.Background(), []byte(`{"inbounds":{}}`)); err == nil {
		t.Fatal("expected error for non-array inbounds")
	}
}

func TestValidateConfig_AcceptsMinimal(t *testing.T) {
	p := New()
	if err := p.ValidateConfig(context.Background(), []byte(`{"inbounds":[]}`)); err != nil {
		t.Fatalf("minimal config should validate, got %v", err)
	}
}

// --- Phase 2 multi-user rendering tests ---
//
// These tests cover the ExperimentalInboundCredentialsKey
// path. PR 2 (this commit) wires the renderer signature;
// PR 3 (the next slice) wires the Builder to populate
// this key from the user_inbound_credentials table.
// Until PR 3 lands, no real CoreConfig has the
// inbound_credentials key set, so these tests are the
// forward-looking test surface for the multi-user path.

// multiUserCfg is a small fixture builder: one inbound
// per multi-user-capable protocol (VLESS, HY2, Trojan;
// Shadowsocks is single-password and stays on the
// existing TestRenderConfig_AllProtocols path), with
// the inbound_credentials Experimental key populated
// for each tag.
//
// Note: the credentials map at the top level is
// `map[string]any` (not `map[string][]credentials.Credential`).
// This matches the existing inbound_params pattern —
// values held in a `map[string]any` retain their
// concrete type (here `[]credentials.Credential`), but
// the top-level map itself is type-asserted as
// `map[string]any` by the renderer via
// `raw.(map[string]any)`. A typed map of slice values
// would fail that assertion because Go reflection
// treats `map[string]any` and `map[string][]T` as
// distinct types even when the slice element type
// matches. The Builder in PR 3 of the Phase 2 plan
// will use the same `map[string]any` shape.
func multiUserCfg(t *testing.T) (cores.CoreConfig, []uuid.UUID) {
	t.Helper()
	u1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	u2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	u3 := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	cfg := cores.CoreConfig{
		Inbounds: []cores.InboundSpec{
			{Tag: "vless-in", Type: "vless", HostID: "h-lv"},
			{Tag: "hy2-in", Type: "hysteria2", HostID: "h-lv"},
			{Tag: "trojan-in", Type: "trojan", HostID: "h-lv"},
		},
		Experimental: map[string]any{
			ExperimentalInboundParamsKey: map[string]any{
				"vless-in": map[string]any{
					"port": 443,
					"uuid": "00000000-0000-0000-0000-000000000001",
					"flow": "xtls-rprx-vision",
					"tls": map[string]any{
						"server_name": "cdn.example.com",
						"reality": map[string]any{
							"private_key": "PRIVKEY",
							"short_ids":   []string{"01ab"},
						},
					},
				},
				"hy2-in": map[string]any{
					"port":     443,
					"password": "hy2-pass",
					"tls":      map[string]any{"server_name": "cdn.example.com"},
				},
				"trojan-in": map[string]any{
					"port":     443,
					"password": "trojan-pass",
					"tls":      map[string]any{"server_name": "cdn.example.com"},
				},
			},
			ExperimentalInboundCredentialsKey: map[string]any{
				"vless-in": []credentials.Credential{
					{ID: uuid.New(), UserID: u1, InboundID: uuid.Nil, CredentialValue: u1.String()},
					{ID: uuid.New(), UserID: u2, InboundID: uuid.Nil, CredentialValue: u2.String()},
				},
				"hy2-in": []credentials.Credential{
					{ID: uuid.New(), UserID: u1, InboundID: uuid.Nil, CredentialValue: "hy2-pw-for-u1"},
					{ID: uuid.New(), UserID: u2, InboundID: uuid.Nil, CredentialValue: "hy2-pw-for-u2"},
					{ID: uuid.New(), UserID: u3, InboundID: uuid.Nil, CredentialValue: "hy2-pw-for-u3"},
				},
				"trojan-in": []credentials.Credential{
					{ID: uuid.New(), UserID: u1, InboundID: uuid.Nil, CredentialValue: "trojan-pw-for-u1"},
				},
			},
		},
	}
	return cfg, []uuid.UUID{u1, u2, u3}
}

func TestRenderConfig_VLESSMultiUser_Phase2(t *testing.T) {
	p := New()
	cfg, userIDs := multiUserCfg(t)

	out, err := p.RenderConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := p.ValidateConfig(context.Background(), []byte(out)); err != nil {
		t.Fatalf("validate round-trip: %v", err)
	}

	var doc struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal: %v\n--- output ---\n%s", err, out)
	}
	if len(doc.Inbounds) != 3 {
		t.Fatalf("expected 3 inbounds, got %d", len(doc.Inbounds))
	}

	var vless map[string]any
	for _, in := range doc.Inbounds {
		if in["tag"] == "vless-in" {
			vless = in
			break
		}
	}
	if vless == nil {
		t.Fatal("vless-in not found in rendered output")
	}
	users, ok := vless["users"].([]any)
	if !ok {
		t.Fatalf("vless-in.users type = %T, want []any", vless["users"])
	}
	if len(users) != 2 {
		t.Fatalf("vless-in.users len = %d, want 2 (Phase 2 multi-user)", len(users))
	}

	// Per-user assertions: each user entry has name =
	// UserID.String() and uuid = CredentialValue. Flow
	// propagates to every user.
	wantUUIDs := map[string]bool{
		userIDs[0].String(): false,
		userIDs[1].String(): false,
	}
	for i, raw := range users {
		u, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("users[%d] type = %T", i, raw)
		}
		if u["name"] != userIDs[i].String() {
			t.Errorf("users[%d].name = %v, want %s", i, u["name"], userIDs[i].String())
		}
		uuidStr, ok := u["uuid"].(string)
		if !ok {
			t.Fatalf("users[%d].uuid type = %T", i, u["uuid"])
		}
		if uuidStr != userIDs[i].String() {
			t.Errorf("users[%d].uuid = %v, want %s (the credential_value)", i, uuidStr, userIDs[i].String())
		}
		if u["flow"] != "xtls-rprx-vision" {
			t.Errorf("users[%d].flow = %v, want xtls-rprx-vision (per-inbound flow propagates to every user)", i, u["flow"])
		}
		wantUUIDs[uuidStr] = true
	}
	for u, seen := range wantUUIDs {
		if !seen {
			t.Errorf("expected uuid %s in vless-in users list, did not find it", u)
		}
	}
}

func TestRenderConfig_HY2MultiUser_Phase2(t *testing.T) {
	p := New()
	cfg, _ := multiUserCfg(t)

	out, err := p.RenderConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := p.ValidateConfig(context.Background(), []byte(out)); err != nil {
		t.Fatalf("validate round-trip: %v", err)
	}

	var doc struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal: %v\n--- output ---\n%s", err, out)
	}
	var hy2 map[string]any
	for _, in := range doc.Inbounds {
		if in["tag"] == "hy2-in" {
			hy2 = in
			break
		}
	}
	if hy2 == nil {
		t.Fatal("hy2-in not found")
	}
	users, ok := hy2["users"].([]any)
	if !ok {
		t.Fatalf("hy2-in.users type = %T", hy2["users"])
	}
	if len(users) != 3 {
		t.Fatalf("hy2-in.users len = %d, want 3", len(users))
	}
	wantPasswords := map[string]bool{
		"hy2-pw-for-u1": false,
		"hy2-pw-for-u2": false,
		"hy2-pw-for-u3": false,
	}
	for i, raw := range users {
		u := raw.(map[string]any)
		pw, ok := u["password"].(string)
		if !ok {
			t.Fatalf("users[%d].password type = %T", i, u["password"])
		}
		if pw == "hy2-pass" {
			t.Errorf("users[%d].password = %q, want a per-user credential (Phase 2 path), not the Phase 1 fallback value", i, pw)
		}
		wantPasswords[pw] = true
	}
	for pw, seen := range wantPasswords {
		if !seen {
			t.Errorf("expected hy2 password %q, did not find it", pw)
		}
	}
}

func TestRenderConfig_TrojanMultiUser_Phase2(t *testing.T) {
	p := New()
	cfg, _ := multiUserCfg(t)

	out, err := p.RenderConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := p.ValidateConfig(context.Background(), []byte(out)); err != nil {
		t.Fatalf("validate round-trip: %v", err)
	}

	var doc struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var trojan map[string]any
	for _, in := range doc.Inbounds {
		if in["tag"] == "trojan-in" {
			trojan = in
			break
		}
	}
	if trojan == nil {
		t.Fatal("trojan-in not found")
	}
	users, ok := trojan["users"].([]any)
	if !ok {
		t.Fatalf("trojan-in.users type = %T", trojan["users"])
	}
	if len(users) != 1 {
		t.Fatalf("trojan-in.users len = %d, want 1", len(users))
	}
	u := users[0].(map[string]any)
	if u["password"] != "trojan-pw-for-u1" {
		t.Errorf("trojan-in.users[0].password = %v, want trojan-pw-for-u1", u["password"])
	}
}

func TestRenderConfig_MixedPhase1AndPhase2(t *testing.T) {
	// Defensive: a Builder that populates the credentials
	// key for SOME inbound tags but not others must not
	// crash and must produce a sensible output. Inbound
	// tags that lack a credentials entry fall back to the
	// Phase 1 params-driven single-user path.
	p := New()
	cfg := cores.CoreConfig{
		Inbounds: []cores.InboundSpec{
			{Tag: "vless-multi", Type: "vless"},
			{Tag: "vless-single", Type: "vless"},
		},
		Experimental: map[string]any{
			ExperimentalInboundParamsKey: map[string]any{
				"vless-multi": map[string]any{
					"port": 443,
					"uuid": "00000000-0000-0000-0000-000000000001",
				},
				"vless-single": map[string]any{
					"port": 8443,
					"uuid": "00000000-0000-0000-0000-000000000002",
				},
			},
			ExperimentalInboundCredentialsKey: map[string]any{
				"vless-multi": []credentials.Credential{
					{ID: uuid.New(), UserID: uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"), InboundID: uuid.Nil, CredentialValue: "aaaaaa-cred-a"},
					{ID: uuid.New(), UserID: uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"), InboundID: uuid.Nil, CredentialValue: "bbbbbb-cred-b"},
				},
				// vless-single intentionally absent — the
				// renderer must fall back to the
				// params["uuid"] value.
			},
		},
	}
	out, err := p.RenderConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var doc struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal: %v\n--- output ---\n%s", err, out)
	}
	byTag := map[string]map[string]any{}
	for _, in := range doc.Inbounds {
		byTag[in["tag"].(string)] = in
	}
	multi, ok := byTag["vless-multi"]["users"].([]any)
	if !ok || len(multi) != 2 {
		t.Errorf("vless-multi.users = %v, want length 2 (Phase 2 path)", byTag["vless-multi"]["users"])
	}
	single, ok := byTag["vless-single"]["users"].([]any)
	if !ok || len(single) != 1 {
		t.Fatalf("vless-single.users = %v, want length 1 (Phase 1 fallback path)", byTag["vless-single"]["users"])
	}
	if u := single[0].(map[string]any); u["uuid"] != "00000000-0000-0000-0000-000000000002" {
		t.Errorf("vless-single.users[0].uuid = %v, want the Phase 1 params value (no credentials for this tag)", u["uuid"])
	}
}

func TestRenderConfig_CredentialsWrongType_FallsBackToParams(t *testing.T) {
	// Defensive: a Builder bug that puts a non-slice /
	// wrong-typed value at the per-tag level (e.g. a
	// single Credential struct instead of a slice)
	// must NOT crash the renderer and must NOT silently
	// produce an empty config. The renderer falls back
	// to the Phase 1 params-driven path for that tag
	// and skips the bad entry.
	p := New()
	cfg := cores.CoreConfig{
		Inbounds: []cores.InboundSpec{{Tag: "vless-in", Type: "vless"}},
		Experimental: map[string]any{
			ExperimentalInboundParamsKey: map[string]any{
				"vless-in": map[string]any{
					"port": 443,
					"uuid": "00000000-0000-0000-0000-000000000001",
				},
			},
			ExperimentalInboundCredentialsKey: map[string]any{
				// Wrong type: a single struct, not a slice.
				// extractCredentialsByTag should skip this.
				"vless-in": credentials.Credential{ID: uuid.New(), UserID: uuid.New(), InboundID: uuid.Nil, CredentialValue: "ignored"},
			},
		},
	}
	out, err := p.RenderConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("render should not fail on bad credentials shape: %v", err)
	}
	var doc struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	vless := doc.Inbounds[0]
	users := vless["users"].([]any)
	if len(users) != 1 {
		t.Errorf("vless-in.users len = %d, want 1 (Phase 1 fallback after wrong-type skip)", len(users))
	}
	if u := users[0].(map[string]any); u["uuid"] != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("vless-in.users[0].uuid = %v, want the Phase 1 params value", u["uuid"])
	}
}
