-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Aegis migration 0003 — per-node inbounds.
--
-- Introduces the `inbounds` table that realises the
-- v3 model from ARCHITECTURE.md §10: an Inbound is a
-- single protocol listener on a specific node
-- (VLESS-Reality, Hysteria 2, Shadowsocks, …). Hosts
-- reference inbounds via Host.Endpoint.InboundID (the
-- next PR replaces the temporary Endpoint.Protocol
-- string with this FK).
--
-- Why a new table and not the existing inbound_sets:
--
--   - inbound_sets (from 0001) is a *reusable template*
--     concept. A set is a named bundle of inbounds
--     that several nodes can subscribe to.
--   - inbounds (this migration) is a *concrete listener*
--     on a specific node — one row per (Node, port,
--     protocol). Concrete inbounds have their own
--     port / TLS / Reality keys; a template would have
--     defaults that the node's agent substitutes at
--     apply time.
--
-- The two are not redundant: a future PR will add a
-- "render inbound_set for node N" path that copies a
-- template into concrete inbounds. The PR series is
-- small-PRs-only, so the relationship is not modelled
-- yet — only the leaf type lands now.
--
-- See ../ARCHITECTURE.md §10.0 and the comment in
-- internal/hosts/host.go on the planned
-- Protocol → InboundID migration.

BEGIN;

-- +migrate Up

CREATE TABLE inbounds (
    id              UUID PRIMARY KEY,
    node_id         UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,           -- operator label, unique per node
    protocol        TEXT NOT NULL,           -- 'vless' | 'hysteria2' | 'shadowsocks' | 'trojan'
    listen          TEXT NOT NULL DEFAULT '::',
    listen_port     INTEGER NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    tags            JSONB NOT NULL DEFAULT '[]'::JSONB,
    -- params is the protocol-specific configuration
    -- blob (Reality keys, UUIDs, passwords, …). The
    -- Go side stores it as map[string]any; the panel
    -- passes it through to the sing-box provider's
    -- RenderConfig.Experimental["inbound_params"].
    -- The shape is closed by the sing-box provider
    -- for now; a JSON-schema check lands in a later PR.
    params          JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Constraint #1: name unique per node. The admin
    -- UI uses the name as a human-readable label; the
    -- Go side enforces this through the Service
    -- layer; the constraint is the last line of
    -- defence.
    UNIQUE (node_id, name),
    -- Constraint #2: only one inbound per port per
    -- node. Two protocols on the same port would
    -- collide at the OS level; a DB constraint
    -- surfaces the misconfiguration at insert time
    -- rather than at agent-apply time.
    UNIQUE (node_id, listen_port),
    -- Constraint #3: protocol allow-list. Mirrors the
    -- closed set in the sing-box provider so a typo
    -- in a future PR's renderer cannot reach the
    -- agent.
    CHECK (protocol IN ('vless', 'hysteria2', 'shadowsocks', 'trojan')),
    -- Constraint #4: port range. PostgreSQL's INTEGER
    -- already enforces the upper bound at 2^31 - 1;
    -- the lower bound keeps the constraint obvious.
    CHECK (listen_port BETWEEN 1 AND 65535),
    -- Constraint #5: listen must look like a host or
    -- IP. The Go validator is more thorough (it
    -- rejects obviously malformed values); the DB
    -- check is the cheap final guard.
    CHECK (listen <> '')
);

-- Common lookups: list inbounds for a node, filter by
-- protocol. The (node_id, enabled) index also serves
-- the "show only enabled inbounds" admin UI filter.
CREATE INDEX inbounds_node_id_idx     ON inbounds (node_id);
CREATE INDEX inbounds_node_id_enabled ON inbounds (node_id, enabled);
CREATE INDEX inbounds_protocol_idx    ON inbounds (protocol);

-- +migrate Down

DROP INDEX IF EXISTS inbounds_protocol_idx;
DROP INDEX IF EXISTS inbounds_node_id_enabled;
DROP INDEX IF EXISTS inbounds_node_id_idx;
DROP TABLE IF EXISTS inbounds;

COMMIT;
