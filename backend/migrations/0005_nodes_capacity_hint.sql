-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Aegis migration 0005 — add `capacity_hint` to the `nodes` table.
--
-- Why:
--
--   The Go Node model (internal/nodes/node.go) carries a
--   `CapacityHint` field that the operator UI uses to label a
--   node with a human-readable capacity string ("1 Gbps",
--   "1000 users", …). The Phase 0 MemoryStore persisted it
--   implicitly inside the in-memory struct; the Phase 1
--   PgStore needs a real column to round-trip it through
--   PostgreSQL.
--
--   We add it as a new column with `DEFAULT ''` so the
--   existing rows in dev / staging get an empty hint without
--   a backfill. Operators fill it in via the standard
--   nodes.Update path; the value is rendered alongside the
--   node row in the admin UI.
--
-- The other "DB-only" columns on `nodes` (ssh_port, ssh_user,
-- ssh_key_id, core_kind, core_version, agent_version,
-- inbound_set_id, last_heartbeat_at, last_config_revision,
-- drain, health) stay at their migration 0001 defaults and
-- are not read by the Go model in Phase 1. A future migration
-- will wire them up as the agent protocol and the apply path
-- land; the slim Phase 1 PgStore intentionally does not
-- touch them.

BEGIN;

-- +migrate Up

ALTER TABLE nodes ADD COLUMN capacity_hint TEXT NOT NULL DEFAULT '';

-- +migrate Down

ALTER TABLE nodes DROP COLUMN capacity_hint;

COMMIT;
