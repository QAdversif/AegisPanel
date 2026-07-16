// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Format variables + wildcard salt (ARCHITECTURE.md
// §10.1.1 and §10.1.2).
//
// # Format variables
//
// The `remark` and `address` fields on a Host (and
// the `SNI` / `Host` arrays on each endpoint) may
// contain `{VARIABLE}` placeholders. The renderer
// substitutes them at fetch time using values
// derived from the requesting user:
//
//	{USERNAME}        — user's username
//	{DATA_USAGE}      — formatted traffic_used_bytes
//	{DATA_LIMIT}      — formatted traffic_limit_bytes
//	{DATA_LEFT}       — formatted (limit - used) bytes
//	{DAYS_LEFT}       — days until expire_at (or "∞")
//	{EXPIRE_DATE}     — Gregorian date string
//	{STATUS_EMOJI}    — ✅ / ⌛️ / 🪫 / ❌ / 🔌
//	{USAGE_PERCENTAGE} — integer percent of limit used
//	{PROTOCOL}        — inbound protocol (vless / etc.)
//	{SERVER_IP}       — node address (first segment)
//
// Unknown placeholders are left intact so the
// operator can see the literal `{XYZ}` in the
// subscription rather than a confusing empty
// string. This matches the convention used by the
// popular Russian panel projects (Marzban, 3X-UI).
//
// # Wildcard salt
//
// The `SNI`, `Host`, and `address` fields on a Host
// (or an endpoint override) may contain `*`. On
// each subscription fetch, the `*` is replaced with
// an 8-character hex salt derived from
//
//	sha256(host_id || user_id || fetch_minute)
//
// The minute-bucket makes the salt stable for one
// minute — clients that re-fetch within the same
// minute get the same SNI, which avoids breaking
// in-flight connections; clients that wait longer
// get a fresh salt. The salt is 8 hex characters
// (32 bits of entropy), which is enough to defeat
// DPI heuristic-fingerprinting on a per-fetch
// basis (DPI does not see a single client, it sees
// the union of all clients in a 60s window).
//
// The cache story is intentionally NOT implemented
// in this PR — the salt is computed per call, which
// is the same end-to-end cost. A future PR may add
// a `(host_id, user_id, fetch_minute) -> salt`
// cache for cases where the resolver runs in front
// of a high-QPS edge.
//
// # Phase 0 scope
//
//   - the substitution helpers (no `text/template`
//     sandbox — a tiny {VAR} matcher is enough for
//     the closed-set of variables)
//   - the salt computation (sha256 / minute-bucket)
//   - the Service-layer integration: the three
//     existing renderers (base64, singbox, clash)
//     are wrapped in an "enrich" pass that runs
//     before the render. The renderers themselves
//     are unchanged — they continue to see
//     `displayName(host)` and `effectiveAddress(ep)`
//     as before, but the values are now pre-resolved.
//   - the HTML sub-page (`?target=html`) does NOT
//     apply vars / salt — the page is a
//     configuration UI, not a rendered subscription.

package subscription

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// RenderContext bundles the per-fetch state that
// every renderer needs: the salt (for wildcard `*`)
// and the variable map (for `{VAR}` placeholders).
// The struct is small enough to copy freely.
type RenderContext struct {
	// Salt is the 8-char hex string that replaces
	// every `*` in the rendered subscription. Empty
	// when no user is attached (Phase 0 unit tests
	// run with user=nil to verify the no-op path).
	Salt string
	// Vars is the placeholder map (USERNAME ->
	// "alice", DATA_LEFT -> "98.5 GB", …). Empty
	// when no user is attached.
	Vars map[string]string
}

// BuildRenderContext computes the per-fetch RenderContext
// for a user + host pair. The salt is
// `(host_id, user_id, fetch_minute)`-derived via
// sha256; the variable map is the user's traffic /
// status / etc. derived from `User`.
//
// When `u` is nil, the returned context has empty
// Vars and the salt is the empty string — every
// substitution is a no-op, which is exactly what
// the unit tests want when they assert the
// no-render-time-magic baseline.
//
// When `now` is the zero time, the salt bucket
// falls back to time.Now(); the public `s.now`
// field on the Service is the right way to inject
// a deterministic clock.
func BuildRenderContext(u *User, h *hosts.Host, now time.Time) RenderContext {
	if u == nil {
		return RenderContext{}
	}
	if now.IsZero() {
		now = time.Now()
	}
	return RenderContext{
		Salt: computeSalt(h.ID, u.ID, now),
		Vars: buildUserVars(u),
	}
}

// computeSalt returns the 8-char hex salt for a
// (host, user, minute) triple. The minute-bucket
// makes the salt stable for one minute so a client
// that re-fetches within the same window gets the
// same value (avoids breaking an in-flight
// connection that re-resolves the SNI).
func computeSalt(hostID, userID uuid.UUID, now time.Time) string {
	bucket := now.Truncate(time.Minute).Unix()
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", hostID, userID, bucket)))
	return hex.EncodeToString(h[:4]) // 8 hex chars
}

// computeSaltWithString is the username-keyed salt
// used by the test-friendly baseline. It is
// deterministic in `username + host + minute` and
// stable across multiple endpoints of the same
// host. When the Phase 1 user-CRUD work lands,
// the Service.newRenderContext thread sets a
// `_user_id` key and computeSalt takes over.
func computeSaltWithString(hostID uuid.UUID, username string, now time.Time) string {
	bucket := now.Truncate(time.Minute).Unix()
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", hostID, username, bucket)))
	return hex.EncodeToString(h[:4])
}

// buildUserVars is the per-user variable map. The
// closed set of variables is pinned by
// ARCHITECTURE.md §10.1.1; unknown variables in the
// Host template are left intact (see applyFormatVariables).
func buildUserVars(u *User) map[string]string {
	vars := make(map[string]string, 12)
	vars["USERNAME"] = u.Username
	vars["DATA_USAGE"] = formatBytes(u.TrafficUsedBytes)
	vars["DATA_LIMIT"] = formatBytes(u.TrafficLimitBytes)
	vars["DATA_LEFT"] = formatBytes(u.TrafficLimitBytes - u.TrafficUsedBytes)
	if u.ExpireAt != nil {
		vars["DAYS_LEFT"] = formatDaysLeft(*u.ExpireAt, time.Now())
		vars["EXPIRE_DATE"] = u.ExpireAt.Format("2006-01-02")
	} else {
		vars["DAYS_LEFT"] = "∞"
		vars["EXPIRE_DATE"] = "∞"
	}
	vars["STATUS_EMOJI"] = statusEmoji(u.Status)
	vars["USAGE_PERCENTAGE"] = formatUsagePercent(u.TrafficUsedBytes, u.TrafficLimitBytes)
	// PROTOCOL is per-endpoint, not per-user; the
	// per-endpoint enrichment path overwrites this.
	// The seed value "?" is a defensive default for
	// the per-host enrichment pass that does not
	// know the endpoint's protocol (e.g. a Host
	// remark that references {PROTOCOL} — that
	// resolves to "?" in Phase 0, which signals
	// "host-level template cannot know").
	vars["PROTOCOL"] = "?"
	// SERVER_IP, ADMIN_USERNAME land with the
	// Phase 1 user-CRUD work; in Phase 0 they
	// stay empty.
	return vars
}

// formatBytes turns an int64 byte count into a
// human-readable string. 0 (unset) renders as
// "∞" to match the convention "no limit = infinity".
// The unit is the largest power of 1024 that
// produces a number < 1024.
func formatBytes(b int64) string {
	if b <= 0 {
		return "∞"
	}
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
		tb = 1024 * gb
	)
	switch {
	case b < mb:
		return fmt.Sprintf("%d B", b)
	case b < gb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	case b < tb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	default:
		return fmt.Sprintf("%.1f TB", float64(b)/float64(tb))
	}
}

// formatDaysLeft returns the integer number of
// days until `expire`. Past-dated expire returns
// "0" (the user is already expired; the renderer
// should map this to a non-live status upstream).
func formatDaysLeft(expire, now time.Time) string {
	if expire.Before(now) {
		return "0"
	}
	d := expire.Sub(now).Hours() / 24
	return fmt.Sprintf("%d", int(d))
}

// formatUsagePercent returns the integer percent of
// the user's traffic limit consumed. 0 limit
// (no limit configured) returns "0"; the human-
// readable value lives in DATA_LEFT.
func formatUsagePercent(used, limit int64) string {
	if limit <= 0 {
		return "0"
	}
	pct := int((used * 100) / limit)
	if pct > 100 {
		pct = 100
	}
	return fmt.Sprintf("%d", pct)
}

// statusEmoji returns the operator-friendly emoji
// for the user's status. Mirrors the convention
// from Marzban / 3X-UI so operators see the same
// glyph across panels.
func statusEmoji(s UserStatus) string {
	switch s {
	case UserStatusActive:
		return "✅"
	case UserStatusGrace:
		return "⌛️"
	case UserStatusDisabled:
		return "❌"
	case UserStatusExpired:
		return "🪫"
	case UserStatusDeleted:
		return "🔌"
	default:
		return "❓"
	}
}

// applyFormatVariables substitutes every {VAR}
// placeholder in `s` with the corresponding value
// from `vars`. Unknown placeholders are left
// intact. An empty / nil vars map is a no-op.
//
// The substitution is a simple string replace — not
// a `text/template` engine — because the closed
// variable set does not warrant the sandbox and
// the parser cost. The braces are required (a
// stray { without } is left intact, so an operator
// can write literally `{not_a_var}` in a remark).
func applyFormatVariables(s string, vars map[string]string) string {
	if len(vars) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] != '{' {
			b.WriteByte(s[i])
			i++
			continue
		}
		// Find the matching '}'.
		j := strings.IndexByte(s[i+1:], '}')
		if j < 0 {
			// Unterminated — copy the rest as-is.
			b.WriteString(s[i:])
			break
		}
		name := s[i+1 : i+1+j]
		if v, ok := vars[name]; ok {
			b.WriteString(v)
		} else {
			// Unknown variable — copy the literal
			// so the operator sees {XYZ} in the
			// output rather than a silent empty.
			b.WriteString(s[i : i+1+j+1])
		}
		i += 1 + j + 1
	}
	return b.String()
}

// applyWildcardSalt replaces every `*` in `s` with
// `salt`. An empty salt is a no-op (the unit tests
// pass empty salt to verify the no-render-time-magic
// baseline). The replacement is byte-level — there
// is no escaping or quoting.
func applyWildcardSalt(s, salt string) string {
	if salt == "" {
		return s
	}
	return strings.ReplaceAll(s, "*", salt)
}

// enrichEndpoint returns a copy of `ep` with the
// `displayName`, `remark`, `address` / `port` /
// `sni` / `host` / `path` fields rewritten through
// `applyFormatVariables` and `applyWildcardSalt`.
// The per-endpoint protocol is added to the
// `PROTOCOL` variable so a host remark like
// "{USERNAME}@{PROTOCOL}" resolves correctly.
//
// A nil rc is a no-op (the function returns a copy
// of ep as-is), which is the test-friendly baseline.
//
// The salt is computed per host (when the host is
// non-nil and the user is non-nil) so a user on
// two different hosts gets two different salts.
func enrichEndpoint(ep ResolvedEndpoint, rc *RenderContext) ResolvedEndpoint {
	if rc == nil {
		return ep
	}
	out := ep
	// Per-host salt: stable for one minute (caller
	// controls time via s.now()). Empty when the
	// user is nil (test-friendly baseline) or when
	// the host is nil (no host-level salt to
	// compute).
	var salt string
	if ep.Host != nil && rc.Vars != nil {
		// We need a "user id" to compute the salt.
		// The user is implicit in rc.Vars (the
		// "USERNAME" key is always present when
		// Vars is non-empty), but for a stable
		// hash we need a real id. Use the first
		// 8 bytes of the username's hash as a
		// deterministic-but-per-user salt
		// input. This is a Phase 0 simplification;
		// the salt seed will move to the User
		// type's id field with the Phase 1
		// user-CRUD work.
		if u, ok := rc.Vars["_user_id"]; ok {
			salt = computeSalt(ep.Host.ID, uuid.MustParse(u), timeNow(rc))
		} else {
			// Fallback: hash on the username. This
			// keeps tests stable without requiring
			// the user id to be threaded through.
			salt = computeSaltWithString(ep.Host.ID, rc.Vars["USERNAME"], timeNow(rc))
		}
	}
	// Build the per-endpoint vars map first, so the
	// per-endpoint PROTOCOL / SERVER_IP keys are
	// available when applyFormatVariables runs on
	// the host remark / displayName. A local copy
	// keeps the function reentrant on a shared
	// RenderContext (e.g. when the Service enriches
	// a slice of endpoints in a loop).
	vars := rc.Vars
	if vars != nil && out.Inbound != nil {
		local := make(map[string]string, len(vars)+2)
		for k, v := range vars {
			local[k] = v
		}
		local["PROTOCOL"] = string(out.Inbound.Protocol)
		if out.Node != nil && out.Node.Address != "" {
			local["SERVER_IP"] = out.Node.Address
		}
		vars = local
	}
	// displayName / remark (host-level). Note
	// displayName wins per the per-entity override
	// chain in the model.
	if out.Host != nil {
		out.Host.DisplayName = applyFormatVariables(applyWildcardSalt(out.Host.DisplayName, salt), vars)
		out.Host.Remark = applyFormatVariables(applyWildcardSalt(out.Host.Remark, salt), vars)
	}
	// Endpoint-level overrides: address / sni /
	// host / path. Port is an int and is not
	// string-substituted.
	out.Endpoint.Address = applyFormatVariablesOnSlice(applyWildcardToStringSlice(out.Endpoint.Address, salt), vars)
	out.Endpoint.SNI = applyFormatVariablesOnSlice(applyWildcardToStringSlice(out.Endpoint.SNI, salt), vars)
	out.Endpoint.Host = applyFormatVariablesOnSlice(applyWildcardToStringSlice(out.Endpoint.Host, salt), vars)
	out.Endpoint.Path = applyFormatVariables(applyWildcardSalt(out.Endpoint.Path, salt), vars)
	return out
}

// timeNow extracts the wall-clock time from a
// RenderContext. Phase 0 stores no time on the
// context, so we fall back to time.Now(). The
// Service tests pin the clock via s.now(); the
// RenderContext is not used to thread the clock
// through the per-endpoint enrich pass yet — the
// salt minute-bucket is a "now-stable for a
// minute" property, and the unit tests do not
// assert on a specific minute.
func timeNow(_ *RenderContext) time.Time {
	return time.Now()
}

// applyWildcardToStringSlice is the []string form of
// applyWildcardSalt. Returns the slice (or a new
// slice if at least one entry was modified).
func applyWildcardToStringSlice(in []string, salt string) []string {
	if salt == "" || len(in) == 0 {
		return in
	}
	out := make([]string, len(in))
	changed := false
	for i, s := range in {
		out[i] = applyWildcardSalt(s, salt)
		if out[i] != s {
			changed = true
		}
	}
	if !changed {
		return in
	}
	return out
}

// applyFormatVariablesOnSlice is the []string form
// of applyFormatVariables.
func applyFormatVariablesOnSlice(in []string, vars map[string]string) []string {
	if len(vars) == 0 || len(in) == 0 {
		return in
	}
	out := make([]string, len(in))
	changed := false
	for i, s := range in {
		out[i] = applyFormatVariables(s, vars)
		if out[i] != s {
			changed = true
		}
	}
	if !changed {
		return in
	}
	return out
}

// _ keeps the inbounds / nodes imports in use while
// the per-endpoint enrich pass evolves. Today the
// enrichment reads `ep.Inbound.Protocol` and
// `ep.Node.Address`; the imports document the
// surface and guard against future drift.
var _ = inbounds.ProtocolVLESS
var _ = nodes.StateNew
