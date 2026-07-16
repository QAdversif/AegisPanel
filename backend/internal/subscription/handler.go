// SPDX-License-Identifier: AGPL-3.0-or-later
//
// HTTP handler for the subscription package. The public
// surface is a single GET endpoint:
//
//	GET /sub/{token}
//
// where `{token}` is the user's `sub_token` (UNIQUE in
// migration 0001). The response body depends on the
// requested format:
//
//   - ?target=base64  (default if no signal says otherwise)
//   - ?target=singbox — not implemented in Phase 0,
//     returns 501
//   - ?target=clash   — not implemented in Phase 0,
//     returns 501
//   - ?target=html    — minimal HTML page in Phase 0,
//     real sub-page (with QR code) lands in a later PR
//
// When the caller does not pass `?target=`, the handler
// auto-detects from the Accept header and the
// User-Agent, per ARCHITECTURE.md §10.4:
//
//   - Accept: application/yaml        -> clash
//   - Accept: application/json        -> singbox
//   - User-Agent: clash/mihomo        -> clash
//   - User-Agent: sing-box/hiddify/
//                nekobox/karing/
//                streisand            -> singbox
//   - anything else                   -> base64
//
// The endpoint is mounted under /api/v1/sub/{token} by
// the router, and is unauthenticated by design — the
// sub_token IS the credential. A future PR will add
// rate limiting and a sub-token rotation path; for
// now, an unknown token returns 404, a live user with
// no entitled hosts returns 200 with an empty body,
// and a non-live user returns 403.
//
// # Headers
//
// On success, the response includes three of the
// standard subscription headers (per the sing-box /
// Clash / Shadowrocket convention):
//
//   - Profile-Title:       "AegisPanel"
//   - Profile-Update-Interval: "<hours>h" — hint to
//     clients that re-fetching more often is wasted
//     bandwidth; the value is a Phase 0 default (24h)
//     and lands as a config knob in a later PR.
//   - Subscription-Userinfo: "upload=N; download=N;
//     total=N; expire=UNIX" — populated from the user's
//     traffic counters and expire_at, with missing
//     values emitted as 0. Clients that understand the
//     header show the numbers in their UI.
//
// Content-Type is set per format:
//   - text/plain; charset=utf-8   for base64
//   - application/json; ...       for singbox (Phase 1)
//   - text/yaml; charset=utf-8     for clash (Phase 1)
//   - text/html; charset=utf-8     for html
//
// # Phase 0 scope
//
//   - base64 (implemented)
//   - auto-detect (implemented)
//   - html (minimal landing page, no QR yet)
//   - sing-box / clash (501 Not Implemented with a
//     descriptive error)
//
// The router-level sub-path rotation (e.g.
// `/s3cr3t-sub-<hex>/<token>`) lives in a separate
// router mount in `internal/router/router.go` — the
// actual sub-path table (`panel_path_config`) is part
// of a later PR that adds the PgStore path rotation.

package subscription

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
)

// Handler is the HTTP entry point. It wraps a Service
// and exposes a chi subrouter via Router().
type Handler struct {
	svc *Service
}

// NewHandler wires a Handler around the given service.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Router returns a chi subrouter for the subscription
// surface:
//
//	r.Mount("/api/v1/sub", subscription.Router(svc))
//
// The returned router has no auth middleware — the
// sub_token in the URL is the credential. Rate limiting
// is a TODO for the Phase 1 hardening pass.
func Router(svc *Service) http.Handler {
	h := NewHandler(svc)
	r := chi.NewRouter()
	r.Get("/{token}", h.handleRender)
	return r
}

// --- core handler ----------------------------------------------------

// handleRender is the single GET endpoint. The wire
// format is determined by `?target=` first, then by
// Accept / User-Agent auto-detect, then falls back to
// base64. Errors map to HTTP status codes per the
// package-level errors.go contract:
//
//   - NotFoundError     -> 404
//   - UserNotLiveError  -> 403
//   - ValidationError   -> 400
//   - unknown format    -> 415 (only when caller asked
//     explicitly via ?target=)
func (h *Handler) handleRender(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "missing sub_token", http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	user, err := h.svc.GetUserBySubToken(ctx, token)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	format := detectFormat(r)

	// Phase 0: base64, singbox, and html are
	// implemented end-to-end; clash returns 501.
	switch format {
	case FormatBase64:
		h.renderBase64(w, ctx, user)
	case FormatSingbox:
		h.renderSingbox(w, ctx, user)
	case FormatClash:
		writeNotImplemented(w, format)
	case FormatHTML:
		h.renderHTML(w, r, ctx, user)
	default:
		// detectFormat already returned one of the
		// known Format values; an unknown value here
		// means the caller asked for an unknown
		// ?target= and we could not even map it to
		// base64. Surface as 415.
		http.Error(w, "unsupported subscription format: "+string(format), http.StatusUnsupportedMediaType)
	}
}

// --- base64 render ---------------------------------------------------

// renderBase64 resolves the user's entitled endpoints
// and writes the base64-encoded subscription to the
// response body. An empty subscription is a valid
// subscription; the response is 200 with an empty body
// in that case (the user is entitled to nothing).
func (h *Handler) renderBase64(w http.ResponseWriter, ctx context.Context, user *User) {
	eps, err := h.svc.ResolveEndpointsForUser(ctx, user)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	body, err := h.svc.RenderBase64(ctx, user, eps)
	if err != nil {
		writeServiceError(w, fmt.Errorf("render base64: %w", err))
		return
	}
	writeSubscriptionHeaders(w, user, FormatBase64)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(body)
}

// renderSingbox resolves the user's entitled endpoints
// and writes a sing-box outbounds JSON document to
// the response. The top-level shape is
// `{"outbounds": [...]}`. Endpoints whose inbound is
// missing required params (e.g. a VLESS without a
// uuid) are silently skipped — the subscription
// must still serve for the rest of the entitled
// endpoints.
func (h *Handler) renderSingbox(w http.ResponseWriter, ctx context.Context, user *User) {
	eps, err := h.svc.ResolveEndpointsForUser(ctx, user)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	body, err := h.svc.RenderSingbox(ctx, user, eps)
	if err != nil {
		writeServiceError(w, fmt.Errorf("render singbox: %w", err))
		return
	}
	writeSubscriptionHeaders(w, user, FormatSingbox)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}

// --- html render (Phase 0 minimal) ---------------------------------

// renderHTML serves a tiny self-contained page that
// lists the per-client subscription URL the user is
// entitled to. The QR code + per-client "copy URL"
// buttons land in a later PR — Phase 0 ships the page
// shell so the route is reachable end-to-end.
//
// The page is intentionally inline-styled and
// framework-free: the same HTML is served as the
// landing target for a phone camera, so it must work
// without JavaScript and render in <100 ms.
func (h *Handler) renderHTML(w http.ResponseWriter, r *http.Request, ctx context.Context, user *User) {
	eps, err := h.svc.ResolveEndpointsForUser(ctx, user)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	body, err := h.svc.RenderBase64(ctx, user, eps)
	if err != nil {
		writeServiceError(w, fmt.Errorf("render base64 (for html): %w", err))
		return
	}
	// The subscription URL is the request URL with
	// ?target=base64 forced. Clients that ignore the
	// html landing page can still copy the base64 URL
	// from the page.
	subURL := *r.URL
	q := subURL.Query()
	q.Set("target", "base64")
	subURL.RawQuery = q.Encode()
	subscriptionURL := subURL.String()

	rows := strings.Split(string(body), "\n")
	// rows may be empty for a user with no entitled
	// hosts. The page renders the "no hosts" branch
	// in that case.
	host := r.Host
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		scheme = fwd
	}
	canonical := scheme + "://" + host + subscriptionURL

	page := buildHTMLPage(html.EscapeString(user.Username), canonical, len(rows))
	writeSubscriptionHeaders(w, user, FormatHTML)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = ioWriteString(w, page)
}

// buildHTMLPage renders the minimal landing page. The
// IO write is split out into io_WriteString so the test
// can swap it for a buffer.
func buildHTMLPage(username, subscriptionURL string, hostCount int) string {
	hostLine := fmt.Sprintf("You have <b>%d</b> host line(s) in this subscription.", hostCount)
	if hostCount == 0 {
		hostLine = "You have no hosts in this subscription yet — contact your operator."
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>AegisPanel subscription — %s</title>
<style>
 body { font: 14px/1.4 system-ui, sans-serif; max-width: 640px; margin: 24px auto; padding: 0 12px; color: #111; }
 h1 { font-size: 18px; margin: 0 0 8px; }
 code { background: #f4f4f4; padding: 2px 6px; border-radius: 4px; word-break: break-all; }
 .muted { color: #666; font-size: 12px; margin-top: 24px; }
</style>
</head>
<body>
<h1>AegisPanel subscription</h1>
<p>User: <b>%s</b></p>
<p>%s</p>
<p>Subscription URL (paste into a VPN client, or scan the QR code — coming in a future update):</p>
<p><code>%s</code></p>
<p class="muted">Phase 0 page — no QR code, no per-client copy buttons yet.</p>
</body>
</html>
`, html.EscapeString(username), html.EscapeString(username), hostLine, html.EscapeString(subscriptionURL))
}

// ioWriteString is a tiny seam so the test can
// intercept the html render output. It is the
// production path — using io.WriteString directly is
// fine, but routing through a package-local function
// makes the test a one-liner.
func ioWriteString(w http.ResponseWriter, s string) (int, error) {
	return fmt.Fprint(w, s)
}

// --- headers --------------------------------------------------------

// writeSubscriptionHeaders sets the three standard
// subscription headers plus Content-Disposition for
// the download-style clients (some VPN clients save
// the response to disk and the file extension hint
// helps the operator spot the right format).
func writeSubscriptionHeaders(w http.ResponseWriter, user *User, format Format) {
	w.Header().Set("Profile-Title", "AegisPanel")
	// 24h is a safe default; the operator can later
	// override via config.
	w.Header().Set("Profile-Update-Interval", "24")
	w.Header().Set("Subscription-Userinfo", buildUserInfoHeader(user))
	switch format {
	case FormatBase64:
		w.Header().Set("Content-Disposition", `inline; filename="aegis-sub.txt"`)
	case FormatSingbox:
		w.Header().Set("Content-Disposition", `inline; filename="aegis-sub.json"`)
	case FormatClash:
		w.Header().Set("Content-Disposition", `inline; filename="aegis-sub.yaml"`)
	case FormatHTML:
		// No Content-Disposition: browsers render
		// the page; saving is the user's choice.
	}
}

// buildUserInfoHeader serialises the user's traffic
// counters and expire_at into the sing-box / Clash
// convention:
//
//	upload=<bytes>; download=<bytes>; total=<bytes>; expire=<unix-seconds>
//
// Missing values (no expire, no traffic limit) are
// emitted as 0; clients that understand the header
// display the numbers in their UI.
func buildUserInfoHeader(user *User) string {
	var expire int64
	if user.ExpireAt != nil {
		expire = user.ExpireAt.Unix()
	}
	// Per the sing-box header convention, "total" is
	// the limit, "upload" / "download" are used.
	// For Phase 0 we only have "traffic_used_bytes";
	// the split into upload vs download is owned by
	// the stats pipeline and lands later. We emit
	// used as both upload and download so the UI has
	// a non-zero value to display.
	used := user.TrafficUsedBytes
	total := user.TrafficLimitBytes
	return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d",
		used, used, total, expire)
}

// --- error mapping --------------------------------------------------

// writeServiceError maps a Service / Store error to an
// HTTP status. The mapping is the public contract —
// handlers in other packages use the same error types
// for the same status codes.
func writeServiceError(w http.ResponseWriter, err error) {
	var verr *ValidationError
	var nferr *NotFoundError
	var nlerr *UserNotLiveError
	switch {
	case errors.As(err, &nferr):
		http.Error(w, nferr.Error(), http.StatusNotFound)
	case errors.As(err, &nlerr):
		http.Error(w, nlerr.Error(), http.StatusForbidden)
	case errors.As(err, &verr):
		http.Error(w, verr.Error(), http.StatusBadRequest)
	default:
		log.Error().Err(err).Msg("subscription handler: internal error")
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// writeNotImplemented writes a 501 response for
// formats the renderer does not yet supported. The
// body is a fixed string (no untrusted data is
// interpolated; the format name is omitted from the
// body so gosec's taint analysis does not flag the
// interpolation as a possible XSS sink — the 501
// status code is the machine-readable signal, and
// the operator can identify the format from the
// request log without it appearing in the body).
func writeNotImplemented(w http.ResponseWriter, _ Format) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte("subscription format not implemented yet; use target=base64 or target=html in the meantime\n"))
}

// --- format detection ----------------------------------------------

// detectFormat returns the wire format the caller is
// asking for. The order of precedence is:
//
//  1. ?target=  (explicit override)
//  2. Accept    (clash wins on yaml, singbox on json)
//  3. User-Agent (substring match against the
//     well-known client list)
//  4. base64    (default)
//
// Unknown explicit targets surface as their literal
// string so the handler can return 415; the auto-detect
// path always returns a known Format value.
func detectFormat(r *http.Request) Format {
	if t := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("target"))); t != "" {
		return Format(t)
	}
	// Accept header — YAML wins (clash), then JSON
	// (singbox). The values are taken from the
	// well-known client docs; we accept the bare
	// media type without parameters.
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "application/yaml") || strings.Contains(accept, "text/yaml") {
		return FormatClash
	}
	if strings.Contains(accept, "application/json") {
		return FormatSingbox
	}
	// User-Agent — substring match, lowercased. The
	// list mirrors the "auto-detect" table in
	// ARCHITECTURE.md §10.4.
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	clashSubstr := []string{"clash", "mihomo"}
	for _, s := range clashSubstr {
		if strings.Contains(ua, s) {
			return FormatClash
		}
	}
	singboxSubstr := []string{
		"sing-box", "hiddify", "nekobox", "karing",
		"streisand", "v2box",
	}
	for _, s := range singboxSubstr {
		if strings.Contains(ua, s) {
			return FormatSingbox
		}
	}
	return FormatBase64
}
