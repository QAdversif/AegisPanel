// SPDX-License-Identifier: AGPL-3.0-or-later
//
// sing-box renderer. The wire format is the sing-box
// "outbounds" JSON document — a subset of a full
// sing-box config, sufficient to be imported as a
// remote subscription by Hiddify, Streisand, NekoBox,
// Karing, V2Box, and sing-box CLI.
//
// # Wire format
//
// The top-level object is:
//
//	{
//	  "outbounds": [ <outbound>, <outbound>, ... ]
//	}
//
// Each outbound has a `type` field that determines the
// shape:
//
//	vless        -> { type, tag, server, server_port,
//	                  uuid, flow?, tls? }
//	hysteria2    -> { type, tag, server, server_port,
//	                  password, tls? }
//	shadowsocks  -> { type, tag, server, server_port,
//	                  method, password }
//	trojan       -> { type, tag, server, server_port,
//	                  password, tls? }
//
// The `tag` is the per-endpoint display name: the
// host.DisplayName (or host.Remark as fallback) plus
// the inbound protocol — clients show this as the
// "server name" in their UI. Endpoints that fail to
// build a valid outbound are skipped (the same
// fail-soft policy as the base64 renderer).
//
// # TLS
//
// VLESS / Hysteria 2 / Trojan carry a `tls` block
// whenever the endpoint carries an SNI override or
// the inbound's params declare a TLS scheme. The TLS
// block is built from the endpoint / inbound params:
//
//   - server_name:   endpoint SNI[0] if set, else
//                    params.sni
//   - alpn:          params.alpn (array of strings) if
//                    set
//   - fingerprint:   params.fingerprint (chrome /
//                    firefox / edge / safari / ios /
//                    android) if set; the sing-box
//                    field is `utls.fingerprint`
//   - reality:       enabled when params.reality !=
//                    nil; carries public_key /
//                    short_id from the params map
//
// Shadowsocks deliberately omits the TLS block; the
// protocol does not negotiate TLS at the outbound
// layer (TLS, if any, is provided by an outer
// transport such as h2 / grpc — those land with the
// Phase 1 transport work).
//
// # Round-trip
//
// The output of RenderSingbox is meant to round-trip
// through the sing-box config loader: a remote
// subscription containing only the outbounds section
// is a valid sing-box config (the missing inbounds /
// route / dns sections are filled in by the client
// from its own template).
//
// # Phase 0 scope
//
//   - all four protocols (VLESS, HY2, Shadowsocks,
//     Trojan) produce valid outbounds.
//   - the TLS block covers server_name, alpn, utls,
//     and reality — the common case.
//   - transport (ws / grpc / h2) lands with the
//     Phase 1 transport work.
//   - XHTTP `download_settings` lands with the
//     Phase 1 multi-port work.

package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/QAdversif/AegisPanel/internal/inbounds"
)

// singboxOutbound is the per-endpoint map. The sing-box
// wire format is open-ended (any field is allowed),
// so we model it as a map[string]any. The keys
// chosen below match the sing-box 1.8 / 1.9 docs;
// clients on older versions ignore unknown fields.
type singboxOutbound = map[string]any

// singboxDoc is the top-level document. We marshal
// with a struct (rather than a map) so the keys
// are stable across refactors and so json.Marshal
// rejects unexpected types at compile time.
type singboxDoc struct {
	Outbounds []singboxOutbound `json:"outbounds"`
}

// RenderSingbox serialises `eps` as a sing-box
// outbounds JSON document. The result is suitable for
// serving from GET /api/v1/sub/<token>?target=singbox,
// or as the auto-detected response for Hiddify /
// NekoBox / sing-box CLI.
//
// Empty input returns a valid empty document
// (`{"outbounds": []}`) and a nil error. An empty
// subscription is a valid subscription.
func (s *Service) RenderSingbox(_ context.Context, u *User, eps []ResolvedEndpoint) ([]byte, error) {
	if len(eps) > 0 {
		// Apply format variables + wildcard salt
		// once, before the per-endpoint builder.
		// nil u is the test-friendly baseline.
		if rc := s.newRenderContext(u); rc != nil {
			enriched := make([]ResolvedEndpoint, len(eps))
			for i, ep := range eps {
				enriched[i] = enrichEndpoint(ep, rc)
			}
			eps = enriched
		}
	}
	doc := singboxDoc{Outbounds: make([]singboxOutbound, 0, len(eps))}
	for _, ep := range eps {
		out, err := renderSingboxOutbound(ep)
		if err != nil {
			// A single unrenderable endpoint
			// must not poison the whole
			// subscription. Skip; future PRs
			// can add a per-endpoint log.
			continue
		}
		doc.Outbounds = append(doc.Outbounds, out)
	}
	return json.Marshal(doc)
}

// renderSingboxOutbound is the per-protocol outbound
// builder. Unknown protocols return an error so the
// caller can skip the endpoint.
func renderSingboxOutbound(ep ResolvedEndpoint) (singboxOutbound, error) {
	address, port := effectiveAddress(ep)
	tag := displayName(ep.Host)
	switch ep.Inbound.Protocol {
	case inbounds.ProtocolVLESS:
		return buildSingboxVLESS(ep, address, port, tag), nil
	case inbounds.ProtocolHysteria2:
		return buildSingboxHysteria2(ep, address, port, tag), nil
	case inbounds.ProtocolShadowsocks:
		return buildSingboxShadowsocks(ep, address, port, tag), nil
	case inbounds.ProtocolTrojan:
		return buildSingboxTrojan(ep, address, port, tag), nil
	default:
		return nil, fmt.Errorf("unknown protocol: %s", ep.Inbound.Protocol)
	}
}

// --- per-protocol builders ----------------------------------------

// buildSingboxVLESS produces a sing-box VLESS outbound.
//
// Required inbound params:
//
//	uuid: string  (UUID for the user)
//
// Optional:
//
//	flow:        string  ("xtls-rprx-vision")
//	sni:         string  (server_name; overridden by
//	                     endpoint.SNI[0] if set)
//	fingerprint: string  (chrome / firefox / edge /
//	                     safari / ios / android)
//	alpn:        []string
//	reality:     map[string]any  { public_key, short_id }
func buildSingboxVLESS(ep ResolvedEndpoint, addr string, port int, tag string) singboxOutbound {
	uuidStr := paramString(ep.Inbound.Params, "uuid")
	out := singboxOutbound{
		"type":        "vless",
		"tag":         tag,
		"server":      addr,
		"server_port": port,
		"uuid":        uuidStr,
	}
	if flow := paramString(ep.Inbound.Params, "flow"); flow != "" {
		out["flow"] = flow
	}
	if tls := buildSingboxTLS(ep, "tls"); tls != nil {
		out["tls"] = tls
	}
	return out
}

// buildSingboxHysteria2 produces a sing-box Hysteria 2
// outbound.
//
// Required inbound params:
//
//	password: string
//
// Optional:
//
//	sni:         string
//	alpn:        []string
//	obfs:        map[string]any  (salamander obfuscation)
func buildSingboxHysteria2(ep ResolvedEndpoint, addr string, port int, tag string) singboxOutbound {
	password := paramString(ep.Inbound.Params, "password")
	out := singboxOutbound{
		"type":        "hysteria2",
		"tag":         tag,
		"server":      addr,
		"server_port": port,
		"password":    password,
	}
	if obfs := paramString(ep.Inbound.Params, "obfs_type"); obfs != "" {
		// sing-box 1.8+: obfs is a nested object with
		// type + password. The Phase 0 shape is the
		// minimum: type only. The password lands
		// with the Phase 1 obfs work.
		out["obfs"] = map[string]any{"type": obfs}
	}
	if tls := buildSingboxTLS(ep, "tls"); tls != nil {
		out["tls"] = tls
	}
	return out
}

// buildSingboxShadowsocks produces a sing-box
// Shadowsocks outbound. The method defaults to
// chacha20-ietf-poly1305 (the modern AEAD cipher)
// when the inbound params do not specify one.
func buildSingboxShadowsocks(ep ResolvedEndpoint, addr string, port int, tag string) singboxOutbound {
	method := paramStringOr(ep.Inbound.Params, "method", "chacha20-ietf-poly1305")
	password := paramString(ep.Inbound.Params, "password")
	return singboxOutbound{
		"type":        "shadowsocks",
		"tag":         tag,
		"server":      addr,
		"server_port": port,
		"method":      method,
		"password":    password,
	}
}

// buildSingboxTrojan produces a sing-box Trojan
// outbound.
func buildSingboxTrojan(ep ResolvedEndpoint, addr string, port int, tag string) singboxOutbound {
	password := paramString(ep.Inbound.Params, "password")
	out := singboxOutbound{
		"type":        "trojan",
		"tag":         tag,
		"server":      addr,
		"server_port": port,
		"password":    password,
	}
	if tls := buildSingboxTLS(ep, "tls"); tls != nil {
		out["tls"] = tls
	}
	return out
}

// --- TLS block builder --------------------------------------------

// buildSingboxTLS assembles the optional `tls` block.
// Returns nil when no TLS-relevant field is set (no
// SNI, no alpn, no fingerprint, no reality) so the
// caller can simply skip the field on the parent
// outbound — sing-box interprets "no tls" as
// "no TLS layer on this outbound".
//
// `key` is the JSON field name on the parent
// outbound (currently always "tls"; reserved for
// future tls_fragment / tls_tricks variants).
func buildSingboxTLS(ep ResolvedEndpoint, _ string) map[string]any {
	tls := make(map[string]any)
	tls["enabled"] = true
	// server_name: endpoint.SNI[0] wins over params.sni.
	if len(ep.Endpoint.SNI) > 0 {
		tls["server_name"] = ep.Endpoint.SNI[0]
	} else if sni := paramString(ep.Inbound.Params, "sni"); sni != "" {
		tls["server_name"] = sni
	}
	// alpn: pass through if present.
	if alpn := alpnFromParams(ep.Inbound.Params); len(alpn) > 0 {
		tls["alpn"] = alpn
	}
	// utls: only set when the inbound declares a
	// fingerprint. The sing-box field is
	// `utls: { enabled, fingerprint }` — both required
	// when present.
	if fp := paramString(ep.Inbound.Params, "fingerprint"); fp != "" {
		tls["utls"] = map[string]any{
			"enabled":     true,
			"fingerprint": fp,
		}
	}
	// reality: enabled when params.reality != nil. The
	// shape is `reality: { enabled, public_key,
	// short_id }` per the sing-box 1.8 docs.
	if reality := paramMap(ep.Inbound.Params, "reality"); reality != nil {
		r := map[string]any{"enabled": true}
		if pk := paramString(reality, "public_key"); pk != "" {
			r["public_key"] = pk
		}
		if sid := paramString(reality, "short_id"); sid != "" {
			r["short_id"] = sid
		}
		tls["reality"] = r
	}
	// If the block is just {"enabled": true} with no
	// other field, drop the enabled key — sing-box
	// tolerates the empty block, but the operator
	// reading the JSON does not need it.
	if len(tls) == 1 {
		return nil
	}
	return tls
}

// --- param helpers -----------------------------------------------

// alpnFromParams reads the inbound's `params.alpn`
// value as a []string. The Phase 0 renderer reads
// only the alpn slice — every other "list of
// strings" field in the inbound params is a single
// value, not a list. When a future renderer needs a
// second list (e.g. server_names in XHTTP), split
// the helper per-field rather than generalising
// here; the unparam linter would flag a generic
// "read any []string by key" function as "always
// called with the same key".
func alpnFromParams(params map[string]any) []string {
	if params == nil {
		return nil
	}
	v, ok := params["alpn"]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		// Also accept []string directly — the
		// inbound's params map can carry either
		// representation depending on how the
		// Service layer marshalled it.
		if ss, ok2 := v.([]string); ok2 {
			return ss
		}
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		s, _ := x.(string)
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// paramMap reads a nested map[string]any from the
// inbound's `params` map. Returns nil if the key is
// missing or not a map. Used for the reality block.
func paramMap(params map[string]any, key string) map[string]any {
	if params == nil {
		return nil
	}
	v, ok := params[key]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

// _ keeps the `errors` import in the symbol table for
// future branches that return an error from a
// per-protocol builder. None of the current
// builders return an error; the switch in
// renderSingboxOutbound covers the unknown-protocol
// case.
var _ = errors.New
