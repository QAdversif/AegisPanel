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
//   - ?target=singbox - implemented (Phase 1)
//   - ?target=clash   - implemented (Phase 1)
//   - ?target=html    - minimal HTML page with QR
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
// the router, and is unauthenticated by design - the
// sub_token IS the credential.
//
// # Rate limiting (PR-K)
//
// The handler is rate-limited per sub_token via the
// internal/ratelimit package. A nil limiter (the
// v0.1.0 default) short-circuits to allow every
// request; a real limiter rejects the over-budget
// path with HTTP 429 and a Retry-After header. The
// key is the sub_token from the URL - the limit
// is per-credential, so a stolen token cannot be
// replayed across many IPs without tripping the
// limit. v0.3 will add a per-IP second dimension
// when the audit log UI exposes a per-user
// "suspicious activity" view.

package subscription

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/ratelimit"
)

// Handler is the HTTP entry point. It wraps a Service
// and exposes a chi subrouter via Router().
type Handler struct {
	svc     *Service
	limiter *ratelimit.Limiter
}

// NewHandler wires a Handler around the given service.
// A nil limiter is safe - Allow() on a nil receiver
// always returns (true, 0), so the subscription
// endpoint behaves as if rate limiting were disabled.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, limiter: nil}
}

// WithLimiter attaches a rate limiter. The same
// limiter instance is shared across all Handler
// instances created by the same Router call; the
// caller is responsible for its lifecycle (the
// limiter has no Close / Stop - it is a pure
// in-memory data structure that lives for the
// lifetime of the process).
func (h *Handler) WithLimiter(l *ratelimit.Limiter) *Handler {
	h.limiter = l
	return h
}

// Router returns a chi subrouter for the subscription
// surface:
//
//	r.Mount("/api/v1/sub", subscription.Router(svc))
//
// The returned router has no auth middleware - the
// sub_token in the URL is the credential. A nil
// limiter is the v0.1.0 behaviour (no rate limiting);
// passing a real *ratelimit.Limiter enables per-
// sub_token throttling with a 429 + Retry-After
// response on the over-budget path.
func Router(svc *Service) http.Handler {
	return RouterWithLimiter(svc, nil)
}

// RouterWithLimiter is the explicit "rate limiting
// enabled" form. Same signature shape as Router; the
// caller passes a real *ratelimit.Limiter to enable
// throttling.
func RouterWithLimiter(svc *Service, l *ratelimit.Limiter) http.Handler {
	h := NewHandler(svc).WithLimiter(l)
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

	// Rate limit per sub_token. The key is the
	// raw token string - the only caller that
	// knows it is the legitimate user (or an
	// attacker who scraped it from a leaked
	// device / proxy log). A stolen token
	// therefore shares the bucket regardless of
	// the attacker's IP - the v0.2 limit
	// defends the credential-scraping case
	// (an attacker spraying many tokens to find
	// one valid). v0.3 adds the per-IP dimension
	// when the audit log UI exposes a per-user
	// "suspicious activity" view.
	if h.limiter != nil {
		if ok, retry := h.limiter.Allow(token); !ok {
			writeRateLimited(w, retry)
			return
		}
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

// writeRateLimited emits a 429 with a Retry-After
// header. The Retry-After value is rounded up to the
// next integer second (the spec allows a delta-
// seconds non-negative integer or an HTTP-date; we
// use the integer form for sub-second precision).
// We also include a JSON-ish body for clients that
// do not render 429s as plain text.
func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(retryAfter / time.Second)
	if retryAfter%time.Second != 0 {
		seconds++ // round up
	}
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
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
// the response.
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
// the response.
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

// --- html render -----------------------------------------------------

// renderHTML serves the QR-code landing page.
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
	base64SubURL := subscriptionURLFor(r, FormatBase64)
	singboxSubURL := subscriptionURLFor(r, FormatSingbox)
	clashSubURL := subscriptionURLFor(r, FormatClash)

	hostCount := 0
	if len(body) > 0 {
		hostCount = len(strings.Split(strings.TrimRight(string(body), "\n"), "\n"))
	}

	qrDataURL, err := buildQRCodeDataURL(base64SubURL, 256)
	if err != nil {
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
// current request with `?target=<format>` forced.
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

// buildHTMLPage renders the landing page.
func buildHTMLPage(username, qrDataURL, base64URL, singboxURL, clashURL string, hostCount int) string {
	hostLine := fmt.Sprintf("You have <b>%d</b> host line(s) in this subscription.", hostCount)
	if hostCount == 0 {
		hostLine = "You have no hosts in this subscription yet - contact your operator."
	}
	rows := htmlClientRows(base64URL, singboxURL, clashURL)
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>AegisPanel subscription - %s</title>
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
<p>Scan the QR code with a VPN client that supports camera import (Hiddify, Streisand, NekoBox, Karing, V2Box, ...):</p>
<img class="qr" alt="Subscription QR code" src="%s">
<p>Or pick a per-client URL below and paste it into the client's "import from URL" field:</p>
<table>
<thead><tr><th>Client</th><th>URL</th><th></th></tr></thead>
<tbody>
%s
</tbody>
</table>
<p class="muted">If the copy button does nothing, your browser blocks the clipboard API over plain HTTP - use the URL text directly.</p>
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
// body.
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
// intercept the html render output.
func ioWriteString(w http.ResponseWriter, s string) (int, error) {
	return fmt.Fprint(w, s)
}

// --- headers --------------------------------------------------------

// writeSubscriptionHeaders sets the three standard
// subscription headers plus Content-Disposition for
// the download-style clients.
func writeSubscriptionHeaders(w http.ResponseWriter, user *User, format Format) {
	w.Header().Set("Profile-Title", "AegisPanel")
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
	}
}

// buildUserInfoHeader serialises the user's traffic
// counters and expire_at into the sing-box / Clash
// convention.
func buildUserInfoHeader(user *User) string {
	var expire int64
	if user.ExpireAt != nil {
		expire = user.ExpireAt.Unix()
	}
	used := user.TrafficUsedBytes
	total := user.TrafficLimitBytes
	return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d",
		used, used, total, expire)
}

// --- error mapping --------------------------------------------------

// writeServiceError maps a Service / Store error to an
// HTTP status.
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
// asking for.
func detectFormat(r *http.Request) Format {
	if t := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("target"))); t != "" {
		return Format(t)
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "application/yaml") || strings.Contains(accept, "text/yaml") {
		return FormatClash
	}
	if strings.Contains(accept, "application/json") {
		return FormatSingbox
	}
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
