// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Clash (Clash Meta / mihomo) renderer. The wire
// format is the Clash proxy list YAML — a subset of a
// full Clash config, sufficient to be imported as a
// remote subscription by Clash Verge, Clash Meta for
// Android, and other mihomo-family clients.
//
// # Wire format
//
// The top-level document is a YAML mapping with the
// single `proxies` key:
//
//	proxies:
//	  - name: "..."
//	    type: vless
//	    server: 1.2.3.4
//	    port: 443
//	    uuid: 00000000-0000-0000-0000-000000000aaa
//	    ...
//	  - name: "..."
//	    type: hysteria2
//	    ...
//
// We do NOT emit `proxy-groups` or `rules` — those are
// a per-client policy concern, and the operator's
// subscription is not the right place to inject
// policy. Clients merge this `proxies` list into
// their own template and apply the user-defined
// groups / rules there.
//
// # Per-protocol field map
//
//	vless        -> name, type, server, port, uuid,
//	                flow?, tls?, sni?, fingerprint?,
//	                alpn?, client-fingerprint?
//	hysteria2    -> name, type, server, port, password,
//	                tls?, sni?, alpn?, obfs?
//	shadowsocks  -> name, type, server, port, cipher,
//	                password
//	trojan       -> name, type, server, port, password,
//	                sni?, alpn?, fingerprint?, tls?,
//	                skip-cert-verify
//
// # TLS
//
// The Clash `tls: true` flag is the simple on/off
// switch. The full TLS block (sni / alpn /
// fingerprint / reality) goes inline as
// `sni:`, `alpn:`, `fingerprint:` at the proxy root,
// not in a nested `tls:` block (Clash YAML schema
// differs from sing-box here — sing-box nests
// everything under `tls:`, Clash inlines the most
// common fields).
//
// # Phase 0 scope
//
//   - all four protocols (VLESS, HY2, Shadowsocks,
//     Trojan) produce valid proxy entries.
//   - the common TLS / SNI / alpn / fingerprint
//     fields are wired.
//   - transport (ws / grpc / h2) lands with the
//     Phase 1 transport work.
//   - the new `proxy-groups` and `rules` policy
//     sections land with the Phase 1 panel policy
//     work — they are not part of the subscription
//     model today (per ARCHITECTURE.md §10.2: groups
//     are a per-pool concern, not a per-subscription
//     one).

package subscription

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/QAdversif/AegisPanel/internal/inbounds"
)

// clashProxy is the per-endpoint map. The Clash
// wire format is open-ended (any field is allowed),
// so we model it as a map[string]any. The keys
// chosen below match the Clash Meta / mihomo docs;
// older Clash clients ignore unknown fields.
type clashProxy = map[string]any

// clashDoc is the top-level document. We marshal
// with a struct (rather than a map) so the keys
// are stable across refactors and so yaml.Marshal
// emits them in the canonical order.
type clashDoc struct {
	Proxies []clashProxy `yaml:"proxies"`
}

// RenderClash serialises `eps` as a Clash proxy list
// YAML document. The result is suitable for serving
// from GET /api/v1/sub/<token>?target=clash, or as
// the auto-detected response for Clash Verge /
// Clash Meta for Android / mihomo.
//
// Empty input returns a valid empty document
// (`proxies: []`) and a nil error.
func (s *Service) RenderClash(_ context.Context, _ *User, eps []ResolvedEndpoint) ([]byte, error) {
	doc := clashDoc{Proxies: make([]clashProxy, 0, len(eps))}
	for _, ep := range eps {
		proxy, err := renderClashProxy(ep)
		if err != nil {
			// A single unrenderable endpoint
			// must not poison the whole
			// subscription. Skip; future PRs
			// can add a per-endpoint log.
			continue
		}
		doc.Proxies = append(doc.Proxies, proxy)
	}
	return yaml.Marshal(doc)
}

// renderClashProxy is the per-protocol proxy builder.
// Unknown protocols return an error so the caller
// can skip the endpoint.
func renderClashProxy(ep ResolvedEndpoint) (clashProxy, error) {
	address, port := effectiveAddress(ep)
	tag := displayName(ep.Host)
	switch ep.Inbound.Protocol {
	case inbounds.ProtocolVLESS:
		return buildClashVLESS(ep, address, port, tag), nil
	case inbounds.ProtocolHysteria2:
		return buildClashHysteria2(ep, address, port, tag), nil
	case inbounds.ProtocolShadowsocks:
		return buildClashShadowsocks(ep, address, port, tag), nil
	case inbounds.ProtocolTrojan:
		return buildClashTrojan(ep, address, port, tag), nil
	default:
		return nil, fmt.Errorf("unknown protocol: %s", ep.Inbound.Protocol)
	}
}

// --- per-protocol builders ----------------------------------------

// buildClashVLESS produces a Clash VLESS proxy entry.
//
// Required inbound params:
//
//	uuid: string  (UUID for the user)
//
// Optional:
//
//	flow:         string  ("xtls-rprx-vision")
//	sni:          string  (server_name; overridden by
//	                      endpoint.SNI[0] if set)
//	fingerprint:  string  (chrome / firefox / edge /
//	                      safari / ios / android)
//	alpn:         []string
//	reality:      map[string]any  { public_key, short_id }
//
// The `client-fingerprint` field is the Clash Meta
// equivalent of sing-box's `utls.fingerprint`. They
// share the same set of values.
func buildClashVLESS(ep ResolvedEndpoint, addr string, port int, tag string) clashProxy {
	uuidStr := paramString(ep.Inbound.Params, "uuid")
	out := clashProxy{
		"name":   tag,
		"type":   "vless",
		"server": addr,
		"port":   port,
		"uuid":   uuidStr,
	}
	if flow := paramString(ep.Inbound.Params, "flow"); flow != "" {
		out["flow"] = flow
	}
	// SNI: endpoint override wins over params.
	sni := ""
	if len(ep.Endpoint.SNI) > 0 {
		sni = ep.Endpoint.SNI[0]
	} else if s := paramString(ep.Inbound.Params, "sni"); s != "" {
		sni = s
	}
	if sni != "" {
		out["sni"] = sni
		out["tls"] = true
	}
	// alpn: emit only when set; otherwise omit.
	if alpn := paramStringSlice(ep.Inbound.Params, "alpn"); len(alpn) > 0 {
		out["alpn"] = alpn
	}
	// fingerprint: emit as `client-fingerprint` (Clash
	// Meta convention). Also sets `fingerprint` for
	// older Clash clients.
	if fp := paramString(ep.Inbound.Params, "fingerprint"); fp != "" {
		out["client-fingerprint"] = fp
		out["fingerprint"] = fp
	}
	// reality: the Clash Meta `reality-opts` sub-block
	// carries public_key + short_id. Older Clash
	// clients ignore this and use the rest of the
	// fields normally.
	if reality := paramMap(ep.Inbound.Params, "reality"); reality != nil {
		opts := make(map[string]any)
		if pk := paramString(reality, "public_key"); pk != "" {
			opts["public-key"] = pk
		}
		if sid := paramString(reality, "short_id"); sid != "" {
			opts["short-id"] = sid
		}
		if len(opts) > 0 {
			out["reality-opts"] = opts
		}
	}
	return out
}

// buildClashHysteria2 produces a Clash Hysteria 2
// proxy entry.
//
// Required inbound params:
//
//	password: string
//
// Optional:
//
//	sni:         string
//	alpn:        []string
//	obfs_type:   string  (salamander obfuscation)
func buildClashHysteria2(ep ResolvedEndpoint, addr string, port int, tag string) clashProxy {
	password := paramString(ep.Inbound.Params, "password")
	out := clashProxy{
		"name":     tag,
		"type":     "hysteria2",
		"server":   addr,
		"port":     port,
		"password": password,
	}
	sni := ""
	if len(ep.Endpoint.SNI) > 0 {
		sni = ep.Endpoint.SNI[0]
	} else if s := paramString(ep.Inbound.Params, "sni"); s != "" {
		sni = s
	}
	if sni != "" {
		out["sni"] = sni
		out["tls"] = true
	}
	if alpn := paramStringSlice(ep.Inbound.Params, "alpn"); len(alpn) > 0 {
		out["alpn"] = alpn
	}
	if obfs := paramString(ep.Inbound.Params, "obfs_type"); obfs != "" {
		out["obfs"] = obfs
	}
	return out
}

// buildClashShadowsocks produces a Clash Shadowsocks
// proxy entry. The cipher defaults to
// chacha20-ietf-poly1305 when the inbound params do
// not specify one.
//
// The Clash field is `cipher`; the sing-box field
// is `method`. The Phase 0 renderer does not
// translate between the two — the operator's
// inbound.params.method is the canonical name and
// the sing-box / Clash / base64 renderers each read
// the same key.
func buildClashShadowsocks(ep ResolvedEndpoint, addr string, port int, tag string) clashProxy {
	cipher := paramStringOr(ep.Inbound.Params, "method", "chacha20-ietf-poly1305")
	password := paramString(ep.Inbound.Params, "password")
	return clashProxy{
		"name":     tag,
		"type":     "ss",
		"server":   addr,
		"port":     port,
		"cipher":   cipher,
		"password": password,
	}
}

// buildClashTrojan produces a Clash Trojan proxy
// entry. Trojan requires TLS — we always set
// `tls: true` (the operator can opt out via the
// params if needed, but the Phase 0 default is the
// safe one).
func buildClashTrojan(ep ResolvedEndpoint, addr string, port int, tag string) clashProxy {
	password := paramString(ep.Inbound.Params, "password")
	out := clashProxy{
		"name":     tag,
		"type":     "trojan",
		"server":   addr,
		"port":     port,
		"password": password,
		"tls":      true,
	}
	sni := ""
	if len(ep.Endpoint.SNI) > 0 {
		sni = ep.Endpoint.SNI[0]
	} else if s := paramString(ep.Inbound.Params, "sni"); s != "" {
		sni = s
	}
	if sni != "" {
		out["sni"] = sni
	}
	if alpn := paramStringSlice(ep.Inbound.Params, "alpn"); len(alpn) > 0 {
		out["alpn"] = alpn
	}
	if fp := paramString(ep.Inbound.Params, "fingerprint"); fp != "" {
		out["client-fingerprint"] = fp
		out["fingerprint"] = fp
	}
	// Trojan's `skip-cert-verify` defaults to false;
	// the operator can opt in via the params.
	if skv, _ := ep.Inbound.Params["skip_cert_verify"].(bool); skv {
		out["skip-cert-verify"] = true
	}
	return out
}
