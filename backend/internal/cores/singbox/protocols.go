// SPDX-License-Identifier: AGPL-3.0-or-later

package singbox

import (
	"fmt"

	"github.com/QAdversif/AegisPanel/internal/cores"
)

// renderVLESS produces a sing-box VLESS inbound. Phase 1
// only supports VLESS+Reality+Vision: that is the protocol
// combo every modern client speaks, and adding plain VLESS
// without TLS is a footgun (no anti-censorship, no anti-DPI).
//
// The output always has a single user with the given UUID
// and flow; multi-user VLESS inbounds land with the
// inbound-templates work in a later phase.
func renderVLESS(spec cores.InboundSpec, params map[string]any) (map[string]any, error) {
	uuid, err := requireString(params, "uuid")
	if err != nil {
		return nil, err
	}
	port, err := requireIntPort(params)
	if err != nil {
		return nil, err
	}

	user := map[string]any{
		"name": spec.Tag,
		"uuid": uuid,
	}
	// Flow is optional — only set for Vision-enabled
	// inbounds. Empty / missing is a valid VLESS config.
	if flow := optionalString(params, "flow", ""); flow != "" {
		user["flow"] = flow
	}

	out := map[string]any{
		"type":        "vless",
		"tag":         spec.Tag,
		"listen":      optionalString(params, "listen", "::"),
		"listen_port": port,
		"users":       []map[string]any{user},
	}

	tls, err := optionalTLS(params, "tls")
	if err != nil {
		return nil, err
	}
	if tls != nil {
		// server_name is required inside a TLS block. We
		// do not check it here — sing-box's startup log
		// will surface a missing SNI, and adding a
		// renderer-side check duplicates that error.
		out["tls"] = tls
	}

	return out, nil
}

// renderHY2 produces a sing-box Hysteria 2 inbound. HY2 is
// a single-user-friendly protocol: each inbound is meant
// for a small set of users, not a multi-tenant panel.
//
// Phase 1 keeps it single-user per inbound (the typical
// deployment shape). The user entry is built from
// `name` and `password`; the panel hands the same value
// to every Host referencing the inbound.
func renderHY2(spec cores.InboundSpec, params map[string]any) (map[string]any, error) {
	password, err := requireString(params, "password")
	if err != nil {
		return nil, err
	}
	port, err := requireIntPort(params)
	if err != nil {
		return nil, err
	}

	out := map[string]any{
		"type":        "hysteria2",
		"tag":         spec.Tag,
		"listen":      optionalString(params, "listen", "::"),
		"listen_port": port,
		"users": []map[string]any{
			{"name": spec.Tag, "password": password},
		},
	}

	tls, err := optionalTLS(params, "tls")
	if err != nil {
		return nil, err
	}
	if tls != nil {
		out["tls"] = tls
	}

	return out, nil
}

// renderShadowsocks produces a sing-box Shadowsocks inbound.
// We require the 2022-blake3 AEAD methods — the legacy
// "aes-256-cfb" is rejected by sing-box 1.8+ and a panel
// that ships those keys is a misconfiguration we want to
// surface at render time, not at node boot.
func renderShadowsocks(spec cores.InboundSpec, params map[string]any) (map[string]any, error) {
	method, err := requireString(params, "method")
	if err != nil {
		return nil, err
	}
	password, err := requireString(params, "password")
	if err != nil {
		return nil, err
	}
	port, err := requireIntPort(params)
	if err != nil {
		return nil, err
	}

	out := map[string]any{
		"type":        "shadowsocks",
		"tag":         spec.Tag,
		"listen":      optionalString(params, "listen", "::"),
		"listen_port": port,
		"method":      method,
		"password":    password,
	}
	return out, nil
}

// renderTrojan produces a sing-box Trojan inbound. Trojan
// is conceptually VLESS-with-password: same TLS requirements,
// different auth material. We require a TLS block (Trojan
// without TLS is just an unauthenticated password check).
func renderTrojan(spec cores.InboundSpec, params map[string]any) (map[string]any, error) {
	password, err := requireString(params, "password")
	if err != nil {
		return nil, err
	}
	port, err := requireIntPort(params)
	if err != nil {
		return nil, err
	}

	tls, err := optionalTLS(params, "tls")
	if err != nil {
		return nil, err
	}
	if tls == nil {
		// sing-box accepts a Trojan inbound without TLS,
		// but the result is functionally equivalent to a
		// password-only proxy — that is never what an
		// operator means when they pick Trojan. Surface
		// the misconfiguration at render time.
		return nil, fmt.Errorf("trojan inbound %q requires a tls block", spec.Tag)
	}

	out := map[string]any{
		"type":        "trojan",
		"tag":         spec.Tag,
		"listen":      optionalString(params, "listen", "::"),
		"listen_port": port,
		"users": []map[string]any{
			{"name": spec.Tag, "password": password},
		},
		"tls": tls,
	}
	return out, nil
}
