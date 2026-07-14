// SPDX-License-Identifier: AGPL-3.0-or-later

package inbounds

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// --- bounds -------------------------------------------------------------

const (
	// maxNameLen is the longest name we will store.
	// Long enough for a 2-line label in the admin UI,
	// short enough to keep the rendered log line
	// readable.
	maxNameLen = 63
	// maxListenLen caps the bind-address string. The
	// longest legal value is an IPv6 literal
	// (e.g. "::ffff:192.0.2.128" = 19 chars); 63
	// leaves headroom for bracketed forms + scope.
	maxListenLen = 63
	// maxTagLen / maxTags mirror the nodes package so
	// the inbound and node tags render with the same
	// rules in the UI.
	maxTagLen = 32
	maxTags   = 16
)

// --- name validation ---------------------------------------------------

func validateName(name string) error {
	if name == "" {
		return &ValidationError{Field: "name", Message: "must not be empty"}
	}
	if len(name) > maxNameLen {
		return &ValidationError{Field: "name", Message: "exceeds maximum length"}
	}
	// Same character set as nodes.Name: lowercase
	// letters, digits, dot, dash, underscore. Spaces
	// would break the agent's log line format and
	// make the rendered config harder to read.
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return &ValidationError{
				Field:   "name",
				Message: "must contain only letters, digits, '-', '_', '.'",
			}
		}
	}
	return nil
}

// --- node validation ---------------------------------------------------

// validateNode collapses "node does not exist" into a
// 400 with a clear field reference — the alternative
// (forwarding nodes.ErrNotFound as a 404) is
// misleading because the *inbound* is the resource the
// client is trying to create.
func validateNode(ctx context.Context, svc *nodes.Service, id uuid.UUID) error {
	if id == uuid.Nil {
		return &ValidationError{Field: "node_id", Message: "must be a non-zero UUID"}
	}
	if _, err := svc.Get(ctx, id); err != nil {
		return &ValidationError{
			Field:   "node_id",
			Message: fmt.Sprintf("node %s does not exist", id),
		}
	}
	return nil
}

// --- protocol / port / listen validation -------------------------------

func validateProtocol(p Protocol) error {
	if p == "" {
		return &ValidationError{Field: "protocol", Message: "must not be empty"}
	}
	if !isAllowedProtocol(p) {
		return &ValidationError{Field: "protocol", Message: "unsupported protocol: " + string(p)}
	}
	return nil
}

func validatePort(p int) error {
	if p < 1 || p > 65535 {
		return &ValidationError{
			Field:   "listen_port",
			Message: "must be in [1, 65535]",
		}
	}
	return nil
}

// validateListen accepts an IP literal, an IPv6
// literal (with or without brackets), a wildcard
// ("::", "0.0.0.0"), or a bare hostname. The agent
// re-validates the resolved address at apply time, so
// we keep the panel check cheap: just confirm the
// value parses as a host:port-free net.IP or is a
// well-formed non-empty printable string.
func validateListen(s string) error {
	if s == "" {
		return &ValidationError{Field: "listen", Message: "must not be empty"}
	}
	if len(s) > maxListenLen {
		return &ValidationError{Field: "listen", Message: "exceeds maximum length"}
	}
	// Reject obvious control characters; the agent
	// would reject them too.
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return &ValidationError{Field: "listen", Message: "must not contain control characters"}
		}
	}
	// If it parses as an IP, accept. Otherwise it's a
	// hostname (or a wildcard like "::" that net.ParseIP
	// does not accept — the empty path / IPv6 wildcard
	// is accepted separately below).
	if ip := net.ParseIP(s); ip != nil {
		return nil
	}
	// IPv6 wildcards. "::" and "::1" both parse, so
	// they hit the IP branch above. We just need to
	// allow "0.0.0.0" which also parses, so the IP
	// branch covers it. The remaining case is a
	// hostname: confirm it is at least a single
	// printable label.
	if !isPrintableHost(s) {
		return &ValidationError{Field: "listen", Message: "not a valid IP or hostname"}
	}
	return nil
}

func isPrintableHost(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == ':':
		default:
			return false
		}
	}
	return true
}

// --- tag normalisation / validation -----------------------------------

func validateTags(in []string) error {
	if len(in) == 0 {
		return nil
	}
	if len(in) > maxTags {
		return &ValidationError{
			Field:   "tags",
			Message: fmt.Sprintf("at most %d entries", maxTags),
		}
	}
	for _, raw := range in {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		if len(tag) > maxTagLen {
			return &ValidationError{
				Field:   "tags",
				Message: fmt.Sprintf("entry exceeds %d characters", maxTagLen),
			}
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
