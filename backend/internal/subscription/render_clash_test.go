// SPDX-License-Identifier: AGPL-3.0-or-later

package subscription

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// newClashFixture seeds a single VLESS endpoint with
// the full Clash TLS field set (sni / alpn /
// fingerprint / reality). Tests that need a different
// protocol / shape extend the seed.
func newClashFixture(t *testing.T) *singboxFixture {
	t.Helper()
	// Reuse the sing-box fixture helper — the shape
	// is identical (one node, one inbound, one host
	// with one endpoint). The sing-box / Clash
	// renderers take the same ResolvedEndpoint input.
	return newSingboxFixture(t)
}

// TestRenderClash_HappyPath_VLESS — the canonical
// case: one VLESS endpoint with the full set of TLS
// fields. The result must be valid YAML with the
// expected top-level shape and per-field values.
func TestRenderClash_HappyPath_VLESS(t *testing.T) {
	f := newClashFixture(t)
	out, err := f.svc.RenderClash(context.Background(), nil, []ResolvedEndpoint{f.ep})
	if err != nil {
		t.Fatalf("RenderClash: %v", err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v\nbody = %s", err, out)
	}
	if len(doc.Proxies) != 1 {
		t.Fatalf("len(Proxies) = %d, want 1", len(doc.Proxies))
	}
	px := doc.Proxies[0]
	if px["type"] != "vless" {
		t.Errorf("type = %v, want vless", px["type"])
	}
	if px["server"] != "1.2.3.4" {
		t.Errorf("server = %v, want 1.2.3.4", px["server"])
	}
	if p, ok := px["port"].(int); !ok || p != 443 {
		t.Errorf("port = %v (%T), want int 443", px["port"], px["port"])
	}
	if px["uuid"] != "00000000-0000-0000-0000-000000000aaa" {
		t.Errorf("uuid = %v", px["uuid"])
	}
	if px["flow"] != "xtls-rprx-vision" {
		t.Errorf("flow = %v, want xtls-rprx-vision", px["flow"])
	}
	if px["sni"] != "cdn.example.com" {
		t.Errorf("sni = %v, want cdn.example.com", px["sni"])
	}
	if tls, _ := px["tls"].(bool); !tls {
		t.Errorf("tls = %v, want true (sni is set)", px["tls"])
	}
	if px["fingerprint"] != "chrome" {
		t.Errorf("fingerprint = %v, want chrome", px["fingerprint"])
	}
	if px["client-fingerprint"] != "chrome" {
		t.Errorf("client-fingerprint = %v, want chrome", px["client-fingerprint"])
	}
	alpn, ok := px["alpn"].([]any)
	if !ok || len(alpn) != 2 || alpn[0] != "h3" {
		t.Errorf("alpn = %v, want [h3, h2]", px["alpn"])
	}
	// reality-opts is the Clash Meta sub-block.
	opts, ok := px["reality-opts"].(map[string]any)
	if !ok {
		t.Fatalf("reality-opts block missing: %v", px["reality-opts"])
	}
	if opts["public-key"] != "pk-xxx" {
		t.Errorf("reality-opts.public-key = %v, want pk-xxx", opts["public-key"])
	}
	if opts["short-id"] != "01" {
		t.Errorf("reality-opts.short-id = %v, want 01", opts["short-id"])
	}
	// tag is the host.DisplayName.
	if px["name"] != "🇱🇻 Latvia" {
		t.Errorf("name = %v, want 🇱🇻 Latvia", px["name"])
	}
}

// TestRenderClash_Empty — no entitled endpoints
// yields a valid empty document, not an error.
func TestRenderClash_Empty(t *testing.T) {
	f := newClashFixture(t)
	out, err := f.svc.RenderClash(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("RenderClash(nil): %v", err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Proxies) != 0 {
		t.Errorf("len(Proxies) = %d, want 0", len(doc.Proxies))
	}
}

// TestRenderClash_EndpointSNIOverridesParams — when
// the endpoint sets an SNI, it wins over params.sni.
func TestRenderClash_EndpointSNIOverridesParams(t *testing.T) {
	f := newClashFixture(t)
	f.ep.Endpoint.SNI = []string{"override.example.com"}
	out, err := f.svc.RenderClash(context.Background(), nil, []ResolvedEndpoint{f.ep})
	if err != nil {
		t.Fatalf("RenderClash: %v", err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	_ = yaml.Unmarshal(out, &doc)
	px := doc.Proxies[0]
	if px["sni"] != "override.example.com" {
		t.Errorf("sni = %v, want override.example.com", px["sni"])
	}
}

// TestRenderClash_VariousProtocols — feed all four
// supported protocols through RenderClash and assert
// the per-protocol shape.
func TestRenderClash_VariousProtocols(t *testing.T) {
	hostsStore := hosts.NewMemoryStore()
	hostsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	nodesStore := nodes.NewMemoryStore()
	nodesStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	inboundsStore := inbounds.NewMemoryStore()
	inboundsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })

	nodeID := uuid.New()
	if err := nodesStore.Create(context.Background(), &nodes.Node{ID: nodeID, Name: "n1", Region: "eu", State: nodes.StateNew, Address: "1.2.3.4"}); err != nil {
		t.Fatalf("node: %v", err)
	}
	inboundsSeed := []struct {
		name     string
		protocol inbounds.Protocol
		params   map[string]any
	}{
		{"vless", inbounds.ProtocolVLESS, map[string]any{"uuid": "uuid-v"}},
		{"hy2", inbounds.ProtocolHysteria2, map[string]any{"password": "pw-h"}},
		{"ss", inbounds.ProtocolShadowsocks, map[string]any{"method": "aes-256-gcm", "password": "pw-s"}},
		{"trojan", inbounds.ProtocolTrojan, map[string]any{"password": "pw-t"}},
	}
	eps := make([]ResolvedEndpoint, 0, len(inboundsSeed))
	for i, inb := range inboundsSeed {
		id := uuid.New()
		if err := inboundsStore.Create(context.Background(), &inbounds.Inbound{
			ID: id, NodeID: nodeID, Name: inb.name,
			Protocol: inb.protocol, Listen: "::", ListenPort: 1000 + i, Enabled: true,
			Params: inb.params,
		}); err != nil {
			t.Fatalf("inbound %s: %v", inb.name, err)
		}
		hostID := uuid.New()
		epID := uuid.New()
		host := &hosts.Host{
			ID: hostID, Remark: inb.name, Type: hosts.HostTypeDirect, Enabled: true,
			Endpoints: []hosts.Endpoint{{ID: epID, NodeID: nodeID, InboundID: id, Weight: 1}},
		}
		if err := hostsStore.Create(context.Background(), host); err != nil {
			t.Fatalf("host %s: %v", inb.name, err)
		}
		eps = append(eps, ResolvedEndpoint{
			Host:     host,
			Endpoint: host.Endpoints[0],
			Node:     &nodes.Node{ID: nodeID, Name: "n1", Address: "1.2.3.4"},
			Inbound: &inbounds.Inbound{
				ID: id, NodeID: nodeID, Name: inb.name, Protocol: inb.protocol, Listen: "::", ListenPort: 1000 + i, Params: inb.params,
			},
		})
	}
	hostsSvc := hosts.NewService(hostsStore, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))
	svc := NewService(NewMemoryStore(), hostsSvc, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))

	out, err := svc.RenderClash(context.Background(), nil, eps)
	if err != nil {
		t.Fatalf("RenderClash: %v", err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v\nbody = %s", err, out)
	}
	if len(doc.Proxies) != 4 {
		t.Fatalf("len = %d, want 4", len(doc.Proxies))
	}
	// Per-protocol type and one signature field.
	wantType := map[string]string{
		"vless":  "vless",
		"hy2":    "hysteria2",
		"ss":     "ss",
		"trojan": "trojan",
	}
	wantSig := map[string]string{
		"vless":  "uuid-v",
		"hy2":    "pw-h",
		"ss":     "aes-256-gcm",
		"trojan": "pw-t",
	}
	for _, px := range doc.Proxies {
		name, _ := px["name"].(string)
		if px["type"] != wantType[name] {
			t.Errorf("%s: type = %v, want %s", name, px["type"], wantType[name])
		}
		switch name {
		case "vless":
			if px["uuid"] != wantSig[name] {
				t.Errorf("vless uuid = %v, want %s", px["uuid"], wantSig[name])
			}
		case "hy2":
			if px["password"] != wantSig[name] {
				t.Errorf("hy2 password = %v, want %s", px["password"], wantSig[name])
			}
		case "ss":
			if px["cipher"] != wantSig[name] {
				t.Errorf("ss cipher = %v, want %s", px["cipher"], wantSig[name])
			}
			if px["password"] != "pw-s" {
				t.Errorf("ss password = %v, want pw-s", px["password"])
			}
		case "trojan":
			if px["password"] != wantSig[name] {
				t.Errorf("trojan password = %v, want %s", px["password"], wantSig[name])
			}
			if tval, _ := px["tls"].(bool); !tval {
				t.Errorf("trojan tls = %v, want true (always-on for trojan)", px["tls"])
			}
		}
	}
}

// TestRenderClash_TLSKeyOnForVLESS — the `tls: true`
// flag is set whenever the endpoint / params carry
// an SNI. This is what triggers the client to dial
// TLS; without it, the connection is plaintext.
func TestRenderClash_TLSKeyOnForVLESS(t *testing.T) {
	f := newClashFixture(t)
	out, err := f.svc.RenderClash(context.Background(), nil, []ResolvedEndpoint{f.ep})
	if err != nil {
		t.Fatalf("RenderClash: %v", err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	_ = yaml.Unmarshal(out, &doc)
	if tval, _ := doc.Proxies[0]["tls"].(bool); !tval {
		t.Errorf("VLESS with sni: tls = %v, want true", doc.Proxies[0]["tls"])
	}
}

// TestRenderClash_NoTLSKeyForShadowsocks — Shadowsocks
// has no TLS layer. The Clash SS schema does not
// carry a `tls:` field. The renderer's omission is
// the safety property.
func TestRenderClash_NoTLSKeyForShadowsocks(t *testing.T) {
	hostsStore := hosts.NewMemoryStore()
	hostsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	nodesStore := nodes.NewMemoryStore()
	nodesStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	inboundsStore := inbounds.NewMemoryStore()
	inboundsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	nodeID := uuid.New()
	_ = nodesStore.Create(context.Background(), &nodes.Node{ID: nodeID, Name: "n", Region: "eu", State: nodes.StateNew, Address: "1.2.3.4"})
	ss := &inbounds.Inbound{
		ID: uuid.New(), NodeID: nodeID, Name: "ss",
		Protocol: inbounds.ProtocolShadowsocks, Listen: "::", ListenPort: 8388, Enabled: true,
		Params: map[string]any{"method": "aes-256-gcm", "password": "pw"},
	}
	_ = inboundsStore.Create(context.Background(), ss)
	hostID := uuid.New()
	epID := uuid.New()
	_ = hostsStore.Create(context.Background(), &hosts.Host{
		ID: hostID, Remark: "ss", Type: hosts.HostTypeDirect, Enabled: true,
		Endpoints: []hosts.Endpoint{{ID: epID, NodeID: nodeID, InboundID: ss.ID, Weight: 1}},
	})
	hostsSvc := hosts.NewService(hostsStore, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))
	svc := NewService(NewMemoryStore(), hostsSvc, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))
	ep := ResolvedEndpoint{
		Host:     &hosts.Host{ID: hostID, Remark: "ss", Type: hosts.HostTypeDirect, Enabled: true},
		Endpoint: hosts.Endpoint{ID: epID, NodeID: nodeID, InboundID: ss.ID, Weight: 1},
		Node:     &nodes.Node{ID: nodeID, Name: "n", Address: "1.2.3.4"},
		Inbound:  ss,
	}
	out, err := svc.RenderClash(context.Background(), nil, []ResolvedEndpoint{ep})
	if err != nil {
		t.Fatalf("RenderClash: %v", err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	_ = yaml.Unmarshal(out, &doc)
	if _, present := doc.Proxies[0]["tls"]; present {
		t.Errorf("shadowsocks proxy must not carry a tls field; got %v", doc.Proxies[0]["tls"])
	}
}
