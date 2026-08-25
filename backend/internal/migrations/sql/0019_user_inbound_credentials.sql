-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- +migrate Up
--
-- v0.7.x — Phase 2 multi-user sing-box render foundation.
--
-- # Why this table exists
--
-- The v0.4.0 / v0.5.0 / v0.7.2 sing-box renderer is
-- single-user per inbound: the protocol-level `users`
-- array inside the rendered config carries exactly one
-- user (the operator's credential, encoded in
-- `inbounds.params["uuid"]` for VLESS, or
-- `inbounds.params["password"]` for HY2 / Trojan).
-- Multi-user rendering is the next milestone (per
-- ARCHITECTURE.md §7.5 and the v0.7.2 KNOWN_LIMITATIONS
-- "Phase 2 multi-user sing-box render — Phase 2" entry)
-- but the data model has to land first.
--
-- This migration adds the join table that the renderer
-- will eventually read. The table sits empty in this
-- PR; the sing-box renderer, the builder, and the
-- BatchedApplier fan-out all stay on the Phase 1
-- single-credential path. The follow-up PRs
-- (`feat(cores): multi-user sing-box render` and
-- `feat(builder): narrow fan-out to per-user nodes`)
-- will start reading this table. Until those land, the
-- `inbounds.params["uuid"]` / `["password"]` path is
-- unchanged — there is no operator-facing behaviour
-- change in this PR.
--
-- # Schema
--
-- One row per (user, inbound) credential. The credential
-- value is opaque TEXT at the storage layer (the panel
-- does not know the per-protocol shape here; the
-- sing-box renderer validates UUIDs for VLESS,
-- password shape for HY2 / Trojan, etc.). The
-- credential is independent of `users.sub_token` —
-- the sub_token is for the cabinet / subscription
-- surface (HTTP /sub/{token}), the credential is for
-- the sing-box protocol-level auth (VLESS UUID,
-- Shadowsocks 2022-blake3 password, etc.).
--
-- `UNIQUE (user_id, inbound_id)` enforces the
-- one-credential-per-(user, inbound) rule. To rotate,
-- UPDATE the existing row (the Service exposes a
-- `Rotate(ctx, id, newValue)` method that does this
-- in a single transaction). To allow a user to have
-- multiple credentials per inbound (e.g. for a soft
-- rollover window where both old and new passwords
-- are accepted), drop the UNIQUE in a future PR.
--
-- Both FKs use `ON DELETE CASCADE` so a user removal
-- or an inbound removal drops the credential row
-- alongside it. This is the canonical "the credential
-- has no meaning without the entity" semantics.
--
-- The audit_log table tracks create / delete /
-- rotate events via the Service layer (the
-- `credentials.Service.WithAudits(...)` pattern from
-- PR #166). The audit row carries `Action =
-- "credential.create"` / `"credential.delete"` /
-- `"credential.rotate"`, `ResourceType =
-- "credential"`, `ResourceID = credential.id`. The
-- migration does not touch the audit_log schema.
--
-- Indexes:
--
--   - PK on `id` (implicit).
--   - UNIQUE on (user_id, inbound_id) (the data
--     invariant).
--   - `idx_user_inbound_credentials_user_id` for
--     the "list by user" query (the subscription
--     package's resolver will need it when it
--     renders the per-user config).
--   - `idx_user_inbound_credentials_inbound_id`
--     for the "list by inbound" query (the future
--     multi-user builder will need it).

CREATE TABLE user_inbound_credentials (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    inbound_id      UUID NOT NULL REFERENCES inbounds(id) ON DELETE CASCADE,
    credential_value TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, inbound_id)
);

CREATE INDEX idx_user_inbound_credentials_user_id ON user_inbound_credentials(user_id);
CREATE INDEX idx_user_inbound_credentials_inbound_id ON user_inbound_credentials(inbound_id);

-- +migrate Down

DROP TABLE IF EXISTS user_inbound_credentials;
