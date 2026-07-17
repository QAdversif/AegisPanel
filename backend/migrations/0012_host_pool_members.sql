-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Aegis migration 0012 — restore `host_pool_members`.
--
-- Why:
--
--   The `host_pool_members` table was originally created in
--   migration 0001 as the join between `host_pools` and
--   `hosts`. Migration 0004_hosts_v3.sql dropped it (along
--   with the v2 `hosts` table) when it introduced the new
--   `host_endpoints` table to model 1:N endpoints per host.
--
--   The v3 split makes sense for the `hosts` package: each
--   host has many endpoints, each with its own (node,
--   inbound, port) pair. The `host_pool_members` table
--   served a different purpose — it grouped hosts into
--   pools, which is a separate concern owned by the
--   `subscription` package.
--
--   The Go model in `internal/subscription` still has
--   `Pool` and `PoolMember` types that reference
--   `host_pool_members` (the MemoryStore uses it for
--   pool→host lookups; the PgStore in PR #50 queries
--   it for `ListPoolMembers` and `ListPoolsForUser`).
--   After migration 0004 the table is missing, so the
--   subscription PgStore integration tests fail.
--
--   This migration re-creates `host_pool_members` with
--   the v2 schema (from migration 0001, restored in
--   0004's Down body). The schema is intentionally
--   minimal:
--
--     - pool_id, host_id (PRIMARY KEY)
--     - weight (default 1, no CHECK — matches v2)
--
--   Phase 1+ may add a CHECK on `weight > 0` and a
--   strategy-aware column. For now the table is a
--   pure join; the v2 contract is what the Go model
--   expects.
--
-- Why now (in PR #50):
--
--   The subscription PgStore landed in PR #50 as
--   parity for the MemoryStore. The integration tests
--   for `ListPoolMembers`, `ListPoolsForUser`, and
--   `ListPoolsAll` exercise the actual SQL path; a
--   missing table surfaces as a `42P01 relation does
--   not exist` error. Rather than ship a PR with
--   skipped tests or untested code paths, the
--   migration lands in the same PR — the schema
--   restore is a 1-line fix, and the integration
--   tests now run end-to-end.

BEGIN;

-- +migrate Up

-- The `host_pool_members` table was created in 0001
-- and dropped in 0004. The Drop here is defensive:
-- if a future migration accidentally re-drops it,
-- the next apply restores the schema. On a clean
-- database the IF NOT EXISTS short-circuits the
-- CREATE; on a stale state the CREATE produces a
-- fresh table.
CREATE TABLE IF NOT EXISTS host_pool_members (
    pool_id         UUID NOT NULL REFERENCES host_pools(id) ON DELETE CASCADE,
    host_id         UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    weight          INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (pool_id, host_id)
);

-- +migrate Down

DROP TABLE IF EXISTS host_pool_members;

COMMIT;
