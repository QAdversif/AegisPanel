// SPDX-License-Identifier: AGPL-3.0-or-later

package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// varsFixture is the minimum data needed to test the
// format-vars + wildcard-salt enrichment. It seeds a
// single VLESS inbound on a single node with a single
// host whose Address / DisplayName / SNI are full of
// `{VARIABLE}` placeholders and `*` wildcards. Tests
// that need a different shape build a new fixture.
type varsFixture struct {
	svc *Service
	u   *User
	ep  ResolvedEndpoint
}

func newVarsFixture(t *testing.T) *varsFixture {
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
	userID := uuid.New()

	node := &nodes.Node{ID: nodeID, Name: "lv-01", Region: "eu", State: nodes.StateNew, Address: "10.0.0.1"}
	if err := nodesStore.Create(context.Background(), node); err != nil {
		t.Fatalf("nodes.Create: %v", err)
	}
	vless := &inbounds.Inbound{
		ID: inboundID, NodeID: nodeID, Name: "vless-reality",
		Protocol: inbounds.ProtocolVLESS, Listen: "::", ListenPort: 443, Enabled: true,
		Params: map[string]any{
			"uuid":        "00000000-0000-0000-0000-000000000aaa",
			"flow":        "xtls-rprx-vision",
			"sni":         "cdn.example.com",
			"fingerprint": "chrome",
		},
	}
	if err := inboundsStore.Create(context.Background(), vless); err != nil {
		t.Fatalf("inbounds.Create: %v", err)
	}
	host := &hosts.Host{
		ID:          hostID,
		Remark:      "{USERNAME}@{PROTOCOL}",
		DisplayName: "{USERNAME}-*",
		Type:        hosts.HostTypeDirect,
		Enabled:     true,
		Priority:    10,
		Endpoints: []hosts.Endpoint{{
			ID:        endpointID,
			NodeID:    nodeID,
			InboundID: inboundID,
			Weight:    1,
			Address:   []string{"cdn.*.example.com"},
			SNI:       []string{"*.example.com"},
		}},
	}
	if err := hostsStore.Create(context.Background(), host); err != nil {
		t.Fatalf("hosts.Create: %v", err)
	}

	hostsSvc := hosts.NewService(hostsStore, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))
	nodesSvc := nodes.NewService(nodesStore)
	inboundsSvc := inbounds.NewService(inboundsStore, nodesSvc)
	svc := NewService(NewMemoryStore(), hostsSvc, nodesSvc, inboundsSvc)
	expire := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	u := &User{
		ID:                userID,
		Username:          "alice",
		Status:            UserStatusActive,
		ExpireAt:          &expire,
		TrafficLimitBytes: 100 * 1024 * 1024 * 1024, // 100 GB
		TrafficUsedBytes:  2 * 1024 * 1024 * 1024,   // 2 GB used
	}
	ep := ResolvedEndpoint{
		Host:     host,
		Endpoint: host.Endpoints[0],
		Node:     node,
		Inbound:  vless,
	}
	return &varsFixture{svc: svc, u: u, ep: ep}
}

// TestComputeSalt_StableWithinMinute — the same
// (host, user, minute) triple produces the same
// salt. Two calls inside the same minute must
// match; a call one minute later must differ.
func TestComputeSalt_StableWithinMinute(t *testing.T) {
	hostID := uuid.New()
	userID := uuid.New()
	now := time.Date(2026, 7, 16, 12, 30, 15, 0, time.UTC)

	s1 := computeSalt(hostID, userID, now)
	s2 := computeSalt(hostID, userID, now.Add(20*time.Second))
	if s1 != s2 {
		t.Errorf("salt changed inside the same minute: %q vs %q", s1, s2)
	}
	if len(s1) != 8 {
		t.Errorf("salt len = %d, want 8", len(s1))
	}
	s3 := computeSalt(hostID, userID, now.Add(90*time.Second))
	if s1 == s3 {
		t.Errorf("salt did not change across a minute boundary: %q", s1)
	}
}

// TestComputeSalt_DifferentInputsDifferentOutput — the
// salt is sensitive to every input. Different host,
// different user, or different minute produces a
// different salt.
func TestComputeSalt_DifferentInputsDifferentOutput(t *testing.T) {
	h1, h2 := uuid.New(), uuid.New()
	u1, u2 := uuid.New(), uuid.New()
	now := time.Date(2026, 7, 16, 12, 30, 0, 0, time.UTC)

	sa := computeSalt(h1, u1, now)
	sb := computeSalt(h2, u1, now)
	if sa == sb {
		t.Errorf("salt same for different host: %q", sa)
	}
	sc := computeSalt(h1, u2, now)
	if sa == sc {
		t.Errorf("salt same for different user: %q", sa)
	}
	sd := computeSalt(h1, u1, now.Add(time.Minute))
	if sa == sd {
		t.Errorf("salt same across a minute: %q", sa)
	}
}

// TestBuildUserVars_AllFieldsPopulated — every key in
// the closed variable set is present, with the value
// derived from the User. PROTOCOL is the per-endpoint
// injection default ("?") at this layer.
func TestBuildUserVars_AllFieldsPopulated(t *testing.T) {
	expire := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	u := &User{
		Username:          "alice",
		Status:            UserStatusActive,
		ExpireAt:          &expire,
		TrafficLimitBytes: 100 * 1024 * 1024 * 1024, // 100 GB
		TrafficUsedBytes:  2 * 1024 * 1024 * 1024,   // 2 GB
	}
	vars := buildUserVars(u)
	if vars["USERNAME"] != "alice" {
		t.Errorf("USERNAME = %q, want alice", vars["USERNAME"])
	}
	if vars["DAYS_LEFT"] == "0" || vars["DAYS_LEFT"] == "∞" {
		t.Errorf("DAYS_LEFT = %q, want positive integer", vars["DAYS_LEFT"])
	}
	if vars["EXPIRE_DATE"] != "2026-12-31" {
		t.Errorf("EXPIRE_DATE = %q, want 2026-12-31", vars["EXPIRE_DATE"])
	}
	if vars["STATUS_EMOJI"] == "" {
		t.Errorf("STATUS_EMOJI empty")
	}
	if vars["USAGE_PERCENTAGE"] != "2" {
		t.Errorf("USAGE_PERCENTAGE = %q, want 2", vars["USAGE_PERCENTAGE"])
	}
	if vars["PROTOCOL"] != "?" {
		t.Errorf("PROTOCOL = %q, want ? (per-endpoint default)", vars["PROTOCOL"])
	}
	if vars["DATA_USAGE"] == "" || vars["DATA_USAGE"] == "∞" {
		t.Errorf("DATA_USAGE = %q, want formatted bytes", vars["DATA_USAGE"])
	}
	if vars["DATA_LIMIT"] == "" || vars["DATA_LIMIT"] == "∞" {
		t.Errorf("DATA_LIMIT = %q, want formatted bytes", vars["DATA_LIMIT"])
	}
	if vars["DATA_LEFT"] == "" || vars["DATA_LEFT"] == "∞" {
		t.Errorf("DATA_LEFT = %q, want formatted bytes", vars["DATA_LEFT"])
	}
}

// TestBuildUserVars_NoExpire — when ExpireAt is nil,
// DAYS_LEFT and EXPIRE_DATE are "∞" (operator
// convention: "no expiry set = never expires").
func TestBuildUserVars_NoExpire(t *testing.T) {
	u := &User{Username: "bob", Status: UserStatusActive}
	vars := buildUserVars(u)
	if vars["DAYS_LEFT"] != "∞" {
		t.Errorf("DAYS_LEFT = %q, want ∞", vars["DAYS_LEFT"])
	}
	if vars["EXPIRE_DATE"] != "∞" {
		t.Errorf("EXPIRE_DATE = %q, want ∞", vars["EXPIRE_DATE"])
	}
}

// TestBuildUserVars_NoTrafficLimit — limit = 0
// (unset) makes DATA_LIMIT and DATA_LEFT "∞";
// USAGE_PERCENTAGE is 0 (caller's responsibility
// to render the human-readable percent elsewhere).
func TestBuildUserVars_NoTrafficLimit(t *testing.T) {
	u := &User{Username: "carol", Status: UserStatusActive, TrafficUsedBytes: 1024 * 1024}
	vars := buildUserVars(u)
	if vars["DATA_LIMIT"] != "∞" {
		t.Errorf("DATA_LIMIT = %q, want ∞", vars["DATA_LIMIT"])
	}
	if vars["DATA_LEFT"] != "∞" {
		t.Errorf("DATA_LEFT = %q, want ∞", vars["DATA_LEFT"])
	}
	if vars["USAGE_PERCENTAGE"] != "0" {
		t.Errorf("USAGE_PERCENTAGE = %q, want 0", vars["USAGE_PERCENTAGE"])
	}
}

// TestStatusEmoji — every closed-set status maps to
// a non-empty emoji. Unknown statuses get the
// fallback "❓" (defensive default for forward
// compatibility).
func TestStatusEmoji(t *testing.T) {
	cases := map[UserStatus]string{
		UserStatusActive:    "✅",
		UserStatusGrace:     "⌛️",
		UserStatusDisabled:  "❌",
		UserStatusExpired:   "🪫",
		UserStatusDeleted:   "🔌",
		UserStatus("bogus"): "❓",
	}
	for s, want := range cases {
		if got := statusEmoji(s); got != want {
			t.Errorf("statusEmoji(%q) = %q, want %q", s, got, want)
		}
	}
}

// TestFormatBytes — boundary cases for the byte
// formatter. 0 / negative = "∞". < 1 MiB = bytes.
// < 1 GiB = KB. < 1 TiB = GB. >= 1 TiB = TB.
// (Note: the function skips the MB label entirely;
// this is the established behaviour from the
// Phase 0 data model and the wire format is
// stable. A future PR may add a "%.1f MB" band.)
func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{
		0:                         "∞",
		-1:                        "∞",
		512:                       "512 B",
		1024:                      "1024 B", // < 1 MiB, so bytes
		1024 * 1024:               "1024.0 KB",
		2 * 1024 * 1024:           "2048.0 KB", // < 1 GiB, so KB
		1024 * 1024 * 1024:        "1.0 GB",
		2 * 1024 * 1024 * 1024:    "2.0 GB",
		1024 * 1024 * 1024 * 1024: "1.0 TB",
	}
	for in, want := range cases {
		if got := formatBytes(in); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestFormatDaysLeft — past-dated expire returns "0".
// Future-dated expire returns the integer day count.
// The (now + 1 day) test gives 1.
func TestFormatDaysLeft(t *testing.T) {
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		expire time.Time
		want   string
	}{
		{now.Add(-time.Hour), "0"},
		{now.Add(24 * time.Hour), "1"},
		{now.Add(7 * 24 * time.Hour), "7"},
	}
	for _, c := range cases {
		if got := formatDaysLeft(c.expire, now); got != c.want {
			t.Errorf("formatDaysLeft(%v) = %q, want %q", c.expire, got, c.want)
		}
	}
}

// TestFormatUsagePercent — the integer percent
// (used / limit) is capped at 100. Limit = 0
// returns "0".
func TestFormatUsagePercent(t *testing.T) {
	cases := []struct {
		used, limit int64
		want        string
	}{
		{0, 100, "0"},
		{50, 100, "50"},
		{100, 100, "100"},
		{150, 100, "100"}, // capped
		{100, 0, "0"},     // no limit
	}
	for _, c := range cases {
		if got := formatUsagePercent(c.used, c.limit); got != c.want {
			t.Errorf("formatUsagePercent(%d, %d) = %q, want %q", c.used, c.limit, got, c.want)
		}
	}
}

// TestEnrichEndpoint_AppliesWildcardAndPerEndpointVars — the
// per-endpoint enrich pass applies the wildcard salt to
// every `*` and substitutes every {VAR} placeholder. The
// per-endpoint PROTOCOL is injected into the local
// vars map so a host remark like "{USERNAME}@{PROTOCOL}"
// resolves correctly.
func TestEnrichEndpoint_AppliesWildcardAndPerEndpointVars(t *testing.T) {
	f := newVarsFixture(t)
	rc := &RenderContext{
		Vars: map[string]string{
			"USERNAME": "alice",
			// PROTOCOL gets overwritten per-endpoint
			// in the local copy.
			"PROTOCOL": "?",
		},
	}
	enriched := enrichEndpoint(f.ep, rc)

	// displayName: "{USERNAME}-*" -> "alice-<8hex>"
	if !strings.HasPrefix(enriched.Host.DisplayName, "alice-") {
		t.Errorf("DisplayName = %q, want prefix alice-", enriched.Host.DisplayName)
	}
	if strings.Contains(enriched.Host.DisplayName, "*") {
		t.Errorf("DisplayName still contains *: %q", enriched.Host.DisplayName)
	}
	if strings.Contains(enriched.Host.DisplayName, "{") {
		t.Errorf("DisplayName still contains a placeholder: %q", enriched.Host.DisplayName)
	}
	// remark: "{USERNAME}@{PROTOCOL}" -> "alice@vless"
	// (PROTOCOL is per-endpoint, comes from
	// enriched.Inbound.Protocol).
	if enriched.Host.Remark != "alice@vless" {
		t.Errorf("Remark = %q, want alice@vless", enriched.Host.Remark)
	}
	// endpoint address: "cdn.*.example.com" -> 8 hex inserted.
	if len(enriched.Endpoint.Address) != 1 {
		t.Fatalf("Address len = %d, want 1", len(enriched.Endpoint.Address))
	}
	if strings.Contains(enriched.Endpoint.Address[0], "*") {
		t.Errorf("Address[0] still contains *: %q", enriched.Endpoint.Address[0])
	}
	if !strings.HasPrefix(enriched.Endpoint.Address[0], "cdn.") || !strings.HasSuffix(enriched.Endpoint.Address[0], ".example.com") {
		t.Errorf("Address[0] = %q, want cdn.*.example.com shape", enriched.Endpoint.Address[0])
	}
	// endpoint SNI: same wildcard treatment.
	if len(enriched.Endpoint.SNI) != 1 || strings.Contains(enriched.Endpoint.SNI[0], "*") {
		t.Errorf("SNI[0] = %v, want 1 entry with no *", enriched.Endpoint.SNI)
	}
}

// TestEnrichEndpoint_UnknownPlaceholderLeftIntact — an
// operator who types {XYZ} in a remark gets {XYZ} in
// the rendered output, not an empty string. This is
// the convention from Marzban / 3X-UI.
func TestEnrichEndpoint_UnknownPlaceholderLeftIntact(t *testing.T) {
	f := newVarsFixture(t)
	f.ep.Host.Remark = "{USERNAME} ({XYZ})"
	rc := &RenderContext{Vars: map[string]string{"USERNAME": "alice"}}
	enriched := enrichEndpoint(f.ep, rc)
	if !strings.Contains(enriched.Host.Remark, "{XYZ}") {
		t.Errorf("Remark = %q, want literal {XYZ} preserved", enriched.Host.Remark)
	}
	if !strings.Contains(enriched.Host.Remark, "alice") {
		t.Errorf("Remark = %q, want alice substituted", enriched.Host.Remark)
	}
}

// TestEnrichEndpoint_NilContextIsNoOp — the test-friendly
// baseline: nil context returns a copy of the input
// unchanged. This is the path the existing render
// tests (which pass u=nil) take.
func TestEnrichEndpoint_NilContextIsNoOp(t *testing.T) {
	f := newVarsFixture(t)
	got := enrichEndpoint(f.ep, nil)
	if got.Host.Remark != f.ep.Host.Remark {
		t.Errorf("Remark changed: %q vs %q", got.Host.Remark, f.ep.Host.Remark)
	}
	if len(got.Endpoint.SNI) != len(f.ep.Endpoint.SNI) || got.Endpoint.SNI[0] != f.ep.Endpoint.SNI[0] {
		t.Errorf("SNI changed: %v vs %v", got.Endpoint.SNI, f.ep.Endpoint.SNI)
	}
}

// TestApplyWildcardToStringSlice — every entry in
// the slice gets the salt substituted; the slice
// identity is preserved when nothing changed.
func TestApplyWildcardToStringSlice(t *testing.T) {
	in := []string{"a.*", "b.*", "c"}
	out := applyWildcardToStringSlice(in, "deadbeef")
	if out[0] != "a.deadbeef" || out[1] != "b.deadbeef" || out[2] != "c" {
		t.Errorf("got %v", out)
	}
	// Empty salt is a no-op and the input slice is
	// returned unchanged (same backing array).
	noOp := applyWildcardToStringSlice(in, "")
	if &noOp[0] != &in[0] {
		t.Errorf("empty salt should return input slice, got new slice")
	}
}

// TestApplyFormatVariables_PreservesUnknown — the
// helper leaves an unknown placeholder intact even
// when surrounded by known ones.
func TestApplyFormatVariables_PreservesUnknown(t *testing.T) {
	vars := map[string]string{"USERNAME": "alice"}
	got := applyFormatVariables("{USERNAME}/{XYZ}/{USERNAME}", vars)
	if got != "alice/{XYZ}/alice" {
		t.Errorf("got %q, want alice/{XYZ}/alice", got)
	}
}

// TestRenderVars_RoundTrip_Base64 — RenderBase64
// with a user attached applies the enrichment and
// the resulting URI contains the substituted values
// (no `*`, no `{USERNAME}`).
func TestRenderVars_RoundTrip_Base64(t *testing.T) {
	f := newVarsFixture(t)
	out, err := f.svc.RenderBase64(context.Background(), f.u, []ResolvedEndpoint{f.ep})
	if err != nil {
		t.Fatalf("RenderBase64: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(out))
	if err != nil {
		t.Fatalf("base64 decode: %v\nbody = %s", err, out)
	}
	body := string(decoded)
	if strings.Contains(body, "*") {
		t.Errorf("body still contains *: %s", body)
	}
	if strings.Contains(body, "{USERNAME}") || strings.Contains(body, "{PROTOCOL}") {
		t.Errorf("body still contains an unsubstituted placeholder: %s", body)
	}
	if !strings.Contains(body, "alice") {
		t.Errorf("body missing USERNAME substitution: %s", body)
	}
	// The host part uses cdn.<hex>.example.com; the
	// base64 decodes to a vless:// URI.
	if !strings.Contains(body, "vless://") {
		t.Errorf("body missing vless:// scheme: %s", body)
	}
	if !strings.Contains(body, ".example.com") {
		t.Errorf("body missing substituted domain: %s", body)
	}
}

// TestRenderVars_RoundTrip_Singbox — RenderSingbox
// applies the same enrichment; the resulting JSON
// tag (per-endpoint displayName) contains the
// substituted USERNAME.
func TestRenderVars_RoundTrip_Singbox(t *testing.T) {
	f := newVarsFixture(t)
	out, err := f.svc.RenderSingbox(context.Background(), f.u, []ResolvedEndpoint{f.ep})
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
	ob := doc.Outbounds[0]
	tag, _ := ob["tag"].(string)
	if !strings.HasPrefix(tag, "alice-") {
		t.Errorf("tag = %q, want prefix alice-", tag)
	}
	if strings.Contains(tag, "*") || strings.Contains(tag, "{") {
		t.Errorf("tag still contains unsubstituted content: %q", tag)
	}
}

// TestRenderVars_RoundTrip_Clash — RenderClash
// applies the same enrichment; the resulting YAML
// `name` field is the per-endpoint displayName.
func TestRenderVars_RoundTrip_Clash(t *testing.T) {
	f := newVarsFixture(t)
	out, err := f.svc.RenderClash(context.Background(), f.u, []ResolvedEndpoint{f.ep})
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
	name, _ := doc.Proxies[0]["name"].(string)
	if !strings.HasPrefix(name, "alice-") {
		t.Errorf("name = %q, want prefix alice-", name)
	}
	if strings.Contains(name, "*") || strings.Contains(name, "{") {
		t.Errorf("name still contains unsubstituted content: %q", name)
	}
}
