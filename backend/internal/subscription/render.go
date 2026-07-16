// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Render turns a slice of ResolvedEndpoint into one of
// the supported wire formats. Phase 0 ships a single
// format: base64. The single-format design keeps the
// public surface minimal and the diff to add the next
// format (sing-box, Clash) small.
//
// # base64
//
// Per ARCHITECTURE.md §2.4 / §10.4, base64 is the
// fallback format for clients that do not understand
// sing-box / Clash (v2rayNG, Shadowrocket, v2rayN).
// The wire format is:
//
//   - one URI per line, separated by '\n';
//   - the joined string base64-encoded with the
//     standard alphabet (no URL-safe variant — that is
//     what the clients expect).
//
// Each URI is a per-protocol scheme:
//
//   - vless://UUID@host:port?params#name
//   - hysteria2://password@host:port?params#name
//   - shadowsocks://base64(method:password)@host:port#name
//   - trojan://password@host:port?params#name
//
// Phase 0 is intentionally minimal: the per-protocol
// renderer honours the address / port / SNI / host /
// path overrides on the endpoint, the inbound
// `params` map, and the node address. Reality keys,
// fingerprint, transport-specific knobs, and
// download_settings land with the Phase 1 format work.

package subscription

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
)

// Format is the wire format the renderer targets. New
// formats land as additional constants.
type Format string

// Supported formats. Phase 0 ships FormatBase64 and
// FormatHTML (the minimal landing page); FormatSingbox
// and FormatClash are placeholders for future PRs.
const (
	FormatBase64  Format = "base64"
	FormatSingbox Format = "singbox"
	FormatClash   Format = "clash"
	FormatHTML    Format = "html"
)

// ErrUnknownFormat is returned by RenderBase64 when the
// caller asks for a format that is not yet
// implemented.
var ErrUnknownFormat = errors.New("subscription: unknown format")

// RenderBase64 serialises `eps` as a base64-encoded
// blob of one URI per line, per the base64 format
// contract above. The result is suitable for serving
// from GET /s3cr3t-sub-<token>?target=base64 (or
// without `target`, with the format defaulted to
// base64 by the auto-detect middleware).
//
// The function is a method on Service rather than a
// free function because it benefits from the same
// store / clock wiring (e.g. for a future "include
// last-fetch timestamp" header).
//
// Empty input returns an empty (non-nil) result and a
// nil error; an empty subscription is a valid
// subscription (the user is entitled to no hosts, so
// we serve them no hosts).
func (s *Service) RenderBase64(ctx context.Context, u *User, eps []ResolvedEndpoint) (out []byte, err error) {
	_ = ctx // future: cache reads may consult ctx
	_ = u   // future: format variables and headers will
	if len(eps) == 0 {
		return []byte{}, nil
	}
	// Deterministic order: priority is already on the
	// host; stable secondary by (host ID, endpoint ID)
	// so the byte stream is reproducible across
	// requests.
	sort.SliceStable(eps, func(i, j int) bool {
		if eps[i].Host.ID != eps[j].Host.ID {
			return eps[i].Host.ID.String() < eps[j].Host.ID.String()
		}
		return eps[i].Endpoint.ID.String() < eps[j].Endpoint.ID.String()
	})
	var lines []string
	for _, ep := range eps {
		uri, err := renderEndpointURI(ep)
		if err != nil {
			// A single unrenderable endpoint
			// should not poison the whole
			// subscription. Skip it; future PRs
			// can add a per-URI error log.
			continue
		}
		lines = append(lines, uri)
	}
	if len(lines) == 0 {
		return []byte{}, nil
	}
	joined := strings.Join(lines, "\n")
	out = make([]byte, base64.StdEncoding.EncodedLen(len(joined)))
	base64.StdEncoding.Encode(out, []byte(joined))
	return out, nil
}

// renderEndpointURI is the per-protocol URI builder.
// Unknown protocols are returned as errors so the
// caller (RenderBase64) can skip them.
func renderEndpointURI(ep ResolvedEndpoint) (uri string, err error) {
	displayName := displayName(ep.Host)
	address, port := effectiveAddress(ep)
	switch ep.Inbound.Protocol {
	case inbounds.ProtocolVLESS:
		return renderVLESS(ep, address, port, displayName)
	case inbounds.ProtocolHysteria2:
		return renderHysteria2(ep, address, port, displayName)
	case inbounds.ProtocolShadowsocks:
		return renderShadowsocks(ep, address, port, displayName)
	case inbounds.ProtocolTrojan:
		return renderTrojan(ep, address, port, displayName)
	default:
		return "", fmt.Errorf("unknown protocol: %s", ep.Inbound.Protocol)
	}
}

// effectiveAddress returns the address and port that
// the URI should advertise. Endpoint-level overrides
// win over the node + inbound defaults.
func effectiveAddress(ep ResolvedEndpoint) (addr string, port int) {
	addr = ep.Node.Address
	if len(ep.Endpoint.Address) > 0 {
		addr = ep.Endpoint.Address[0]
	}
	port = ep.Inbound.ListenPort
	if ep.Endpoint.Port != nil {
		port = *ep.Endpoint.Port
	}
	return addr, port
}

// displayName is the fragment / "name" part of the
// URI. Endpoint-level (none in v3) wins over
// host.DisplayName wins over host.Remark.
func displayName(h *hosts.Host) string {
	if h.DisplayName != "" {
		return h.DisplayName
	}
	return h.Remark
}

// --- per-protocol renderers --------------------------------

func renderVLESS(ep ResolvedEndpoint, addr string, port int, name string) (uri string, err error) {
	uuidStr := paramString(ep.Inbound.Params, "uuid")
	if uuidStr == "" {
		return "", errors.New("vless: missing params.uuid")
	}
	q := url.Values{}
	if flow := paramString(ep.Inbound.Params, "flow"); flow != "" {
		q.Set("flow", flow)
	}
	q.Set("encryption", paramStringOr(ep.Inbound.Params, "encryption", "none"))
	if len(ep.Endpoint.SNI) > 0 {
		q.Set("sni", ep.Endpoint.SNI[0])
	}
	if fp := paramString(ep.Inbound.Params, "fingerprint"); fp != "" {
		q.Set("fp", fp)
	}
	if pbk := paramString(ep.Inbound.Params, "public_key"); pbk != "" {
		q.Set("pbk", pbk)
	}
	if sid := paramString(ep.Inbound.Params, "short_id"); sid != "" {
		q.Set("sid", sid)
	}
	if transport := paramString(ep.Inbound.Params, "transport"); transport != "" {
		q.Set("type", transport)
	}
	if len(ep.Endpoint.Host) > 0 {
		q.Set("host", ep.Endpoint.Host[0])
	}
	if ep.Endpoint.Path != "" {
		q.Set("path", ep.Endpoint.Path)
	}
	return "vless://" + uuidStr + "@" + addr + ":" + itoa(port) + "?" + q.Encode() + "#" + url.PathEscape(name), nil
}

func renderHysteria2(ep ResolvedEndpoint, addr string, port int, name string) (uri string, err error) {
	password := paramString(ep.Inbound.Params, "password")
	if password == "" {
		return "", errors.New("hysteria2: missing params.password")
	}
	q := url.Values{}
	if len(ep.Endpoint.SNI) > 0 {
		q.Set("sni", ep.Endpoint.SNI[0])
	}
	if obfs := paramString(ep.Inbound.Params, "obfs"); obfs != "" {
		q.Set("obfs", obfs)
	}
	return "hysteria2://" + url.PathEscape(password) + "@" + addr + ":" + itoa(port) + "?" + q.Encode() + "#" + url.PathEscape(name), nil
}

func renderShadowsocks(ep ResolvedEndpoint, addr string, port int, name string) (uri string, err error) {
	method := paramStringOr(ep.Inbound.Params, "method", "chacha20-ietf-poly1305")
	password := paramString(ep.Inbound.Params, "password")
	if password == "" {
		return "", errors.New("shadowsocks: missing params.password")
	}
	// SS uses SIP002 userinfo: base64(method:password)
	// without padding stripped. The trailing "=" is
	// allowed by every mainstream client.
	userinfo := base64.StdEncoding.EncodeToString([]byte(method + ":" + password))
	return "ss://" + userinfo + "@" + addr + ":" + itoa(port) + "#" + url.PathEscape(name), nil
}

func renderTrojan(ep ResolvedEndpoint, addr string, port int, name string) (uri string, err error) {
	password := paramString(ep.Inbound.Params, "password")
	if password == "" {
		return "", errors.New("trojan: missing params.password")
	}
	q := url.Values{}
	q.Set("allowInsecure", "0")
	if len(ep.Endpoint.SNI) > 0 {
		q.Set("sni", ep.Endpoint.SNI[0])
	}
	return "trojan://" + url.PathEscape(password) + "@" + addr + ":" + itoa(port) + "?" + q.Encode() + "#" + url.PathEscape(name), nil
}

// --- param helpers ----------------------------------------

// paramString reads a string value from the inbound's
// `params` map. The map is `map[string]any` per the
// inbound model; we accept any value and string-ify it.
// Empty / missing values return "".
func paramString(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	v, ok := params[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// paramStringOr is paramString with a default value
// when the key is missing or empty.
func paramStringOr(params map[string]any, key, def string) string {
	if v := paramString(params, key); v != "" {
		return v
	}
	return def
}

// itoa is a tiny non-allocating int-to-string for port
// numbers. We never need i18n or a custom base.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
