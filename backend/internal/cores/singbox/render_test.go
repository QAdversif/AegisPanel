// SPDX-License-Identifier: AGPL-3.0-or-later

package singbox

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/QAdversif/AegisPanel/internal/cores"
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
