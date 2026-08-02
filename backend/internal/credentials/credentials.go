// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package credentials is the Phase 2 multi-user
// sing-box render data model: per-(user, inbound)
// credentials, independent of `inbounds.params["uuid"]` /
// `["password"]` which still carries the Phase 1
// single-operator credential.
//
// # Why this package exists (separate from `internal/users`)
//
// The `users` package owns the end-user CRUD surface
// (the `users` table, the `User` model, the
// sub_token rotation, etc.). The `inbounds` package
// owns the per-node inbound CRUD surface. The
// `credentials` package owns the join — the
// per-(user, inbound) credential the sing-box
// renderer will eventually read.
//
// Splitting it out keeps the dependency graph clean:
// the `users` and `inbounds` packages do not need to
// know about the credential table; the credentials
// package is the only thing that owns the
// (user, inbound, credential_value) relationship.
//
// # Phase 1 vs Phase 2
//
// In v0.7.2 and earlier, the sing-box renderer reads
// `inbounds.params["uuid"]` (VLESS) or
// `inbounds.params["password"]` (HY2 / Trojan) and
// emits a `users: [{name, uuid}]` array of length 1
// in the rendered config. The BatchedApplier fan-out
// (PR #157) re-renders the full node config on any
// user or inbound change, so the rendered config is
// always "the operator's credential on every inbound
// on the node" — a single-tenant model.
//
// The Phase 2 multi-user work (ARCHITECTURE.md §7.5,
// the v0.7.2 KNOWN_LIMITATIONS "Phase 2 multi-user
// sing-box render — Phase 2" entry) needs:
//   1. **A join table** that maps (user, inbound) to
//      credential. This PR.
//   2. **A renderer change** that accepts a list of
//      users instead of a single user from
//      `inbounds.params`. Deferred to a follow-up PR.
//   3. **A builder change** that queries this table
//      and filters by the user's `hosts_allowlist` /
//      `hosts_blocklist` to produce the per-inbound
//      `users` list. Deferred.
//   4. **A BatchedApplier narrow** that fans out a
//      `DeltaAddUser` to ONLY the nodes matching the
//      user's host allowlist, not every node. Deferred.
//
// Until steps 2-4 land, this table is dead weight
// (no read path queries it). The table sits empty
// in this PR; the sing-box renderer, the builder,
// and the BatchedApplier fan-out all stay on the
// Phase 1 single-credential path. The follow-up
// PRs that wire this table in are tracked in
// KNOWN_LIMITATIONS.
//
// # What "credential" means
//
// The `credential_value` column is opaque TEXT. The
// panel does not know the per-protocol shape here —
// the sing-box renderer validates the value (UUID
// format for VLESS, password length for HY2 / Trojan,
// method tag for Shadowsocks 2022-blake3) when it
// builds the `users` array for a specific inbound.
// The panel stores whatever the admin / operator
// provides; the renderer decides whether it is
// usable.
//
// Independence from `users.sub_token`: the
// sub_token is for the cabinet / subscription
// surface (HTTP /sub/{token} resolves a sub_token
// to a user, then renders a per-user config that
// lists every inbound the user has access to). The
// credential here is for the sing-box protocol-level
// auth (VLESS UUID, Shadowsocks 2022-blake3
// password, etc.). A future PR may auto-derive
// credentials from the sub_token (e.g.
// `uuid = sha256(sub_token + inbound_id)`) to
// avoid a separate admin CRUD surface, but that
// decision is the inbound-templates work, not
// this PR.

package credentials

import (
	"time"

	"github.com/google/uuid"
)

// Credential is the per-(user, inbound) credential
// the sing-box renderer reads in Phase 2. The JSON
// tags are snake_case to match the rest of the
// panel's wire format (the `internal/users` and
// `internal/inbounds` packages both use snake_case).
//
// `CredentialValue` is the opaque per-protocol
// credential. The panel stores it as TEXT; the
// sing-box renderer validates the shape at render
// time. Empty strings are rejected by the Service
// layer (see `validateCredential`).
//
// `CreatedAt` / `UpdatedAt` are the standard
// audit-trail timestamps. The Service layer uses
// `Rotate` to UPDATE the value; `UpdatedAt` is
// re-stamped by the DB on every UPDATE (the
// pgx-side ON UPDATE NOW() trigger from migration
// 0001 covers both columns uniformly).
type Credential struct {
	ID              uuid.UUID `json:"id"`
	UserID          uuid.UUID `json:"user_id"`
	InboundID       uuid.UUID `json:"inbound_id"`
	CredentialValue string    `json:"credential_value"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// IsValid reports whether c carries the minimum
// data the Store requires to accept an insert. The
// heavy validation (UUID format of the FK columns,
// the credential_value format itself) lives in the
// Service layer. IsValid is the cheap pre-flight
// the store uses to reject obviously broken rows.
func (c *Credential) IsValid() bool {
	if c == nil {
		return false
	}
	if c.UserID == uuid.Nil {
		return false
	}
	if c.InboundID == uuid.Nil {
		return false
	}
	if c.CredentialValue == "" {
		return false
	}
	return true
}
