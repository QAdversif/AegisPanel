// SPDX-License-Identifier: AGPL-3.0-or-later

package hosts

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// --- bounds -------------------------------------------------------------

const (
	// maxRemarkLen is the longest remark we will store.
	// Long enough for a 2-line display name in the admin
	// UI, short enough to keep the rendered subscription
	// URL readable on a phone.
	maxRemarkLen = 96
	// maxDisplayNameLen bounds the optional
	// display_name override.
	maxDisplayNameLen = 96
	// maxCountry / maxCity match what the admin UI
	// shows in the country flag + city column.
	maxCountryLen = 8 // ISO 3166-1 alpha-2/alpha-3 with margin
	maxCityLen    = 64
	// maxTagLen / maxTags mirror the nodes package so
	// the host and node tags render with the same
	// rules in the UI.
	maxTagLen = 32
	maxTags   = 16
	// Priority is signed 16-bit. Negative priorities
	// mean "this host should appear before the
	// zero-priority default" — useful for premium /
	// sponsor hosts.
	minPriority = -32768
	maxPriority = 32767
	maxWeight   = 1000
	maxPort     = 65535
	// Healthcheck URL is bounded so a typo does not
	// bloat the row.
	maxHealthcheckURLLen = 256
	// Healthcheck interval is bounded so a typo (say,
	// 0 seconds) does not melt the agent.
	minHealthcheckIntervalSec = 10
	maxHealthcheckIntervalSec = 3600
)

// --- remark / name validation -------------------------------------------

func validateRemark(remark string) error {
	remark = strings.TrimSpace(remark)
	if remark == "" {
		return &ValidationError{Field: "remark", Message: "must not be empty"}
	}
	if len(remark) > maxRemarkLen {
		return &ValidationError{Field: "remark", Message: "exceeds maximum length"}
	}
	// Reject control characters and other non-printable
	// runes. The admin UI shows remarks verbatim in the
	// subscription URL, and embedding a newline would
	// break the URL.
	for _, r := range remark {
		if r < 0x20 || r == 0x7f {
			return &ValidationError{Field: "remark", Message: "must not contain control characters"}
		}
	}
	return nil
}

// validateDisplayName caps the optional display_name. It
// is intentionally a separate function (not a single
// generic "name" helper) so each field can evolve its
// own rules — e.g. display_name is allowed to contain
// emoji, remark is not.
func validateDisplayName(s string) error {
	if len(s) > maxDisplayNameLen {
		return &ValidationError{Field: "display_name", Message: "exceeds maximum length"}
	}
	return nil
}

// validateCountry caps the ISO country code. The UI
// uses it for the flag rendering; an over-long value
// is a misconfigured client.
func validateCountry(s string) error {
	if len(s) > maxCountryLen {
		return &ValidationError{Field: "country", Message: "exceeds maximum length"}
	}
	return nil
}

// validateCity caps the free-form city label.
func validateCity(s string) error {
	if len(s) > maxCityLen {
		return &ValidationError{Field: "city", Message: "exceeds maximum length"}
	}
	return nil
}

// --- type / strategy / status validation --------------------------------

func validateType(t HostType) error {
	switch t {
	case HostTypeDirect, HostTypeBalancer:
		return nil
	}
	return &ValidationError{Field: "type", Message: "unknown type: " + string(t)}
}

func validateStrategy(s BalancerStrategy) error {
	switch s {
	case StrategyRoundRobin, StrategyLeastLoaded, StrategyRandom,
		StrategyLeastPing, StrategyURLTest:
		return nil
	}
	return &ValidationError{Field: "balancer.strategy", Message: "unknown strategy: " + string(s)}
}

func validateStatusFilter(in []UserStatus) error {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[UserStatus]struct{}, len(in))
	for _, s := range in {
		switch s {
		case UserStatusActive, UserStatusOnHold, UserStatusExpired,
			UserStatusLimited, UserStatusDisabled:
		default:
			return &ValidationError{Field: "status_filter", Message: "unknown status: " + string(s)}
		}
		if _, dup := seen[s]; dup {
			return &ValidationError{Field: "status_filter", Message: "duplicate entry: " + string(s)}
		}
		seen[s] = struct{}{}
	}
	return nil
}

func validateHealthcheck(rawURL string, intervalSec int) error {
	if rawURL == "" && intervalSec == 0 {
		return nil
	}
	if rawURL != "" {
		if len(rawURL) > maxHealthcheckURLLen {
			return &ValidationError{Field: "balancer.healthcheck_url", Message: "exceeds maximum length"}
		}
		u, err := url.Parse(rawURL)
		if err != nil {
			return &ValidationError{Field: "balancer.healthcheck_url", Message: "not a valid URL: " + err.Error()}
		}
		// http or https only — the agent does not
		// speak anything fancier for a health probe.
		if u.Scheme != "http" && u.Scheme != "https" {
			return &ValidationError{Field: "balancer.healthcheck_url", Message: "scheme must be http or https"}
		}
		if u.Host == "" {
			return &ValidationError{Field: "balancer.healthcheck_url", Message: "host must be set"}
		}
	}
	if intervalSec != 0 {
		if intervalSec < minHealthcheckIntervalSec || intervalSec > maxHealthcheckIntervalSec {
			return &ValidationError{
				Field:   "balancer.healthcheck_interval_sec",
				Message: fmt.Sprintf("must be in [%d, %d]", minHealthcheckIntervalSec, maxHealthcheckIntervalSec),
			}
		}
	}
	return nil
}

// --- endpoint validation ------------------------------------------------

func validateEndpointNode(ctx context.Context, svc *nodes.Service, id uuid.UUID) error {
	if id == uuid.Nil {
		return &ValidationError{Field: "endpoints[].node_id", Message: "must be a non-zero UUID"}
	}
	if _, err := svc.Get(ctx, id); err != nil {
		// We collapse "node does not exist" into a
		// 400 with a clear field reference — the
		// alternative (forwarding nodes.ErrNotFound as
		// a 404) is misleading because the *host* is
		// the resource the client is trying to create.
		return &ValidationError{
			Field:   "endpoints[].node_id",
			Message: fmt.Sprintf("node %s does not exist", id),
		}
	}
	return nil
}

func validateEndpointProtocol(p string) error {
	if p == "" {
		return &ValidationError{Field: "endpoints[].protocol", Message: "must not be empty"}
	}
	if !isAllowedProtocol(p) {
		return &ValidationError{Field: "endpoints[].protocol", Message: "unsupported protocol: " + p}
	}
	return nil
}

func validateWeight(w int) error {
	if w <= 0 {
		return &ValidationError{Field: "endpoints[].weight", Message: "must be positive"}
	}
	if w > maxWeight {
		return &ValidationError{
			Field:   "endpoints[].weight",
			Message: fmt.Sprintf("must be <= %d", maxWeight),
		}
	}
	return nil
}

// validateEndpointOverrides checks the override fields
// for sanity. The list-shape fields (Address, SNI, Host)
// are capped so a typo cannot bloat the row. Port is
// validated against the IANA range.
func validateEndpointOverrides(ep Endpoint) error {
	if len(ep.Address) > 16 {
		return &ValidationError{Field: "endpoints[].address", Message: "at most 16 entries"}
	}
	if len(ep.SNI) > 16 {
		return &ValidationError{Field: "endpoints[].sni", Message: "at most 16 entries"}
	}
	if len(ep.Host) > 16 {
		return &ValidationError{Field: "endpoints[].host", Message: "at most 16 entries"}
	}
	if ep.Port != nil {
		p := *ep.Port
		if p < 1 || p > maxPort {
			return &ValidationError{
				Field:   "endpoints[].port",
				Message: fmt.Sprintf("must be in [1, %d]", maxPort),
			}
		}
	}
	if len(ep.Path) > 256 {
		return &ValidationError{Field: "endpoints[].path", Message: "exceeds 256 characters"}
	}
	return nil
}

// --- priority / tag normalisation ---------------------------------------

func validatePriority(p int) error {
	if p < minPriority || p > maxPriority {
		return &ValidationError{
			Field:   "priority",
			Message: fmt.Sprintf("must be in [%d, %d]", minPriority, maxPriority),
		}
	}
	return nil
}

func normaliseTags(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		if len(tag) > maxTagLen {
			continue
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
		if len(out) >= maxTags {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normaliseStatusFilter(in []UserStatus) []UserStatus {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[UserStatus]struct{}, len(in))
	out := make([]UserStatus, 0, len(in))
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
