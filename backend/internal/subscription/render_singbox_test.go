// SPDX-License-Identifier: AGPL-3.0-or-later

package subscription

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// singboxFixture is the minimum data needed to render
// a sing-box subscription. It seeds a single VLESS
// inbound (the most common case) on a single node,
// with a single host pointing at it. Tests that need
// multiple endpoints / protocols extend the seed.
type singboxFixture struct {
	svc *Service
	ep  ResolvedEndpoint
}

func newSingboxFixture(t *testing.T) *singboxFixture {
	t.Helper()
	hostsStore := hosts.NewMemoryStore()
	hostsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	nodesStore := nodes.NewMemoryStore()
	nodesStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	inboundsStore := inbounds.NewMemoryStore()
	inboundsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })

	nodeID := uuid.New()
	inboundID := uuid.New()
	hostID := uuid.New()
	endpointID := uuid.New()

	node := &nodes.Node{ID: nodeID, Name: "lv-01", Region: "eu", State: nodes.StateNew, Address: "1.2.3.4"}
	if err := nodesStore.Create(context.Background(), node); err != nil {
		t.Fatalf("nodes.Create: %v", err)
	}
	vless := &inbounds.Inbound{
		ID: inboundID, NodeID: nodeID, Name: "vless-reality",
		Protocol: inbounds.ProtocolVLESS, Listen: "::", ListenPort: 443, Enabled: true,
		Tags: []string{"production"},
		Params: map[string]any{
			"uuid":        "00000000-0000-0000-0000-000000000aaa",
			"flow":        "xtls-rprx-vision",
			"sni":         "cdn.example.com",
			"fingerprint": "chrome",
			"alpn":        []any{"h3", "h2"},
			"reality":     map[string]any{"public_key": "pk-xxx", "short_id": "01"},
		},
	}
	if err := inboundsStore.Create(context.Background(), vless); err != nil {
		t.Fatalf("inbounds.Create: %v", err)
	}
	host := &hosts.Host{
		ID: hostID, Remark: "Latvia", DisplayName: "🇱🇻 Latvia",
		Type: hosts.HostTypeDirect, Enabled: true, Priority: 10,
		Endpoints: []hosts.Endpoint{{
			ID: endpointID, NodeID: nodeID, InboundID: inboundID, Weight: 1,
		}},
	}
	if err := hostsStore.Create(context.Background(), host); err != nil {
		t.Fatalf("hosts.Create: %v", err)
	}

	hostsSvc := hosts.NewService(hostsStore, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))
	nodesSvc := nodes.NewService(nodesStore)
	inboundsSvc := inbounds.NewService(inboundsStore, nodesSvc)
	svc := NewService(NewMemoryStore(), hostsSvc, nodesSvc, inboundsSvc)
	ep := ResolvedEndpoint{
		Host:     host,
		Endpoint: host.Endpoints[0],
		Node:     node,
		Inbound:  vless,
	}
	return &singboxFixture{svc: svc, ep: ep}
}

// TestRenderSingbox_HappyPath_VLESS — the canonical
// case: one VLESS endpoint with the full set of TLS
// fields. The result must be valid JSON with the
// expected top-level shape and per-field values.
func TestRenderSingbox_HappyPath_VLESS(t *testing.T) {
	f := newSingboxFixture(t)
	out, err := f.svc.RenderSingbox(context.Background(), nil, []ResolvedEndpoint{f.ep})
	if err != nil {
		t.Fatalf("RenderSingbox: %v", err)
	}
	// Must be valid JSON; must have "outbounds" array.
	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v\nbody = %s", err, out)
	}
	if len(doc.Outbounds) != 1 {
		t.Fatalf("len(Outbounds) = %d, want 1", len(doc.Outbounds))
	}
	ob := doc.Outbounds[0]
	if ob["type"] != "vless" {
		t.Errorf("type = %v, want vless", ob["type"])
	}
	if ob["server"] != "1.2.3.4" {
		t.Errorf("server = %v, want 1.2.3.4", ob["server"])
	}
	// JSON numbers come back as float64; compare as such.
	if p, ok := ob["server_port"].(float64); !ok || int(p) != 443 {
		t.Errorf("server_port = %v, want 443", ob["server_port"])
	}
	if ob["uuid"] != "00000000-0000-0000-0000-000000000aaa" {
		t.Errorf("uuid = %v", ob["uuid"])
	}
	if ob["flow"] != "xtls-rprx-vision" {
		t.Errorf("flow = %v, want xtls-rprx-vision", ob["flow"])
	}
	// tag is the host.DisplayName, not the host.Remark.
	if ob["tag"] != "🇱🇻 Latvia" {
		t.Errorf("tag = %v, want 🇱🇻 Latvia", ob["tag"])
	}
	// TLS block: server_name, alpn, utls, reality.
	tls, ok := ob["tls"].(map[string]any)
	if !ok {
		t.Fatalf("tls block missing or wrong type: %v", ob["tls"])
	}
	if tls["server_name"] != "cdn.example.com" {
		t.Errorf("tls.server_name = %v", tls["server_name"])
	}
	alpn, ok := tls["alpn"].([]any)
	if !ok || len(alpn) != 2 || alpn[0] != "h3" {
		t.Errorf("tls.alpn = %v, want [h3, h2]", tls["alpn"])
	}
	utls, ok := tls["utls"].(map[string]any)
	if !ok || utls["fingerprint"] != "chrome" {
		t.Errorf("tls.utls = %v, want fingerprint=chrome", tls["utls"])
	}
	real, ok := tls["reality"].(map[string]any)
	if !ok || real["public_key"] != "pk-xxx" || real["short_id"] != "01" {
		t.Errorf("tls.reality = %v, want public_key=pk-xxx short_id=01", tls["reality"])
	}
}

// TestRenderSingbox_Empty — no entitled endpoints
// yields a valid empty document, not an error.
func TestRenderSingbox_Empty(t *testing.T) {
	f := newSingboxFixture(t)
	out, err := f.svc.RenderSingbox(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("RenderSingbox(nil): %v", err)
	}
	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Outbounds) != 0 {
		t.Errorf("len(Outbounds) = %d, want 0", len(doc.Outbounds))
	}
}

// TestRenderSingbox_EndpointSNIOverridesParams — when
// the endpoint sets an SNI, it wins over params.sni.
func TestRenderSingbox_EndpointSNIOverridesParams(t *testing.T) {
	f := newSingboxFixture(t)
	f.ep.Endpoint.SNI = []string{"override.example.com"}
	out, err := f.svc.RenderSingbox(context.Background(), nil, []ResolvedEndpoint{f.ep})
	if err != nil {
		t.Fatalf("RenderSingbox: %v", err)
	}
	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	_ = json.Unmarshal(out, &doc)
	tls := doc.Outbounds[0]["tls"].(map[string]any)
	if tls["server_name"] != "override.example.com" {
		t.Errorf("server_name = %v, want override.example.com", tls["server_name"])
	}
}

// TestRenderSingbox_VariousProtocols — feed all four
// supported protocols through RenderSingbox and
// assert the per-protocol shape. This is the
// "all-shape" smoke test.
func TestRenderSingbox_VariousProtocols(t *testing.T) {
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

	out, err := svc.RenderSingbox(context.Background(), nil, eps)
	if err != nil {
		t.Fatalf("RenderSingbox: %v", err)
	}
	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v\nbody = %s", err, out)
	}
	if len(doc.Outbounds) != 4 {
		t.Fatalf("len = %d, want 4", len(doc.Outbounds))
	}
	// Per-protocol type and one signature field.
	want := map[string]string{
		"vless":  "uuid-v",
		"hy2":    "pw-h",
		"ss":     "aes-256-gcm",
		"trojan": "pw-t",
	}
	for _, ob := range doc.Outbounds {
		tag, _ := ob["tag"].(string)
		expectedSig, ok := want[tag]
		if !ok {
			t.Errorf("unknown tag %q", tag)
			continue
		}
		switch tag {
		case "vless":
			if ob["uuid"] != expectedSig {
				t.Errorf("vless uuid = %v, want %s", ob["uuid"], expectedSig)
			}
		case "hy2":
			if ob["password"] != expectedSig {
				t.Errorf("hy2 password = %v, want %s", ob["password"], expectedSig)
			}
		case "ss":
			if ob["method"] != expectedSig {
				t.Errorf("ss method = %v, want %s", ob["method"], expectedSig)
			}
			if ob["password"] != "pw-s" {
				t.Errorf("ss password = %v, want pw-s", ob["password"])
			}
		case "trojan":
			if ob["password"] != expectedSig {
				t.Errorf("trojan password = %v, want %s", ob["password"], expectedSig)
			}
		}
	}
}

// TestRenderSingbox_SkipsUnrenderable — an endpoint
// whose required params are missing is silently
// skipped, not failed. (Same contract as RenderBase64.)
func TestRenderSingbox_SkipsUnrenderable(t *testing.T) {
	_ = newSingboxFixture(t) // sanity-check the fixture builds
	// Add a second endpoint with a broken HY2 inbound.
	hostsStore := hosts.NewMemoryStore()
	hostsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	nodesStore := nodes.NewMemoryStore()
	nodesStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	inboundsStore := inbounds.NewMemoryStore()
	inboundsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	nodeID := uuid.New()
	_ = nodesStore.Create(context.Background(), &nodes.Node{ID: nodeID, Name: "n", Region: "eu", State: nodes.StateNew, Address: "1.2.3.4"})
	hy2 := &inbounds.Inbound{
		ID: uuid.New(), NodeID: nodeID, Name: "hy2", Protocol: inbounds.ProtocolHysteria2,
		Listen: "::", ListenPort: 443, Enabled: true, Params: map[string]any{}, // no password
	}
	_ = inboundsStore.Create(context.Background(), hy2)
	hostID := uuid.New()
	epID := uuid.New()
	_ = hostsStore.Create(context.Background(), &hosts.Host{
		ID: hostID, Remark: "hy2", Type: hosts.HostTypeDirect, Enabled: true,
		Endpoints: []hosts.Endpoint{{ID: epID, NodeID: nodeID, InboundID: hy2.ID, Weight: 1}},
	})
	hostsSvc := hosts.NewService(hostsStore, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))
	svc := NewService(NewMemoryStore(), hostsSvc, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))

	// A HY2 endpoint with no password — the builder
	// still produces a (technically invalid) outbound
	// with password="". We do NOT skip on missing
	// params; that is the sing-box client's problem.
	// (Documented in render_singbox.go.)
	ep := ResolvedEndpoint{
		Host:     &hosts.Host{ID: hostID, Remark: "hy2", Type: hosts.HostTypeDirect, Enabled: true},
		Endpoint: hosts.Endpoint{ID: epID, NodeID: nodeID, InboundID: hy2.ID, Weight: 1},
		Node:     &nodes.Node{ID: nodeID, Name: "n", Address: "1.2.3.4"},
		Inbound:  hy2,
	}
	out, err := svc.RenderSingbox(context.Background(), nil, []ResolvedEndpoint{ep})
	if err != nil {
		t.Fatalf("RenderSingbox: %v", err)
	}
	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	_ = json.Unmarshal(out, &doc)
	if len(doc.Outbounds) != 1 {
		t.Fatalf("len = %d, want 1 (HY2 with empty params is still rendered, client validates)", len(doc.Outbounds))
	}
	if doc.Outbounds[0]["type"] != "hysteria2" {
		t.Errorf("type = %v, want hysteria2", doc.Outbounds[0]["type"])
	}
}

// TestRenderSingbox_TLSBlockOmittedWhenNoFields — a
// host with no SNI / alpn / utls / reality must not
// emit a TLS block (the sing-box client would then
// dial a plaintext connection; the operator's
// intent is "no TLS layer").
func TestRenderSingbox_TLSBlockOmittedWhenNoFields(t *testing.T) {
	hostsStore := hosts.NewMemoryStore()
	hostsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	nodesStore := nodes.NewMemoryStore()
	nodesStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	inboundsStore := inbounds.NewMemoryStore()
	inboundsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })

	nodeID := uuid.New()
	_ = nodesStore.Create(context.Background(), &nodes.Node{ID: nodeID, Name: "n", Region: "eu", State: nodes.StateNew, Address: "1.2.3.4"})
	// Shadowsocks has no TLS layer; the builder will
	// not call buildSingboxTLS. We feed it through
	// anyway to confirm the helper drops an empty
	// block.
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
	out, err := svc.RenderSingbox(context.Background(), nil, []ResolvedEndpoint{ep})
	if err != nil {
		t.Fatalf("RenderSingbox: %v", err)
	}
	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	_ = json.Unmarshal(out, &doc)
	if _, present := doc.Outbounds[0]["tls"]; present {
		t.Errorf("shadowsocks outbound must not carry a tls block; got %v", doc.Outbounds[0]["tls"])
	}
}

// --- handler integration -------------------------------------------

// TestHandler_RenderSingbox_NotImplementedGone lives in
// handler_test.go (it needs the same chi / handler
// fixture as the other handler tests). This file
// covers the renderer in isolation.
