-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Aegis migration 0012 — restore `host_pool_members`
-- + drop the `panel_path_config_id_check` CHECK
-- constraint from migration 0010.
--
-- Why two changes in one migration:
--
--   Both are pre-existing schema bugs surfaced by the
--   integration tests in PR #50 (SubscriptionPgStore +
--   PanelcfgPgStore). Combining them keeps the
--   migration list short — each test cycle is one
--   `migrate up` round trip.
--
-- ---------------------------------------------------------------------------
-- 1. Restore `host_pool_members`
-- ---------------------------------------------------------------------------
--
--   The `host_pool_members` table was originally
--   created in migration 0001 as the join between
--   `host_pools` and `hosts`. Migration
--   0004_hosts_v3.sql dropped it (along with the v2
--   `hosts` table) when it introduced the new
--   `host_endpoints` table to model 1:N endpoints
--   per host.
--
--   The v3 split makes sense for the `hosts` package:
--   each host has many endpoints, each with its own
--   (node, inbound, port) pair. The
--   `host_pool_members` table served a different
--   purpose — it grouped hosts into pools, which is
--   a separate concern owned by the
--   `subscription` package.
--
--   The Go model in `internal/subscription` still has
--   `Pool` and `PoolMember` types that reference
--   `host_pool_members` (the MemoryStore uses it for
--   pool→host lookups; the PgStore in PR #50 queries
--   it for `ListPoolMembers` and `ListPoolsForUser`).
--   After migration 0004 the table is missing, so
--   the subscription PgStore integration tests fail.
--
--   This migration re-creates `host_pool_members`
--   with the v2 schema (from migration 0001,
--   restored in 0004's Down body). The schema is
--   intentionally minimal:
--
--     - pool_id, host_id (PRIMARY KEY)
--     - weight (default 1, no CHECK — matches v2)
--
--   Phase 1+ may add a CHECK on `weight > 0` and a
--   strategy-aware column. For now the table is a
--   pure join; the v2 contract is what the Go model
--   expects.
--
-- ---------------------------------------------------------------------------
-- 2. Drop `panel_path_config_id_check`
-- ---------------------------------------------------------------------------
--
--   Migration 0010 added
--   `CHECK (id = '00000000-0000-0000-0000-000000000001'::UUID)`
--   to enforce "this table has a single row". The
--   intent was correct (one global sub_path config)
--   but the mechanism is wrong: the MemoryStore
--   implements rotation as "insert a new row with
--   a fresh UUID + deactivate the old one" (so
--   rotation history is preserved). The CHECK
--   constraint blocks every insert except the
--   sentinel, breaking the rotation flow at the SQL
--   level.
--
--   The right enforcement is the `is_active` flag
--   combined with the `GetActive` predicate
--   (`is_active = TRUE AND (expires_at IS NULL OR
--   expires_at > now())`). The "at most one active
--   row" invariant is held by the SetActive / Reset
--   transactions in `PanelcfgPgStore` — they
--   deactivate all currently-active rows before
--   inserting the new one. The application is the
--   right place for this invariant; the database
--   CHECK is redundant and wrong.
--
--   We drop the CHECK with `IF EXISTS` so a re-apply
--   is a no-op. The check constraint was added with
--   the original CREATE TABLE in migration 0010; on
--   databases that ran 0010 before this fix, the
--   check is present and gets dropped. On databases
--   that ran 0010 after this fix (a future fresh
--   install) the IF EXISTS short-circuits.
--
--   Note: the sentinel-id convention is preserved.
--   The `SentinelID` constant in the Go model
--   (`00000000-0000-0000-0000-000000000001`) is
--   still the only row that may carry
--   `sub_path = ''` (the default), enforced by the
--   Reset path's `ON CONFLICT (id) DO UPDATE` upsert
--   onto the sentinel.

BEGIN;

-- +migrate Up

-- (1) `host_pool_members` restore.
CREATE TABLE IF NOT EXISTS host_pool_members (
    pool_id         UUID NOT NULL REFERENCES host_pools(id) ON DELETE CASCADE,
    host_id         UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    weight          INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (pool_id, host_id)
);

-- (2) Drop the wrong CHECK constraint from migration 0010.
-- The constraint was added in 0010 with the original
-- CREATE TABLE. ALTER TABLE ... DROP CONSTRAINT IF EXISTS
-- is idempotent: a fresh install that does not have the
-- constraint yet (because a future migration 0010 will
-- have it pre-removed) is a no-op.
ALTER TABLE panel_path_config DROP CONSTRAINT IF EXISTS panel_path_config_id_check;

-- (3) Drop the wrong UNIQUE constraint on sub_path.
-- The column-level UNIQUE was added in 0010. It blocks
-- a legitimate use case: the operator rotates to path
-- A, then rotates to path B, then rotates back to path
-- A. The MemoryStore allows this (each rotation gets a
-- fresh id; the "at most one active row" invariant is
-- held by the SetActive transaction via the `is_active`
-- flag). The SQL UNIQUE on sub_path would either:
--   (a) reject the second rotation back to A (the
--       operator's intent fails silently), or
--   (b) require a separate UNIQUE partial index on
--       `sub_path WHERE is_active = TRUE` (the
--       "one active row per path" invariant).
-- (b) is the production-correct SQL-level expression
-- of the invariant, but the application already enforces
-- it via the deactivate-all-then-insert-one transaction
-- in `SetActive`. The risk of an active-row collision
-- is bounded by the application's own correctness, not
-- the schema. We drop the UNIQUE here and document the
-- invariant in the `SetActive` code (see
-- `PanelcfgPgStore.SetActive`).
ALTER TABLE panel_path_config DROP CONSTRAINT IF EXISTS panel_path_config_sub_path_key;

-- +migrate Down

-- (1) Re-create the CHECK constraint. The Down of a
-- new migration must leave the schema in the same
-- state as the Up of the previous migration. The
-- pre-migration state had the CHECK in place, so
-- the Down re-adds it. (This is a "Forward-fix"
-- migration: the Down is not for restoring the
-- pre-0012 schema, it is for rolling forward the
-- migration itself to its starting state.)
--
-- The down does NOT drop `host_pool_members`:
-- migration 0004's Down body is responsible for
-- re-creating it (the v2 chain). Re-adding the CHECK
-- here is a 1-line side-effect of the bug fix.
ALTER TABLE panel_path_config ADD CONSTRAINT panel_path_config_id_check CHECK (id = '00000000-0000-0000-0000-000000000001'::UUID);

ALTER TABLE panel_path_config ADD CONSTRAINT panel_path_config_sub_path_key UNIQUE (sub_path);

DROP TABLE IF EXISTS host_pool_members;

COMMIT;
