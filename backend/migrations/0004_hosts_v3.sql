-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Aegis migration 0004 — Host model v3.
--
-- Replaces the v2 `hosts` table (from migration 0001)
-- with the v3 schema that realises the bundle-of-endpoints
-- model introduced in ARCHITECTURE.md §10 and shipped
-- in PR #33 (the Go model) and PR #34 (per-node
-- inbounds). The migration drops the old single-node,
-- no-endpoints schema and recreates it in the new shape.
--
-- Why DROP + CREATE rather than ALTER:
--
--   - Phase 1 has no production data; the old `hosts`
--     table was never wired to the Go code.
--   - The new schema is materially different: most
--     columns move from `hosts` to a new
--     `host_endpoints` table, and the old `hosts`
--     columns (security, transport_settings, …) are
--     deferred to Phase 2 (subscription service).
--   - The migration's `Down` body restores the v2
--     schema for any environment that needs to roll
--     back.
--
-- Cross-entity invariant
-- ----------------------
-- `host_endpoints.inbound_id` references `inbounds.id`
-- (PR #34). The Service layer enforces
-- `host_endpoints.node_id = inbounds.node_id`; the DB
-- cannot express this as a CHECK constraint because
-- PostgreSQL does not allow subqueries in CHECK, and
-- a trigger would obscure the validation. The
-- application-side check is the canonical guard.
--
-- See ../ARCHITECTURE.md §10 and the comment in
-- internal/hosts/host.go on the planned
-- Protocol → InboundID migration.

BEGIN;

-- +migrate Up

-- Drop the v2 hosts table. The schema below is
-- incompatible (v2 had a single node_id and a single
-- port per host; v3 has many endpoints with their own
-- node/inbound/port), so the cleanest path is DROP +
-- CREATE rather than ALTER.
--
-- PostgreSQL refuses to DROP a table that other
-- objects depend on (SQLSTATE 2BP01). The v2 hosts
-- table has inbound references from:
--
--   - host_pool_members.host_id (FK ON DELETE CASCADE,
--     from migration 0001)
--   - hosts_node_id_idx, hosts_enabled_idx (indexes
--     from 0001; auto-drop with the table, but listed
--     here for clarity)
--
-- We drop host_pool_members first (it is empty in
-- Phase 0 anyway), then the table. The Down body
-- restores the v2 chain in reverse.
DROP TABLE IF EXISTS host_pool_members;
DROP INDEX IF EXISTS hosts_enabled_idx;
DROP INDEX IF EXISTS hosts_node_id_idx;
DROP TABLE IF EXISTS hosts;

CREATE TABLE hosts (
    id              UUID PRIMARY KEY,
    remark          TEXT NOT NULL,
    type            TEXT NOT NULL DEFAULT 'direct',
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    priority        INTEGER NOT NULL DEFAULT 0,
    status_filter   JSONB NOT NULL DEFAULT '[]'::JSONB,
    country         TEXT NOT NULL DEFAULT '',
    city            TEXT NOT NULL DEFAULT '',
    tags            JSONB NOT NULL DEFAULT '[]'::JSONB,
    balancer        JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- v3 type allow-list. 'chain' is Phase 4+
    -- (cascade topology) and deliberately not
    -- included here.
    CHECK (type IN ('direct', 'balancer')),
    -- 'direct' requires a single endpoint; 'balancer'
    -- requires >=2. The Service layer enforces this
    -- on the host_endpoints table; we cannot put it
    -- in a CHECK because the count is on a different
    -- table.
    CHECK (priority BETWEEN -32768 AND 32767)
);

CREATE INDEX hosts_enabled_idx  ON hosts (enabled);
CREATE INDEX hosts_priority_idx ON hosts (priority);

-- A host's endpoints live in their own table.
-- Endpoints are addressed by their own UUID
-- (Host.Endpoints[].ID) so the Balancer
-- .FailoverEndpointIDs can reference them across
-- rows.
CREATE TABLE host_endpoints (
    id              UUID PRIMARY KEY,
    host_id         UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    node_id         UUID NOT NULL REFERENCES nodes(id) ON DELETE RESTRICT,
    inbound_id      UUID NOT NULL REFERENCES inbounds(id) ON DELETE RESTRICT,
    weight          INTEGER NOT NULL DEFAULT 1,
    -- Override layer (per-endpoint). The Host
    -- model also has Host-level defaults in
    -- Phase 2; for now overrides live only on the
    -- endpoint. The sing-box provider reads them
    -- at render time.
    address         JSONB NOT NULL DEFAULT '[]'::JSONB,
    port            INTEGER,
    sni             JSONB NOT NULL DEFAULT '[]'::JSONB,
    host            JSONB NOT NULL DEFAULT '[]'::JSONB,
    path            TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (weight > 0),
    CHECK (port IS NULL OR port BETWEEN 1 AND 65535),
    CHECK (path <> '')
);

CREATE INDEX host_endpoints_host_id_idx    ON host_endpoints (host_id);
CREATE INDEX host_endpoints_node_id_idx    ON host_endpoints (node_id);
CREATE INDEX host_endpoints_inbound_id_idx ON host_endpoints (inbound_id);

-- +migrate Down

DROP INDEX IF EXISTS host_endpoints_inbound_id_idx;
DROP INDEX IF EXISTS host_endpoints_node_id_idx;
DROP INDEX IF EXISTS host_endpoints_host_id_idx;
DROP TABLE IF EXISTS host_endpoints;
DROP INDEX IF EXISTS hosts_priority_idx;
DROP INDEX IF EXISTS hosts_enabled_idx;
DROP TABLE IF EXISTS hosts;

-- Restore the v2 `hosts` table and its dependents
-- from migration 0001. The chain is host_pool_members
-- → host_pools → nodes (host_pools.node_id); restoring
-- the dependent tables first keeps PostgreSQL happy
-- on the way back. The FKs are ON DELETE CASCADE from
-- host_pool_members to hosts and host_pools, so the
-- reverse order is host_pool_members → host_pools →
-- hosts.
CREATE TABLE hosts (
    id              UUID PRIMARY KEY,
    remark          TEXT NOT NULL,
    type            TEXT NOT NULL DEFAULT 'direct',
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    priority        INTEGER NOT NULL DEFAULT 100,
    status_filter   JSONB NOT NULL DEFAULT '[]'::JSONB,
    node_id         UUID NOT NULL REFERENCES nodes(id) ON DELETE RESTRICT,
    inbound_id      UUID,
    address         JSONB NOT NULL DEFAULT '[]'::JSONB,
    port            INTEGER,
    sni             JSONB NOT NULL DEFAULT '[]'::JSONB,
    host            JSONB NOT NULL DEFAULT '[]'::JSONB,
    path            TEXT,
    security        TEXT,
    alpn            JSONB NOT NULL DEFAULT '[]'::JSONB,
    fingerprint     TEXT,
    transport_settings JSONB NOT NULL DEFAULT '{}'::JSONB,
    http_headers    JSONB NOT NULL DEFAULT '{}'::JSONB,
    balancer        JSONB,
    chain           JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (type IN ('direct', 'balancer', 'chain'))
);
CREATE INDEX hosts_node_id_idx ON hosts (node_id);
CREATE INDEX hosts_enabled_idx  ON hosts (enabled);

CREATE TABLE host_pools (
    id              UUID PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    strategy        TEXT NOT NULL DEFAULT 'all',
    antiaffinity    BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE host_pool_members (
    pool_id         UUID NOT NULL REFERENCES host_pools(id) ON DELETE CASCADE,
    host_id         UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    weight          INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (pool_id, host_id)
);

-- Restore the v2 `hosts` table from migration 0001.
-- This is the verbatim block that 0001 had; the
-- operator running `aegis migrate down 0004` is
-- expected to then `aegis migrate down 0003` (and
-- further) to fully reverse.
CREATE TABLE hosts (
    id              UUID PRIMARY KEY,
    remark          TEXT NOT NULL,
    type            TEXT NOT NULL DEFAULT 'direct',
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    priority        INTEGER NOT NULL DEFAULT 100,
    status_filter   JSONB NOT NULL DEFAULT '[]'::JSONB,
    node_id         UUID NOT NULL REFERENCES nodes(id) ON DELETE RESTRICT,
    inbound_id      UUID,
    address         JSONB NOT NULL DEFAULT '[]'::JSONB,
    port            INTEGER,
    sni             JSONB NOT NULL DEFAULT '[]'::JSONB,
    host            JSONB NOT NULL DEFAULT '[]'::JSONB,
    path            TEXT,
    security        TEXT,
    alpn            JSONB NOT NULL DEFAULT '[]'::JSONB,
    fingerprint     TEXT,
    transport_settings JSONB NOT NULL DEFAULT '{}'::JSONB,
    http_headers    JSONB NOT NULL DEFAULT '{}'::JSONB,
    balancer        JSONB,
    chain           JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (type IN ('direct', 'balancer', 'chain'))
);
CREATE INDEX hosts_node_id_idx ON hosts (node_id);
CREATE INDEX hosts_enabled_idx  ON hosts (enabled);

COMMIT;
