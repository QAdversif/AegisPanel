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

	// Phase 0: base64, singbox, clash, and html are
	// implemented end-to-end.
	switch format {
	case FormatBase64:
		h.renderBase64(w, ctx, user)
	case FormatSingbox:
		h.renderSingbox(w, ctx, user)
	case FormatClash:
		h.renderClash(w, ctx, user)
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

// renderClash resolves the user's entitled endpoints
// and writes a Clash proxy-list YAML document to
// the response. The top-level shape is
// `proxies: [ <proxy>, ... ]`. proxy-groups and
// rules are intentionally NOT emitted — those are a
// per-client policy concern, not a per-subscription
// one. Clients merge this list into their own
// template and apply the user-defined groups / rules
// there.
func (h *Handler) renderClash(w http.ResponseWriter, ctx context.Context, user *User) {
	eps, err := h.svc.ResolveEndpointsForUser(ctx, user)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	body, err := h.svc.RenderClash(ctx, user, eps)
	if err != nil {
		writeServiceError(w, fmt.Errorf("render clash: %w", err))
		return
	}
	writeSubscriptionHeaders(w, user, FormatClash)
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	_, _ = w.Write(body)
}

// --- html render (Phase 0 minimal) ---------------------------------

// renderHTML serves the QR-code landing page. The
// page embeds:
//
//   - a 256x256 QR code (PNG, data-URL) encoding the
//     base64 subscription URL — the default for phone-
//     camera import;
//   - three copyable per-client URLs (base64, singbox,
//     clash) with a vanilla-JS "copy" button;
//   - the user's username and a count of entitled
//     hosts.
//
// The page is intentionally inline-styled and
// framework-free: a phone camera must be able to
// render it without JavaScript and within the first
// second of the request returning. The copy-button
// JS is the only script on the page; it degrades
// gracefully (selects the input on clipboard-API
// failure) so the URL is still accessible.
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
	// The base64 URL is the request URL with
	// ?target=base64 forced. Clients that ignore the
	// html landing page can still copy the base64 URL
	// from the page.
	base64SubURL := subscriptionURLFor(r, FormatBase64)
	singboxSubURL := subscriptionURLFor(r, FormatSingbox)
	clashSubURL := subscriptionURLFor(r, FormatClash)

	// hostCount is read off the rendered base64 body:
	// each line is one URI, and an empty / single-
	// newline body means "no entitled hosts".
	hostCount := 0
	if len(body) > 0 {
		hostCount = len(strings.Split(strings.TrimRight(string(body), "\n"), "\n"))
	}

	// Build the QR code. The QR encodes the base64
	// URL — that is the path every client can import
	// from a URL; the per-client URLs on the page are
	// the explicit override.
	qrDataURL, err := buildQRCodeDataURL(base64SubURL, 256)
	if err != nil {
		// A QR failure must not 5xx the page; the user
		// can still use the per-client URLs. We log
		// the error and render the page with an empty
		// QR (the <img> shows the alt text).
		log.Warn().Err(err).Msg("subscription handler: qr render failed")
		qrDataURL = ""
	}

	page := buildHTMLPage(
		html.EscapeString(user.Username),
		qrDataURL,
		base64SubURL,
		singboxSubURL,
		clashSubURL,
		hostCount,
	)
	writeSubscriptionHeaders(w, user, FormatHTML)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = ioWriteString(w, page)
}

// subscriptionURLFor returns the absolute URL of the
// current request with `?target=<format>` forced. The
// scheme honours r.TLS and the X-Forwarded-Proto
// header (set by a reverse proxy in front of the
// panel); the host is r.Host. Both are needed because
// the QR code is scanned off the device — the device
// has to be able to reach the URL it scans.
func subscriptionURLFor(r *http.Request, format Format) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		scheme = fwd
	}
	u := *r.URL
	q := u.Query()
	q.Set("target", string(format))
	u.RawQuery = q.Encode()
	return scheme + "://" + r.Host + u.String()
}

// buildHTMLPage renders the landing page. The page
// embeds the QR code (a `data:` URL) and a per-client
// URL table. The QR is sized at 256x256, which is the
// sweet spot for phone-camera scanning without
// bloating the response past ~5 KB.
//
// `urls` carries one row per (label, url) pair. The
// page renders a copy-to-clipboard button next to
// each row; the JS handler is a tiny `onclick` that
// calls `navigator.clipboard.writeText` and falls
// back to a manual prompt if the API is unavailable
// (older browsers, no-HTTPS, etc.).
func buildHTMLPage(username, qrDataURL, base64URL, singboxURL, clashURL string, hostCount int) string {
	hostLine := fmt.Sprintf("You have <b>%d</b> host line(s) in this subscription.", hostCount)
	if hostCount == 0 {
		hostLine = "You have no hosts in this subscription yet — contact your operator."
	}
	rows := htmlClientRows(base64URL, singboxURL, clashURL)
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>AegisPanel subscription — %s</title>
<style>
 body { font: 14px/1.4 system-ui, sans-serif; max-width: 640px; margin: 24px auto; padding: 0 12px; color: #111; }
 h1 { font-size: 18px; margin: 0 0 8px; }
 .qr { display: block; margin: 16px auto; max-width: 256px; width: 100%%; height: auto; }
 code { background: #f4f4f4; padding: 2px 6px; border-radius: 4px; word-break: break-all; }
 table { width: 100%%; border-collapse: collapse; margin-top: 12px; }
 th, td { text-align: left; padding: 6px 8px; border-bottom: 1px solid #eee; font-size: 13px; vertical-align: top; }
 th { background: #fafafa; }
 button { font: inherit; padding: 4px 10px; border: 1px solid #ccc; background: #fff; border-radius: 4px; cursor: pointer; }
 button:hover { background: #f0f0f0; }
 .muted { color: #666; font-size: 12px; margin-top: 24px; }
 .url-cell { font-family: ui-monospace, Menlo, monospace; font-size: 12px; }
</style>
</head>
<body>
<h1>AegisPanel subscription</h1>
<p>User: <b>%s</b></p>
<p>%s</p>
<p>Scan the QR code with a VPN client that supports camera import (Hiddify, Streisand, NekoBox, Karing, V2Box, …):</p>
<img class="qr" alt="Subscription QR code" src="%s">
<p>Or pick a per-client URL below and paste it into the client's "import from URL" field:</p>
<table>
<thead><tr><th>Client</th><th>URL</th><th></th></tr></thead>
<tbody>
%s
</tbody>
</table>
<p class="muted">If the copy button does nothing, your browser blocks the clipboard API over plain HTTP — use the URL text directly.</p>
</body>
<script>
document.addEventListener('DOMContentLoaded', function() {
  var btns = document.querySelectorAll('button[data-copy]');
  for (var i = 0; i < btns.length; i++) {
    btns[i].addEventListener('click', function() {
      var id = this.getAttribute('data-copy');
      var el = document.getElementById(id);
      if (!el) return;
      try { navigator.clipboard.writeText(el.value).then(function() { var s = el.parentNode.querySelector('.status'); if (s) s.textContent = 'copied'; }); }
      catch (e) { el.select(); }
    });
  }
});
</script>
</html>
`,
		html.EscapeString(username),
		html.EscapeString(username),
		hostLine,
		qrDataURL,
		rows,
	)
}

// htmlClientRows renders the per-client URL table
// body. Each row has a copyable `<input>` so the
// "copy" button has a stable target element. The
// inputs are `readonly` (the user is not supposed to
// edit them — the URL is the credential).
func htmlClientRows(base64URL, singboxURL, clashURL string) string {
	return fmt.Sprintf(`<tr><td>Base64 (v2rayN, Shadowrocket, v2rayNG)</td><td class="url-cell"><input id="u1" readonly value="%s"></td><td><button data-copy="u1">copy</button> <span class="status"></span></td></tr>
<tr><td>Sing-box (Hiddify, NekoBox, sing-box CLI)</td><td class="url-cell"><input id="u2" readonly value="%s"></td><td><button data-copy="u2">copy</button> <span class="status"></span></td></tr>
<tr><td>Clash / Mihomo (Clash Verge, Clash Meta)</td><td class="url-cell"><input id="u3" readonly value="%s"></td><td><button data-copy="u3">copy</button> <span class="status"></span></td></tr>`,
		html.EscapeString(base64URL),
		html.EscapeString(singboxURL),
		html.EscapeString(clashURL),
	)
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
