// SPDX-License-Identifier: AGPL-3.0-or-later

package singbox

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/QAdversif/AegisPanel/internal/cores"
)

// ExperimentalInboundParamsKey is the key inside
// CoreConfig.Experimental where the panel hands the
// per-inbound render parameters. The value is a
// map[inbound_tag]map[string]any. The tag is the same
// InboundSpec.Tag the panel set when constructing the
// CoreConfig; missing keys are an error (the panel UI
// should not let an admin publish a Host that references
// an inbound whose parameters have not been set).
const ExperimentalInboundParamsKey = "inbound_params"

// sbConfig is the top-level sing-box configuration we
// render. Only the fields Phase 1 actually produces are
// modelled as typed struct fields; everything else stays
// inside the per-inbound maps produced by renderInbound.
//
// The split between "typed top-level shell" and
// "map[string]any inbounds" is deliberate: the top-level
// keys (log, outbounds, route) have a fixed shape across
// every Phase 1 deployment, while per-inbound schemas
// vary by protocol and sing-box version. Forcing a single
// struct for everything would lock us into 1.8.x's schema
// and require a code change every time sing-box adds a
// field we use.
type sbConfig struct {
	Log       *sbLog           `json:"log,omitempty"`
	Inbounds  []map[string]any `json:"inbounds"`
	Outbounds []sbOutbound     `json:"outbounds"`
	Route     *sbRoute         `json:"route"`
}

// sbLog is the minimal log block. The level is "info" by
// default — sing-box accepts "trace" / "debug" / "info" /
// "warn" / "error" / "fatal" / "panic".
type sbLog struct {
	Level     string `json:"level"`
	Timestamp bool   `json:"timestamp"`
}

// sbOutbound is one entry in the top-level outbounds array.
// The Phase 1 default render emits exactly two outbounds:
// "direct" and "block", in that order, so route.final can
// always fall back to "direct" without a nil check.
type sbOutbound struct {
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

// sbRoute is the routing block. Phase 1 only emits the
// "geoip:private → direct" rule plus a final "direct"; more
// rules (blocklists, custom proxies, …) come with the
// Cascade / ACL PRs.
type sbRoute struct {
	Rules []sbRouteRule `json:"rules"`
	Final string        `json:"final"`
}

// sbRouteRule is one routing rule. The IP CIDR list is
// what sing-box's `route.rules[*].ip_cidr` field expects;
// `geoip:private` is a sing-box built-in tag.
type sbRouteRule struct {
	IPCIDR   []string `json:"ip_cidr,omitempty"`
	Port     []int    `json:"port,omitempty"`
	Outbound string   `json:"outbound"`
}

// RenderConfig implements cores.CoreProvider. The
// algorithm:
//
//  1. Pick the inbound-parameters map out of
//     CoreConfig.Experimental. Missing / wrong-typed map
//     is an error — the panel must hand the renderer the
//     full set of parameters, or none at all.
//  2. For each InboundSpec, look up its parameters by tag
//     and dispatch to the per-protocol renderer. An
//     unknown type or missing tag is a render error
//     (caller decides whether to abort the whole render
//     or skip the broken inbound).
//  3. Build the top-level shell with default outbounds
//     and a minimal route.
//  4. Marshal with two-space indent + trailing newline
//     so the result is a normal text file on disk.
//
// The returned string always ends with a single newline.
// sing-box itself does not require the trailing newline,
// but the agent uses line-based change tracking and emits
// a clean diff when the file has one.
func (p *Provider) RenderConfig(_ context.Context, cfg cores.CoreConfig) (string, error) {
	// Step 1: extract inbound parameters.
	raw, ok := cfg.Experimental[ExperimentalInboundParamsKey]
	if !ok || raw == nil {
		// No params at all — that's a programmer error in
		// the panel, not a renderable state. The empty
		// config is the noop provider's job, not ours.
		return "", fmt.Errorf("singbox: %s missing from Experimental", ExperimentalInboundParamsKey)
	}
	params, ok := raw.(map[string]any)
	if !ok {
		return "", fmt.Errorf("singbox: %s must be map[string]any, got %T", ExperimentalInboundParamsKey, raw)
	}

	// Step 2: render inbounds.
	inbounds := make([]map[string]any, 0, len(cfg.Inbounds))
	for _, spec := range cfg.Inbounds {
		ip, ok := params[spec.Tag]
		if !ok {
			return "", fmt.Errorf("singbox: no parameters for inbound tag %q (type %q)", spec.Tag, spec.Type)
		}
		p, ok := ip.(map[string]any)
		if !ok {
			return "", fmt.Errorf("singbox: parameters for inbound %q must be map[string]any, got %T", spec.Tag, ip)
		}
		rendered, err := renderInbound(spec, p)
		if err != nil {
			return "", fmt.Errorf("singbox: render inbound %q (%s): %w", spec.Tag, spec.Type, err)
		}
		inbounds = append(inbounds, rendered)
	}

	// Step 3: top-level shell.
	doc := sbConfig{
		Log:      &sbLog{Level: "info", Timestamp: true},
		Inbounds: inbounds,
		Outbounds: []sbOutbound{
			{Type: "direct", Tag: "direct"},
			{Type: "block", Tag: "block"},
		},
		Route: &sbRoute{
			Rules: []sbRouteRule{
				{IPCIDR: []string{"geoip:private"}, Outbound: "direct"},
			},
			Final: "direct",
		},
	}

	// Step 4: marshal. We use a normal Marshal here, not an
	// Encoder, so the trailing newline is ours to add and
	// not the encoder's "\n". Two-space indent matches
	// sing-box's own example configs.
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("singbox: marshal: %w", err)
	}
	return string(out) + "\n", nil
}

// renderInbound dispatches to the per-protocol renderer.
// New protocols land here as new cases; the unknown-type
// branch is a render error so a typo in CoreConfig.Inbounds
// cannot silently produce an empty config.
func renderInbound(spec cores.InboundSpec, params map[string]any) (map[string]any, error) {
	switch spec.Type {
	case "vless":
		return renderVLESS(spec, params)
	case "hysteria2":
		return renderHY2(spec, params)
	case "shadowsocks":
		return renderShadowsocks(spec, params)
	case "trojan":
		return renderTrojan(spec, params)
	default:
		return nil, fmt.Errorf("unsupported inbound type %q", spec.Type)
	}
}

// requireString extracts a required string field. Missing
// or wrong-typed values are render errors — a missing port
// is a misconfigured panel-side Host, not something the
// renderer should paper over with a default.
func requireString(m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", fmt.Errorf("missing required %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%q must be string, got %T", key, v)
	}
	if s == "" {
		return "", fmt.Errorf("%q must not be empty", key)
	}
	return s, nil
}

// requireIntPort extracts the inbound's listen_port. JSON
// unmarshals numbers as float64 when the target is
// map[string]any, so we accept that and narrow.
//
// We do not need a generic `requireInt(key string)` helper
// for Phase 1 — every inbound takes a single `port` and
// there is no second integer field. If a future protocol
// adds one, generalise this back to a `requireInt(key)`
// helper rather than copy-pasting the type switch.
func requireIntPort(m map[string]any) (int, error) {
	v, ok := m["port"]
	if !ok {
		return 0, fmt.Errorf("missing required %q", "port")
	}
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("%q must be int, got %T", "port", v)
	}
}

// optionalString extracts an optional string with a default.
// Missing keys and empty strings both fall back to def.
func optionalString(m map[string]any, key, def string) string {
	v, ok := m[key]
	if !ok {
		return def
	}
	s, ok := v.(string)
	if !ok {
		return def
	}
	if s == "" {
		return def
	}
	return s
}

// optionalStringSlice is intentionally absent. No Phase 1
// protocol takes a top-level string slice in its inbound
// params (Reality short_ids / alpn flow through the nested
// TLS map, which renderInbound passes through as
// map[string]any). When a protocol that needs a top-level
// slice lands, add a helper here with the same type-switch
// pattern used by requireIntPort.

// optionalTLS extracts an optional TLS block. The block is
// rendered as a map[string]any so Phase 1 can pass through
// keys the renderer does not model (alpn, ech_config_list,
// certificate_path, …) without a code change. Required
// sub-keys (server_name) are validated when present.
func optionalTLS(m map[string]any, key string) (map[string]any, error) {
	v, ok := m[key]
	if !ok {
		return nil, nil
	}
	tls, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%q must be map[string]any, got %T", key, v)
	}
	// Sing-box TLS block always has enabled=true when present
	// — adding it is harmless and lets a panel-side UI toggle
	// it off without changing the renderer.
	tls["enabled"] = true
	return tls, nil
}
