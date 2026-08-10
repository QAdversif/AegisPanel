// SPDX-License-Identifier: AGPL-3.0-or-later

package inboundtemplates

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ValidationError is the per-field validation failure
// raised by the Service layer. The wrapped error
// message includes the offending field name and a
// human-readable reason so the HTTP layer can put it
// in the 400 response body verbatim.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("inboundtemplates: invalid %s: %s", e.Field, e.Message)
}

// allowedProtocols is the closed set of protocol
// families a template may declare. Mirrors the DB
// CHECK constraint in migration
// 0021_inbound_templates.sql and the inbounds package
// Protocol set.
var allowedProtocols = map[Protocol]struct{}{
	ProtocolVLESS:       {},
	ProtocolHysteria2:   {},
	ProtocolShadowsocks: {},
	ProtocolTrojan:      {},
}

// isAllowedProtocol is the cheap closed-set check.
// Returns false for the empty string and for any
// value not in allowedProtocols.
func isAllowedProtocol(p Protocol) bool {
	_, ok := allowedProtocols[p]
	return ok
}

// validateName is the cheap name check. Mirrors the
// UNIQUE (name) DB constraint + the name-shape rules
// the inbounds package uses (1-64 chars, no
// control / leading-or-trailing whitespace).
func validateName(name string) error {
	if name == "" {
		return &ValidationError{Field: "name", Message: "must not be empty"}
	}
	trimmed := strings.TrimSpace(name)
	if trimmed != name {
		return &ValidationError{Field: "name", Message: "must not have leading or trailing whitespace"}
	}
	count := utf8.RuneCountInString(name)
	if count > 64 {
		return &ValidationError{Field: "name", Message: "must be at most 64 characters"}
	}
	return nil
}

// validateProtocol is the cheap protocol check.
// Closed set — any value outside allowedProtocols is
// rejected. Mirrors the DB CHECK constraint.
func validateProtocol(p Protocol) error {
	if p == "" {
		return &ValidationError{Field: "protocol", Message: "must not be empty"}
	}
	if !isAllowedProtocol(p) {
		return &ValidationError{Field: "protocol", Message: fmt.Sprintf("unknown protocol: %q", p)}
	}
	return nil
}

// validateDescription is the cheap description check.
// Optional, no max length enforced at the Go layer
// (the DB does not have a CHECK on it either; the
// admin UI limits to 200 chars for display).
func validateDescription(s string) error {
	if utf8.RuneCountInString(s) > 200 {
		return &ValidationError{Field: "description", Message: "must be at most 200 characters"}
	}
	return nil
}

// cloneParams returns a shallow copy of the params
// map. The values are kept as-is (any) — the panel
// does not own the per-protocol schema, and the
// sing-box provider is the authoritative validator.
func cloneParams(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
