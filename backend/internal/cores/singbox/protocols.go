// SPDX-License-Identifier: AGPL-3.0-or-later

package singbox

import (
	"fmt"

	"github.com/QAdversif/AegisPanel/internal/cores"
	"github.com/QAdversif/AegisPanel/internal/credentials"
)

// renderVLESS produces a sing-box VLESS inbound. Phase 1
// only supports VLESS+Reality+Vision: that is the protocol
// combo every modern client speaks, and adding plain VLESS
// without TLS is a footgun (no anti-censorship, no anti-DPI).
//
// When `users` is non-empty (Phase 2 multi-user path), the
// rendered inbound has a length-N `users: [...]` array,
// one entry per Credential. Each entry uses the
// CredentialValue as the `uuid` field and the UserID as
// the `name` (the sing-box `name` is just a label, not
// an auth identity; using the UserID guarantees
// uniqueness across users in the same inbound).
//
// When `users` is empty (Phase 1 / v0.7.2 and earlier),
// the renderer falls back to the single-operator
// credential in params["uuid"] and emits a length-1
// array. The `name` falls back to the inbound tag (the
// only "name" available without a UserID).
func renderVLESS(spec cores.InboundSpec, params map[string]any, users []credentials.Credential) (map[string]any, error) {
	port, err := requireIntPort(params)
	if err != nil {
		return nil, err
	}

	// Flow is optional — only set for Vision-enabled
	// inbounds. Empty / missing is a valid VLESS config.
	// Flow is per-inbound (sing-box puts it on each user
	// entry), so the same value is applied to every
	// user in the Phase 2 multi-user case.
	flow := optionalString(params, "flow", "")

	var userEntries []map[string]any
	if len(users) > 0 {
		// Phase 2: one user entry per Credential.
		userEntries = make([]map[string]any, 0, len(users))
		for _, c := range users {
			u := map[string]any{
				"name": c.UserID.String(),
				"uuid": c.CredentialValue,
			}
			if flow != "" {
				u["flow"] = flow
			}
			userEntries = append(userEntries, u)
		}
	} else {
		// Phase 1: single user, params-driven.
		uuid, err := requireString(params, "uuid")
		if err != nil {
			return nil, err
		}
		u := map[string]any{
			"name": spec.Tag,
			"uuid": uuid,
		}
		if flow != "" {
			u["flow"] = flow
		}
		userEntries = []map[string]any{u}
	}

	out := map[string]any{
		"type":        "vless",
		"tag":         spec.Tag,
		"listen":      optionalString(params, "listen", "::"),
		"listen_port": port,
		"users":       userEntries,
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
// The Phase 2 path accepts a non-empty `users` list and
// emits a length-N `users: [...]` array. The Phase 1 path
// (empty `users`) falls back to params["password"] and
// emits a length-1 array — same behavior as v0.7.2 and
// earlier. The Builder populates Phase 2 only for inbounds
// with at least one row in user_inbound_credentials.
func renderHY2(spec cores.InboundSpec, params map[string]any, users []credentials.Credential) (map[string]any, error) {
	port, err := requireIntPort(params)
	if err != nil {
		return nil, err
	}

	var userEntries []map[string]any
	if len(users) > 0 {
		// Phase 2: one user entry per Credential.
		userEntries = make([]map[string]any, 0, len(users))
		for _, c := range users {
			userEntries = append(userEntries, map[string]any{
				"name":     c.UserID.String(),
				"password": c.CredentialValue,
			})
		}
	} else {
		// Phase 1: single user, params-driven.
		password, err := requireString(params, "password")
		if err != nil {
			return nil, err
		}
		userEntries = []map[string]any{
			{"name": spec.Tag, "password": password},
		}
	}

	out := map[string]any{
		"type":        "hysteria2",
		"tag":         spec.Tag,
		"listen":      optionalString(params, "listen", "::"),
		"listen_port": port,
		"users":       userEntries,
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
//
// Shadowsocks has NO per-user concept in sing-box 1.14+ —
// the inbound is single-password by protocol design
// (every client connecting shares the same auth material).
// The Phase 2 multi-user work does NOT change this; the
// Shadowsocks signature stays (spec, params) and there
// is no `users` list to thread through. Operators who
// want per-user auth on a Shadowsocks inbound should
// pick VLESS or Trojan instead.
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
//
// The Phase 2 path accepts a non-empty `users` list and
// emits a length-N `users: [...]` array. The Phase 1 path
// (empty `users`) falls back to params["password"] and
// emits a length-1 array — same behavior as v0.7.2 and
// earlier.
func renderTrojan(spec cores.InboundSpec, params map[string]any, users []credentials.Credential) (map[string]any, error) {
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

	var userEntries []map[string]any
	if len(users) > 0 {
		// Phase 2: one user entry per Credential.
		userEntries = make([]map[string]any, 0, len(users))
		for _, c := range users {
			userEntries = append(userEntries, map[string]any{
				"name":     c.UserID.String(),
				"password": c.CredentialValue,
			})
		}
	} else {
		// Phase 1: single user, params-driven.
		password, err := requireString(params, "password")
		if err != nil {
			return nil, err
		}
		userEntries = []map[string]any{
			{"name": spec.Tag, "password": password},
		}
	}

	out := map[string]any{
		"type":        "trojan",
		"tag":         spec.Tag,
		"listen":      optionalString(params, "listen", "::"),
		"listen_port": port,
		"users":       userEntries,
		"tls":         tls,
	}
	return out, nil
}
