// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package inboundtemplates implements the panel-side CRUD for
// the `inbound_templates` table (migration
// 0021_inbound_templates.sql).
//
// # Model
//
// An InboundTemplate is a *named, reusable* protocol
// configuration that any number of `inbounds` rows on
// any node can reference via the new nullable FK
// `inbounds.template_id`. The template carries the
// protocol-specific configuration blob the sing-box
// provider's RenderConfig already understands
// (Reality keys, UUIDs, passwords, …) — the same
// shape as `inbounds.params`, but factored out so the
// operator does not paste the same JSON into every
// inbound.
//
// Templates are global (not per-node) — a VLESS-Reality
// template on 8443 with the same Reality keys can back
// twenty inbounds on twenty different nodes. The
// UNIQUE (name) constraint enforces one-name-per-panel
// (the templates are shared resources).
//
// # Relationship to inbounds.params
//
// v0.8.0-v0.8.12 every inbound carries its own
// `params` JSONB. v0.8.13+ inbounds can opt into a
// template: when `template_id` is non-NULL, the
// sing-box renderer reads `template.params` instead
// of `inbound.params`. The inline `params` field is
// kept on the inbound for backwards compatibility —
// existing inbounds without a template_id are
// untouched, and the renderer picks one or the other
// at apply time.
//
// The per-user credential from
// `user_inbound_credentials` is still layered on top
// in the multi-user render — the template is the
// "shared protocol config", the per-user row is the
// "per-user auth credential". They do not collide
// (the multi-user render replaces the template's
// `uuid` / `password` keys with the per-user value;
// the rest of the params flow through unchanged).
package inboundtemplates

import (
	"time"

	"github.com/google/uuid"
)

// Protocol is the protocol family of an
// InboundTemplate. The string values match the
// sing-box provider's per-protocol renderers
// (internal/cores/singbox/) and are pinned by the DB
// CHECK constraint in migration
// 0021_inbound_templates.sql.
//
// We import the inbounds.Protocol type for the
// closed-set guarantee (the templates share the same
// set of allowed protocols as the inbounds that
// reference them) but expose it as our own
// re-typed alias so callers don't need the inbounds
// import just to construct a template.
type Protocol string

// Protocol values. Closed set — any value outside
// this list is rejected at the Service boundary.
const (
	ProtocolVLESS       Protocol = "vless"
	ProtocolHysteria2   Protocol = "hysteria2"
	ProtocolShadowsocks Protocol = "shadowsocks"
	ProtocolTrojan      Protocol = "trojan"
)

// InboundTemplate is the panel-side view of one row
// in the `inbound_templates` table. Fields mirror
// the table one-to-one; we keep them as a Go struct
// rather than `map[string]any` so handlers can rely
// on the type at compile time.
type InboundTemplate struct {
	ID uuid.UUID `json:"id"`
	// Name is the operator's human-readable label.
	// The DB UNIQUE (name) constraint enforces
	// one-name-per-panel.
	Name string `json:"name"`
	// Protocol is the protocol family. Closed set
	// (see the Protocol constants above). An inbound
	// that references this template must carry the
	// same protocol value (the Service layer
	// enforces the constraint at the inbounds CRUD
	// boundary; the DB does not).
	Protocol Protocol `json:"protocol"`
	// Params is the protocol-specific configuration
	// blob. The Go side stores it as map[string]any
	// because the per-protocol schema is owned by
	// the sing-box provider and would otherwise
	// duplicate the renderer's parameters. The
	// service validator does not enforce a shape
	// here; the sing-box provider's RenderConfig is
	// the authoritative schema check.
	Params map[string]any `json:"params"`
	// Description is operator-supplied free-form
	// notes ("VLESS-Reality for the EU fleet, rotated
	// quarterly"). Optional. The admin UI surfaces
	// it as a placeholder; no operator workflow
	// reads it.
	Description string `json:"description,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// IsValid reports whether the template carries the
// minimum data the Service layer requires to accept
// it. It is intentionally permissive — heavy
// validation (cross-entity, length, format) lives in
// `Service.Create` so it can return rich per-field
// errors. IsValid is the cheap pre-flight check used
// by the store to reject obviously broken inserts.
func (t *InboundTemplate) IsValid() bool {
	if t == nil || t.Name == "" {
		return false
	}
	if t.Protocol == "" {
		return false
	}
	return true
}
