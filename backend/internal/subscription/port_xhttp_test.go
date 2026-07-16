// SPDX-License-Identifier: AGPL-3.0-or-later

package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// withFixedRand swaps the package-level per-fetch
// picker for one that always returns the given
// index. Use this when the test asserts on a
// specific value.
func withFixedRand(t *testing.T, idx int) {
	t.Helper()
	prev := randPicker
	setRandPicker(func(n int) int {
		if n <= 0 {
			return 0
		}
		if idx >= n {
			idx = n - 1
		}
		return idx
	})
	t.Cleanup(func() {
		setRandPicker(prev)
	})
}

// portsFixture wires a single-user single-host graph
// whose inbound has both a primary `ListenPort` and
// a `ListenPorts` array. The user → plan → pool →
// host graph is what the Subscription Service
// walks; without it, ResolveEndpointsForUser
// returns zero entries and the renderer emits
// nothing.
type portsFixture struct {
	svc        *Service
	host       *hosts.Host
	vless      *inbounds.Inbound
	mainNode   *nodes.Node
	endpointID uuid.UUID
	user       *User
}

func newPortsFixture(t *testing.T) *portsFixture {
	t.Helper()
	subStore := NewMemoryStore()
	subStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	hostsStore := hosts.NewMemoryStore()
	hostsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	nodesStore := nodes.NewMemoryStore()
	nodesStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	inboundsStore := inbounds.NewMemoryStore()
	inboundsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })

	planID := uuid.New()
	poolID := uuid.New()
	userID := uuid.New()
	hostID := uuid.New()
	nodeID := uuid.New()
	inboundID := uuid.New()
	endpointID := uuid.New()

	subStore.WithPlan(&Plan{ID: planID, Name: "starter", Duration: 30 * 24 * time.Hour, ResetPeriod: ResetMonthly})
	subStore.WithPool(&Pool{ID: poolID, Name: "eu", Strategy: PoolStrategyAll})
	subStore.WithPoolMember(PoolMember{PoolID: poolID, HostID: hostID, Weight: 1})
	planRef := planID
	subStore.WithUser(&User{
		ID: userID, Username: "alice", Status: UserStatusActive,
		PlanID: &planRef, SubToken: "tok-alice",
	})

	node := &nodes.Node{ID: nodeID, Name: "edge-01", Region: "eu", State: nodes.StateNew, Address: "10.0.0.1"}
	if err := nodesStore.Create(context.Background(), node); err != nil {
		t.Fatalf("nodes.Create: %v", err)
	}
	vless := &inbounds.Inbound{
		ID:          inboundID,
		NodeID:      nodeID,
		Name:        "vless-multi",
		Protocol:    inbounds.ProtocolVLESS,
		Listen:      "::",
		ListenPort:  443,
		ListenPorts: []int{8080, 8443, 9090},
		Enabled:     true,
		Params: map[string]any{
			"uuid": "00000000-0000-0000-0000-000000000aaa",
		},
	}
	if err := inboundsStore.Create(context.Background(), vless); err != nil {
		t.Fatalf("inbounds.Create: %v", err)
	}
	host := &hosts.Host{
		ID:       hostID,
		Remark:   "Multi-port",
		Type:     hosts.HostTypeDirect,
		Enabled:  true,
		Priority: 10,
		Endpoints: []hosts.Endpoint{{
			ID:        endpointID,
			NodeID:    nodeID,
			InboundID: inboundID,
			Weight:    1,
		}},
	}
	if err := hostsStore.Create(context.Background(), host); err != nil {
		t.Fatalf("hosts.Create: %v", err)
	}

	hostsSvc := hosts.NewService(hostsStore, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))
	nodesSvc := nodes.NewService(nodesStore)
	inboundsSvc := inbounds.NewService(inboundsStore, nodesSvc)
	svc := NewService(subStore, hostsSvc, nodesSvc, inboundsSvc)
	_ = poolID
	return &portsFixture{
		svc:        svc,
		host:       host,
		vless:      vless,
		mainNode:   node,
		endpointID: endpointID,
		user: &User{
			ID:       userID,
			Username: "alice",
			Status:   UserStatusActive,
			PlanID:   &planRef,
		},
	}
}

// TestPickPort_EmptyListenPortsFallsBackToListenPort — the
// historical single-port case.
func TestPickPort_EmptyListenPortsFallsBackToListenPort(t *testing.T) {
	in := &inbounds.Inbound{ListenPort: 443}
	if got := pickPort(in); got != 443 {
		t.Errorf("pickPort with no ListenPorts = %d, want 443", got)
	}
}

// TestPickPort_NilListenPortsFallsBackToListenPort — a nil
// slice is the same as an empty slice.
func TestPickPort_NilListenPortsFallsBackToListenPort(t *testing.T) {
	in := &inbounds.Inbound{ListenPort: 443, ListenPorts: nil}
	if got := pickPort(in); got != 443 {
		t.Errorf("pickPort with nil ListenPorts = %d, want 443", got)
	}
}

// TestPickPort_PicksFromListenPorts — the picker
// honours the index it gets from the random
// function. Test pins index 1 → expects 8443.
func TestPickPort_PicksFromListenPorts(t *testing.T) {
	withFixedRand(t, 1)
	in := &inbounds.Inbound{
		ListenPort:  443,
		ListenPorts: []int{8080, 8443, 9090},
	}
	if got := pickPort(in); got != 8443 {
		t.Errorf("pickPort = %d, want 8443", got)
	}
}

// TestRenderBase64_MultiPort — RenderBase64 picks
func TestRenderBase64_MultiPort(t *testing.T) {
	f := newPortsFixture(t)
	withFixedRand(t, 2)
	eps, err := f.svc.ResolveEndpointsForUser(context.Background(), f.user)
	if err != nil {
		t.Fatalf("ResolveEndpointsForUser: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("len(eps) = %d, want 1", len(eps))
	}
	out, err := f.svc.RenderBase64(context.Background(), nil, eps)
	if err != nil {
		t.Fatalf("RenderBase64: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(out))
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	body := string(decoded)
	if !contains(body, ":9090?") {
		t.Errorf("body did not contain picked port 9090: %s", body)
	}
	if contains(body, ":8080?") {
		t.Errorf("body contained the wrong port 8080: %s", body)
	}
}

// TestRenderSingbox_MultiPort — same contract as
// the base64 renderer; the picked port shows up as
// `server_port` on the outbound.
func TestRenderSingbox_MultiPort(t *testing.T) {
	f := newPortsFixture(t)
	withFixedRand(t, 0)
	eps, err := f.svc.ResolveEndpointsForUser(context.Background(), f.user)
	if err != nil {
		t.Fatalf("ResolveEndpointsForUser: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("len(eps) = %d, want 1", len(eps))
	}
	out, err := f.svc.RenderSingbox(context.Background(), nil, eps)
	if err != nil {
		t.Fatalf("RenderSingbox: %v", err)
	}
	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v\nbody = %s", err, out)
	}
	if len(doc.Outbounds) != 1 {
		t.Fatalf("len(Outbounds) = %d, want 1", len(doc.Outbounds))
	}
	if p, ok := doc.Outbounds[0]["server_port"].(float64); !ok || int(p) != 8080 {
		t.Errorf("server_port = %v, want 8080", doc.Outbounds[0]["server_port"])
	}
}

// TestRenderClash_MultiPort — the Clash YAML
// renders the picked port as `port:` on the proxy.
func TestRenderClash_MultiPort(t *testing.T) {
	f := newPortsFixture(t)
	withFixedRand(t, 1)
	eps, err := f.svc.ResolveEndpointsForUser(context.Background(), f.user)
	if err != nil {
		t.Fatalf("ResolveEndpointsForUser: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("len(eps) = %d, want 1", len(eps))
	}
	out, err := f.svc.RenderClash(context.Background(), nil, eps)
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
	if p, ok := doc.Proxies[0]["port"].(int); !ok || p != 8443 {
		t.Errorf("port = %v, want 8443", doc.Proxies[0]["port"])
	}
}

// --- XHTTP download_settings --------------------------------------------

// xhttpFixture builds a VLESS+XHTTP endpoint whose
// `DownloadHostID` references a separate CDN host.
// The CDN host is NOT in the user's pool — the
// Service looks it up by id directly. The main
// host IS in the user's pool (so the user can see
// it); the CDN host is reachable only via the
// download reference.
type xhttpFixture struct {
	svc        *Service
	vless      *inbounds.Inbound
	mainHost   *hosts.Host
	cdnHost    *hosts.Host
	cdnNode    *nodes.Node
	cdnInbound *inbounds.Inbound
	user       *User
}

func newXHTTPFixture(t *testing.T) *xhttpFixture {
	t.Helper()
	subStore := NewMemoryStore()
	subStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	hostsStore := hosts.NewMemoryStore()
	hostsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	nodesStore := nodes.NewMemoryStore()
	nodesStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	inboundsStore := inbounds.NewMemoryStore()
	inboundsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })

	planID := uuid.New()
	poolID := uuid.New()
	userID := uuid.New()
	mainNodeID := uuid.New()
	cdnNodeID := uuid.New()
	mainInboundID := uuid.New()
	cdnInboundID := uuid.New()
	mainHostID := uuid.New()
	cdnHostID := uuid.New()
	mainEndpointID := uuid.New()
	cdnEndpointID := uuid.New()

	subStore.WithPlan(&Plan{ID: planID, Name: "starter", Duration: 30 * 24 * time.Hour, ResetPeriod: ResetMonthly})
	subStore.WithPool(&Pool{ID: poolID, Name: "eu", Strategy: PoolStrategyAll})
	subStore.WithPoolMember(PoolMember{PoolID: poolID, HostID: mainHostID, Weight: 1})
	planRef := planID
	subStore.WithUser(&User{
		ID: userID, Username: "alice", Status: UserStatusActive,
		PlanID: &planRef, SubToken: "tok-alice",
	})

	mainNode := &nodes.Node{ID: mainNodeID, Name: "edge-01", Region: "eu", State: nodes.StateNew, Address: "10.0.0.1"}
	if err := nodesStore.Create(context.Background(), mainNode); err != nil {
		t.Fatalf("nodes.Create main: %v", err)
	}
	cdnNode := &nodes.Node{ID: cdnNodeID, Name: "cdn-01", Region: "global", State: nodes.StateNew, Address: "cdn.example.com"}
	if err := nodesStore.Create(context.Background(), cdnNode); err != nil {
		t.Fatalf("nodes.Create cdn: %v", err)
	}

	vless := &inbounds.Inbound{
		ID:         mainInboundID,
		NodeID:     mainNodeID,
		Name:       "vless-xhttp",
		Protocol:   inbounds.ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
		Enabled:    true,
		Params: map[string]any{
			"uuid":      "00000000-0000-0000-0000-000000000bbb",
			"transport": "xhttp",
		},
	}
	if err := inboundsStore.Create(context.Background(), vless); err != nil {
		t.Fatalf("inbounds.Create vless: %v", err)
	}
	cdnInbound := &inbounds.Inbound{
		ID:         cdnInboundID,
		NodeID:     cdnNodeID,
		Name:       "cdn-hy2",
		Protocol:   inbounds.ProtocolHysteria2,
		Listen:     "::",
		ListenPort: 8443,
		Enabled:    true,
		Params:     map[string]any{"password": "x"},
	}
	if err := inboundsStore.Create(context.Background(), cdnInbound); err != nil {
		t.Fatalf("inbounds.Create cdn: %v", err)
	}

	cdnHost := &hosts.Host{
		ID:       cdnHostID,
		Remark:   "CDN",
		Type:     hosts.HostTypeDirect,
		Enabled:  true,
		Priority: 0,
		Endpoints: []hosts.Endpoint{{
			ID:        cdnEndpointID,
			NodeID:    cdnNodeID,
			InboundID: cdnInboundID,
			Weight:    1,
		}},
	}
	if err := hostsStore.Create(context.Background(), cdnHost); err != nil {
		t.Fatalf("hosts.Create cdn: %v", err)
	}
	mainHost := &hosts.Host{
		ID:       mainHostID,
		Remark:   "Main",
		Type:     hosts.HostTypeDirect,
		Enabled:  true,
		Priority: 10,
		Endpoints: []hosts.Endpoint{{
			ID:             mainEndpointID,
			NodeID:         mainNodeID,
			InboundID:      mainInboundID,
			Weight:         1,
			DownloadHostID: &cdnHostID,
		}},
	}
	if err := hostsStore.Create(context.Background(), mainHost); err != nil {
		t.Fatalf("hosts.Create main: %v", err)
	}

	hostsSvc := hosts.NewService(hostsStore, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))
	nodesSvc := nodes.NewService(nodesStore)
	inboundsSvc := inbounds.NewService(inboundsStore, nodesSvc)
	svc := NewService(subStore, hostsSvc, nodesSvc, inboundsSvc)
	_ = poolID
	return &xhttpFixture{
		svc:        svc,
		vless:      vless,
		mainHost:   mainHost,
		cdnHost:    cdnHost,
		cdnNode:    cdnNode,
		cdnInbound: cdnInbound,
		user: &User{
			ID:       userID,
			Username: "alice",
			Status:   UserStatusActive,
			PlanID:   &planRef,
		},
	}
}

// TestRenderSingbox_XHTTP_DownloadSettings — the
// sing-box renderer emits a `download_settings`
// block with the download host's address and port
// when the inbound declares `transport: xhttp` and
// the endpoint carries a DownloadHostID.
func TestRenderSingbox_XHTTP_DownloadSettings(t *testing.T) {
	f := newXHTTPFixture(t)
	withFixedRand(t, 0)
	eps, err := f.svc.ResolveEndpointsForUser(context.Background(), f.user)
	if err != nil {
		t.Fatalf("ResolveEndpointsForUser: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("len(eps) = %d, want 1", len(eps))
	}
	if eps[0].Download == nil {
		t.Fatalf("Download is nil; expected resolved CDN endpoint")
	}
	if eps[0].Download.Address != "cdn.example.com" {
		t.Errorf("Download.Address = %q, want cdn.example.com", eps[0].Download.Address)
	}
	if eps[0].Download.Port != 8443 {
		t.Errorf("Download.Port = %d, want 8443", eps[0].Download.Port)
	}

	out, err := f.svc.RenderSingbox(context.Background(), nil, eps)
	if err != nil {
		t.Fatalf("RenderSingbox: %v", err)
	}
	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v\nbody = %s", err, out)
	}
	if len(doc.Outbounds) != 1 {
		t.Fatalf("len(Outbounds) = %d, want 1", len(doc.Outbounds))
	}
	dl, ok := doc.Outbounds[0]["download_settings"].(map[string]any)
	if !ok {
		t.Fatalf("download_settings missing or wrong type: %v", doc.Outbounds[0])
	}
	if dl["address"] != "cdn.example.com" {
		t.Errorf("download_settings.address = %v, want cdn.example.com", dl["address"])
	}
	if p, ok := dl["port"].(float64); !ok || int(p) != 8443 {
		t.Errorf("download_settings.port = %v, want 8443", dl["port"])
	}
}

// TestRenderSingbox_NoDownloadSettingsWithoutXHTTP — the
// download_settings block is gated on the inbound's
// `params.transport == "xhttp"`. An inbound with
// the same DownloadHostID but transport unset
// does NOT emit the block (sing-box would reject
// it).
func TestRenderSingbox_NoDownloadSettingsWithoutXHTTP(t *testing.T) {
	noXHTTP := newXHTTPFixtureWithoutTransport(t)
	withFixedRand(t, 0)
	eps, err := noXHTTP.svc.ResolveEndpointsForUser(context.Background(), noXHTTP.user)
	if err != nil {
		t.Fatalf("ResolveEndpointsForUser: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("len(eps) = %d, want 1", len(eps))
	}
	// Download is still populated — the Service
	// resolves it eagerly regardless of transport.
	if eps[0].Download == nil {
		t.Fatalf("Download is nil; expected resolved CDN endpoint (transport check is renderer's job)")
	}
	out, err := noXHTTP.svc.RenderSingbox(context.Background(), nil, eps)
	if err != nil {
		t.Fatalf("RenderSingbox: %v", err)
	}
	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, has := doc.Outbounds[0]["download_settings"]; has {
		t.Errorf("download_settings emitted without xhttp transport: %v", doc.Outbounds[0])
	}
}

// TestRenderBase64_XHTTP_DownloadIgnored — the
// base64 wire format has no download_settings
// equivalent. The renderer silently ignores the
// Download field.
func TestRenderBase64_XHTTP_DownloadIgnored(t *testing.T) {
	f := newXHTTPFixture(t)
	withFixedRand(t, 0)
	eps, err := f.svc.ResolveEndpointsForUser(context.Background(), f.user)
	if err != nil {
		t.Fatalf("ResolveEndpointsForUser: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("len(eps) = %d, want 1", len(eps))
	}
	out, err := f.svc.RenderBase64(context.Background(), nil, eps)
	if err != nil {
		t.Fatalf("RenderBase64: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(out))
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	body := string(decoded)
	if !contains(body, "vless://") {
		t.Errorf("body missing vless:// scheme: %s", body)
	}
	// The base64 wire format has no place to put
	// the download URL; just check the body does
	// not contain the CDN address as a fragment /
	// query parameter.
	if contains(body, "cdn.example.com") {
		t.Errorf("base64 body referenced the CDN address: %s", body)
	}
}

// TestRenderClash_XHTTP_DownloadIgnored — the
// Clash wire format has no download_settings
// field, so the reference is dropped on the
// floor.
func TestRenderClash_XHTTP_DownloadIgnored(t *testing.T) {
	f := newXHTTPFixture(t)
	withFixedRand(t, 0)
	eps, err := f.svc.ResolveEndpointsForUser(context.Background(), f.user)
	if err != nil {
		t.Fatalf("ResolveEndpointsForUser: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("len(eps) = %d, want 1", len(eps))
	}
	out, err := f.svc.RenderClash(context.Background(), nil, eps)
	if err != nil {
		t.Fatalf("RenderClash: %v", err)
	}
	body := string(out)
	if !contains(body, "type: vless") {
		t.Errorf("Clash body missing vless proxy: %s", body)
	}
	if contains(body, "cdn.example.com") {
		t.Errorf("Clash body referenced the CDN address: %s", body)
	}
}

// xhttpFixtureWithoutTransport is a parallel of
// newXHTTPFixture whose VLESS inbound does not
// declare `params.transport = "xhttp"`. The
// download_settings block is gated on the
// transport; this fixture is the "no block"
// half of the contract test.
func newXHTTPFixtureWithoutTransport(t *testing.T) *xhttpFixture {
	t.Helper()
	subStore := NewMemoryStore()
	subStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	hostsStore := hosts.NewMemoryStore()
	hostsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	nodesStore := nodes.NewMemoryStore()
	nodesStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	inboundsStore := inbounds.NewMemoryStore()
	inboundsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })

	planID := uuid.New()
	poolID := uuid.New()
	userID := uuid.New()
	mainNodeID := uuid.New()
	cdnNodeID := uuid.New()
	mainInboundID := uuid.New()
	cdnInboundID := uuid.New()
	mainHostID := uuid.New()
	cdnHostID := uuid.New()
	mainEndpointID := uuid.New()
	cdnEndpointID := uuid.New()

	subStore.WithPlan(&Plan{ID: planID, Name: "starter", Duration: 30 * 24 * time.Hour, ResetPeriod: ResetMonthly})
	subStore.WithPool(&Pool{ID: poolID, Name: "eu", Strategy: PoolStrategyAll})
	subStore.WithPoolMember(PoolMember{PoolID: poolID, HostID: mainHostID, Weight: 1})
	planRef := planID
	subStore.WithUser(&User{
		ID: userID, Username: "alice", Status: UserStatusActive,
		PlanID: &planRef, SubToken: "tok-alice",
	})

	mainNode := &nodes.Node{ID: mainNodeID, Name: "edge-01", Region: "eu", State: nodes.StateNew, Address: "10.0.0.1"}
	if err := nodesStore.Create(context.Background(), mainNode); err != nil {
		t.Fatalf("nodes.Create main: %v", err)
	}
	cdnNode := &nodes.Node{ID: cdnNodeID, Name: "cdn-01", Region: "global", State: nodes.StateNew, Address: "cdn.example.com"}
	if err := nodesStore.Create(context.Background(), cdnNode); err != nil {
		t.Fatalf("nodes.Create cdn: %v", err)
	}

	vless := &inbounds.Inbound{
		ID:         mainInboundID,
		NodeID:     mainNodeID,
		Name:       "vless-ws",
		Protocol:   inbounds.ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
		Enabled:    true,
		Params: map[string]any{
			"uuid":      "00000000-0000-0000-0000-000000000bbb",
			"transport": "ws",
		},
	}
	if err := inboundsStore.Create(context.Background(), vless); err != nil {
		t.Fatalf("inbounds.Create vless: %v", err)
	}
	cdnInbound := &inbounds.Inbound{
		ID:         cdnInboundID,
		NodeID:     cdnNodeID,
		Name:       "cdn-hy2",
		Protocol:   inbounds.ProtocolHysteria2,
		Listen:     "::",
		ListenPort: 8443,
		Enabled:    true,
		Params:     map[string]any{"password": "x"},
	}
	if err := inboundsStore.Create(context.Background(), cdnInbound); err != nil {
		t.Fatalf("inbounds.Create cdn: %v", err)
	}

	cdnHost := &hosts.Host{
		ID:       cdnHostID,
		Remark:   "CDN",
		Type:     hosts.HostTypeDirect,
		Enabled:  true,
		Priority: 0,
		Endpoints: []hosts.Endpoint{{
			ID:        cdnEndpointID,
			NodeID:    cdnNodeID,
			InboundID: cdnInboundID,
			Weight:    1,
		}},
	}
	if err := hostsStore.Create(context.Background(), cdnHost); err != nil {
		t.Fatalf("hosts.Create cdn: %v", err)
	}
	mainHost := &hosts.Host{
		ID:       mainHostID,
		Remark:   "Main",
		Type:     hosts.HostTypeDirect,
		Enabled:  true,
		Priority: 10,
		Endpoints: []hosts.Endpoint{{
			ID:             mainEndpointID,
			NodeID:         mainNodeID,
			InboundID:      mainInboundID,
			Weight:         1,
			DownloadHostID: &cdnHostID,
		}},
	}
	if err := hostsStore.Create(context.Background(), mainHost); err != nil {
		t.Fatalf("hosts.Create main: %v", err)
	}

	hostsSvc := hosts.NewService(hostsStore, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))
	nodesSvc := nodes.NewService(nodesStore)
	inboundsSvc := inbounds.NewService(inboundsStore, nodesSvc)
	svc := NewService(subStore, hostsSvc, nodesSvc, inboundsSvc)
	_ = poolID
	return &xhttpFixture{
		svc:        svc,
		vless:      vless,
		mainHost:   mainHost,
		cdnHost:    cdnHost,
		cdnNode:    cdnNode,
		cdnInbound: cdnInbound,
		user: &User{
			ID:       userID,
			Username: "alice",
			Status:   UserStatusActive,
			PlanID:   &planRef,
		},
	}
}

// TestResolveDownload_MissingHostSkipped — the
// Service silently skips a missing download host
// (fail-soft). The endpoint's Download is nil and
// the sing-box renderer omits the block.
func TestResolveDownload_MissingHostSkipped(t *testing.T) {
	subStore := NewMemoryStore()
	subStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	hostsStore := hosts.NewMemoryStore()
	hostsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	nodesStore := nodes.NewMemoryStore()
	nodesStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	inboundsStore := inbounds.NewMemoryStore()
	inboundsStore.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })

	planID := uuid.New()
	poolID := uuid.New()
	userID := uuid.New()
	nodeID := uuid.New()
	inboundID := uuid.New()
	hostID := uuid.New()
	endpointID := uuid.New()
	missingID := uuid.New() // not created

	subStore.WithPlan(&Plan{ID: planID, Name: "starter", Duration: 30 * 24 * time.Hour, ResetPeriod: ResetMonthly})
	subStore.WithPool(&Pool{ID: poolID, Name: "eu", Strategy: PoolStrategyAll})
	subStore.WithPoolMember(PoolMember{PoolID: poolID, HostID: hostID, Weight: 1})
	planRef := planID
	subStore.WithUser(&User{
		ID: userID, Username: "alice", Status: UserStatusActive,
		PlanID: &planRef, SubToken: "tok-alice",
	})

	node := &nodes.Node{ID: nodeID, Name: "edge-01", Region: "eu", State: nodes.StateNew, Address: "10.0.0.1"}
	if err := nodesStore.Create(context.Background(), node); err != nil {
		t.Fatalf("nodes.Create: %v", err)
	}
	vless := &inbounds.Inbound{
		ID: inboundID, NodeID: nodeID, Name: "vless",
		Protocol: inbounds.ProtocolVLESS, Listen: "::", ListenPort: 443, Enabled: true,
		Params: map[string]any{"uuid": "00000000-0000-0000-0000-000000000ccc", "transport": "xhttp"},
	}
	if err := inboundsStore.Create(context.Background(), vless); err != nil {
		t.Fatalf("inbounds.Create: %v", err)
	}
	host := &hosts.Host{
		ID: hostID, Remark: "Main", Type: hosts.HostTypeDirect, Enabled: true, Priority: 10,
		Endpoints: []hosts.Endpoint{{
			ID: endpointID, NodeID: nodeID, InboundID: inboundID, Weight: 1,
			DownloadHostID: &missingID,
		}},
	}
	if err := hostsStore.Create(context.Background(), host); err != nil {
		t.Fatalf("hosts.Create: %v", err)
	}

	hostsSvc := hosts.NewService(hostsStore, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))
	nodesSvc := nodes.NewService(nodesStore)
	inboundsSvc := inbounds.NewService(inboundsStore, nodesSvc)
	svc := NewService(subStore, hostsSvc, nodesSvc, inboundsSvc)

	eps, err := svc.ResolveEndpointsForUser(context.Background(), &User{
		ID: userID, Username: "alice", Status: UserStatusActive,
		PlanID: &planRef,
	})
	if err != nil {
		t.Fatalf("ResolveEndpointsForUser: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("len(eps) = %d, want 1", len(eps))
	}
	if eps[0].Download != nil {
		t.Errorf("Download = %+v, want nil (download host does not exist)", eps[0].Download)
	}
}

// contains is a tiny strings.Contains alias to keep
// the test assertions terse.
func contains(s, substr string) bool {
	if substr == "" {
		return true
	}
	return indexOf(s, substr) >= 0
}

// indexOf is a minimal `strings.Index` to avoid an
// extra import.
func indexOf(s, substr string) int {
	n, m := len(s), len(substr)
	if m == 0 {
		return 0
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == substr {
			return i
		}
	}
	return -1
}
