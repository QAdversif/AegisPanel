-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- +migrate Up
--
-- v0.8.x — inbound templates: per-tenant `Params` defaults.
--
-- # Why this table exists
--
-- Every `inbounds` row today carries its own `params`
-- JSONB blob (the protocol-specific configuration
-- the sing-box provider's RenderConfig understands:
-- Reality keys, UUIDs, passwords, …). The
-- multi-user sing-box render (v0.8.0+) reads the
-- per-(user, inbound) credential from
-- `user_inbound_credentials`; the inbound's `params`
-- is otherwise shared across every user that lands
-- on that inbound.
--
-- For a real deployment, the operator often wants to
-- spin up the same inbound shape on many nodes
-- (VLESS-Reality on 8443, with the same Reality keys
-- and flow, on every edge node). Today they have to
-- paste the same JSON blob into every inbound's
-- `params` field. A typo in one node breaks one
-- node, silently. A rotation has to touch every
-- node by hand.
--
-- The `inbound_templates` table is the answer: one
-- named JSON-blob record that any number of
-- `inbounds` rows reference via the new nullable FK
-- `inbounds.template_id`. The renderer reads the
-- template's `params` (not the inbound's inline
-- `params`) when the FK is set. Existing inbounds
-- without a template_id are untouched — the
-- inline-params path is the v0.8.0-v0.8.12 default
-- and stays the documented fallback.
--
-- # Schema
--
-- `id` UUID PK; `name` TEXT UNIQUE (the operator's
-- human-readable label, like `inbounds.name` per
-- node); `protocol` TEXT with the same closed-set
-- CHECK constraint as `inbounds.protocol` (the
-- template's protocol constrains which inbounds can
-- reference it — a VLESS template cannot be paired
-- with a HY2 inbound); `params` JSONB (the same shape
-- as `inbounds.params`); `description` TEXT NULL
-- (operator notes, optional); `created_at` /
-- `updated_at` TIMESTAMPTZ.
--
-- The `inbounds.template_id` FK is NULLABLE for
-- backwards compatibility: existing inbounds keep
-- their inline `params`, the new column is added
-- with a default of NULL, and the renderer picks
-- between `template.params` and `inbound.params`
-- at apply time. `ON DELETE SET NULL` so deleting a
-- template does not cascade-delete the inbounds
-- that referenced it — the inbounds fall back to
-- the inline-params path and the operator can
-- re-attach or migrate at their pace.
--
-- # Indexes
--
-- - PK on `id` (implicit).
-- - UNIQUE on `name` (the operator-facing identity).
-- - `inbound_templates_protocol_idx` for the
--   "list by protocol" admin UI filter.
-- - `inbounds_template_id_idx` for the
--   "which inbounds use this template" admin UI
--   filter and the renderer's
--   "expand template_id → params" hot path.
--
-- # Wire / render contract
--
-- The sing-box renderer's `BuildCoreConfigForNode`
-- (internal/cores/builder) iterates over the
-- node's inbounds; for each inbound with
-- `template_id != nil` it looks up the template and
-- substitutes `template.params` for the inbound's
-- inline `params`. The per-user credential from
-- `user_inbound_credentials` is still layered on
-- top in the multi-user render — the template is
-- the "shared protocol config", the per-user row
-- is the "per-user auth credential". They do not
-- collide (the multi-user render replaces the
-- template's `uuid` / `password` keys with the
-- per-user value; the rest of the params flow
-- through unchanged).

BEGIN;

CREATE TABLE inbound_templates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    protocol        TEXT NOT NULL,
    params          JSONB NOT NULL DEFAULT '{}'::JSONB,
    description     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- One name per panel (the templates are global —
    -- any inbound on any node can reference any
    -- template). Mirrors the "templates are shared,
    -- inbounds are per-node" model.
    UNIQUE (name),
    -- Closed-set protocol allow-list, matching
    -- inbounds.protocol and the sing-box provider's
    -- renderer set.
    CHECK (protocol IN ('vless', 'hysteria2', 'shadowsocks', 'trojan')),
    CHECK (name <> '')
);

CREATE INDEX inbound_templates_protocol_idx ON inbound_templates (protocol);

-- Add the FK to inbounds. NULLABLE for backwards
-- compat: every existing inbound keeps its inline
-- params, the FK is added with default NULL. The
-- renderer picks `template.params` over
-- `inbound.params` when the FK is set; existing
-- inbounds are unaffected.
ALTER TABLE inbounds ADD COLUMN template_id UUID REFERENCES inbound_templates(id) ON DELETE SET NULL;

CREATE INDEX inbounds_template_id_idx ON inbounds (template_id);

-- +migrate Down

DROP INDEX IF EXISTS inbounds_template_id_idx;
ALTER TABLE IF EXISTS inbounds DROP COLUMN IF EXISTS template_id;
DROP INDEX IF EXISTS inbound_templates_protocol_idx;
DROP TABLE IF EXISTS inbound_templates;

COMMIT;
