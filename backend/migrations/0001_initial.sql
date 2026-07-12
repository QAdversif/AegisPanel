-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Aegis initial migration.
-- Creates the core tables for Phase 0:
--   admins, audit_log, nodes, inbounds, hosts, host_pools, users, plans,
--   subscriptions, plan_pool, node_tags, host_pool_members.
--
-- See ../ARCHITECTURE.md §16 for the full data-model specification.

BEGIN;

-- +migrate Up

-- ---------------------------------------------------------------------------
-- Admin accounts & RBAC
-- ---------------------------------------------------------------------------

CREATE TABLE admins (
    id              UUID PRIMARY KEY,
    username        TEXT NOT NULL UNIQUE,
    email           TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,           -- argon2id encoded
    role            TEXT NOT NULL,           -- 'super-admin' | 'operator' | 'viewer'
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (role IN ('super-admin', 'operator', 'viewer'))
);

CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    actor_id        UUID REFERENCES admins(id) ON DELETE SET NULL,
    actor_username  TEXT,
    action          TEXT NOT NULL,           -- 'create_node' | 'update_user' | ...
    resource_type   TEXT NOT NULL,           -- 'node' | 'user' | 'host' | ...
    resource_id     TEXT,
    before          JSONB,
    after           JSONB,
    ip              INET,
    user_agent      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX audit_log_created_at_idx ON audit_log (created_at DESC);
CREATE INDEX audit_log_actor_id_idx    ON audit_log (actor_id);
CREATE INDEX audit_log_resource_idx    ON audit_log (resource_type, resource_id);

-- ---------------------------------------------------------------------------
-- Nodes (BYO)
-- ---------------------------------------------------------------------------

CREATE TABLE nodes (
    id              UUID PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    region          TEXT NOT NULL,
    state           TEXT NOT NULL DEFAULT 'provisioning',
    address         TEXT NOT NULL,           -- public IP or domain
    ssh_port        INTEGER NOT NULL DEFAULT 22,
    ssh_user        TEXT NOT NULL DEFAULT 'root',
    ssh_key_id      UUID,                   -- ref to encrypted secrets store
    core_kind       TEXT NOT NULL DEFAULT 'sing-box',
    core_version    TEXT,
    agent_version   TEXT,
    inbound_set_id  UUID,
    last_heartbeat_at TIMESTAMPTZ,
    last_config_revision BIGINT,
    drain           BOOLEAN NOT NULL DEFAULT FALSE,
    health          JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (state IN ('provisioning', 'active', 'degraded', 'suspended', 'decommissioned'))
);
CREATE INDEX nodes_state_idx ON nodes (state);
CREATE INDEX nodes_region_idx ON nodes (region);

CREATE TABLE node_tags (
    node_id         UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    tag             TEXT NOT NULL,
    PRIMARY KEY (node_id, tag)
);

-- ---------------------------------------------------------------------------
-- Inbounds / Cores
-- ---------------------------------------------------------------------------

CREATE TABLE cores (
    id              UUID PRIMARY KEY,
    kind            TEXT NOT NULL,           -- 'sing-box' | 'xray' | 'hysteria2' | ...
    version         TEXT,
    capabilities    JSONB NOT NULL DEFAULT '[]'::JSONB,
    is_default      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE inbound_sets (
    id              UUID PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    core_id         UUID NOT NULL REFERENCES cores(id) ON DELETE RESTRICT,
    raw_template    JSONB NOT NULL,          -- template body
    schema_ref      TEXT,                   -- JSON-schema $ref
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE inbound_revisions (
    id              BIGSERIAL PRIMARY KEY,
    inbound_set_id  UUID NOT NULL REFERENCES inbound_sets(id) ON DELETE CASCADE,
    revision        BIGINT NOT NULL,
    raw_rendered    JSONB NOT NULL,
    applied_at      TIMESTAMPTZ,
    applied_by      UUID REFERENCES admins(id) ON DELETE SET NULL,
    result          TEXT,                   -- 'ok' | 'failed' | 'rolled_back'
    comment         TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (inbound_set_id, revision)
);

-- ---------------------------------------------------------------------------
-- Hosts & Pools
-- ---------------------------------------------------------------------------

CREATE TABLE hosts (
    id              UUID PRIMARY KEY,
    remark          TEXT NOT NULL,           -- display name; supports format variables
    type            TEXT NOT NULL DEFAULT 'direct',
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    priority        INTEGER NOT NULL DEFAULT 100,
    status_filter   JSONB NOT NULL DEFAULT '[]'::JSONB, -- user statuses
    node_id         UUID NOT NULL REFERENCES nodes(id) ON DELETE RESTRICT,
    inbound_id      UUID,                    -- ref to inbounds (TBD in Phase 1)
    address         JSONB NOT NULL DEFAULT '[]'::JSONB,   -- [string, ...]
    port            INTEGER,
    sni             JSONB NOT NULL DEFAULT '[]'::JSONB,
    host            JSONB NOT NULL DEFAULT '[]'::JSONB,
    path            TEXT,
    security        TEXT,
    alpn            JSONB NOT NULL DEFAULT '[]'::JSONB,
    fingerprint     TEXT,
    transport_settings JSONB NOT NULL DEFAULT '{}'::JSONB,
    http_headers    JSONB NOT NULL DEFAULT '{}'::JSONB,
    balancer        JSONB,                   -- present when type='balancer'
    chain           JSONB,                   -- present when type='chain' (Phase 4+)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (type IN ('direct', 'balancer', 'chain'))
);
CREATE INDEX hosts_node_id_idx ON hosts (node_id);
CREATE INDEX hosts_enabled_idx  ON hosts (enabled);

CREATE TABLE host_pools (
    id              UUID PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    strategy        TEXT NOT NULL DEFAULT 'all',  -- 'all' | 'round_robin' | 'least_loaded' | 'geo_aware'
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

-- ---------------------------------------------------------------------------
-- Users, Plans, Subscriptions
-- ---------------------------------------------------------------------------

CREATE TABLE users (
    id              UUID PRIMARY KEY,
    external_id     TEXT,                   -- ID from external Cabinet
    username        TEXT NOT NULL UNIQUE,
    status          TEXT NOT NULL DEFAULT 'active',
    plan_id         UUID,
    expire_at       TIMESTAMPTZ,
    traffic_limit_bytes BIGINT NOT NULL DEFAULT 0,
    traffic_used_bytes  BIGINT NOT NULL DEFAULT 0,
    last_reset_at   TIMESTAMPTZ,
    device_limit    INTEGER NOT NULL DEFAULT 0,
    hosts_allowlist JSONB NOT NULL DEFAULT '[]'::JSONB,
    hosts_blocklist JSONB NOT NULL DEFAULT '[]'::JSONB,
    sub_token       TEXT NOT NULL UNIQUE,
    sub_token_rotated_at TIMESTAMPTZ,
    telegram_id     BIGINT,
    email           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (status IN ('active', 'grace', 'disabled', 'expired', 'deleted'))
);
CREATE INDEX users_status_idx   ON users (status);
CREATE INDEX users_plan_id_idx  ON users (plan_id);
CREATE INDEX users_sub_token_idx ON users (sub_token);

CREATE TABLE plans (
    id              UUID PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    traffic_limit_bytes BIGINT NOT NULL DEFAULT 0,
    duration        INTERVAL NOT NULL,
    device_limit    INTEGER NOT NULL DEFAULT 0,
    reset_period    TEXT NOT NULL DEFAULT 'monthly',  -- 'daily' | 'weekly' | 'monthly' | 'never'
    price_cents     BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE plan_pool (
    plan_id         UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    pool_id         UUID NOT NULL REFERENCES host_pools(id) ON DELETE CASCADE,
    PRIMARY KEY (plan_id, pool_id)
);

-- ---------------------------------------------------------------------------
-- Webhooks (NEW — see ARCHITECTURE.md §13.4)
-- ---------------------------------------------------------------------------

CREATE TABLE webhook_endpoints (
    id              UUID PRIMARY KEY,
    url             TEXT NOT NULL,
    secret          TEXT NOT NULL,           -- HMAC secret (encrypted at rest)
    events          JSONB NOT NULL DEFAULT '[]'::JSONB,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    last_delivery_at TIMESTAMPTZ,
    last_status_code INTEGER,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- Panel-path secrets (decoy / random URL prefixes, ARCHITECTURE.md §26)
-- ---------------------------------------------------------------------------

CREATE TABLE panel_path_config (
    id              INTEGER PRIMARY KEY DEFAULT 1,
    admin_path      TEXT NOT NULL,           -- e.g. '/s3cr3t-p4n3l-7a8b9c'
    sub_path        TEXT NOT NULL,           -- e.g. '/s3cr3t-sub-d4e5f6'
    path_rotated_at TIMESTAMPTZ,
    CHECK (id = 1)
);

-- +migrate Down

DROP TABLE IF EXISTS panel_path_config;
DROP TABLE IF EXISTS webhook_endpoints;
DROP TABLE IF EXISTS plan_pool;
DROP TABLE IF EXISTS plans;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS host_pool_members;
DROP TABLE IF EXISTS host_pools;
DROP TABLE IF EXISTS hosts;
DROP TABLE IF EXISTS inbound_revisions;
DROP TABLE IF EXISTS inbound_sets;
DROP TABLE IF EXISTS cores;
DROP TABLE IF EXISTS node_tags;
DROP TABLE IF EXISTS nodes;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS admins;

COMMIT;
