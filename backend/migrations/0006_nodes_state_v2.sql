-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Aegis migration 0006 — align `nodes.state` with the Go model.
--
-- Why:
--
--   The Phase 0 Go model (internal/nodes/node.go) uses the
--   operator-facing lifecycle:
--
--     new         — registered but not yet bootstrapped
--     online      — agent reported healthy recently
--     draining    — out of rotation, no new users
--     offline     — agent unreachable
--     disabled    — operator disabled
--
--   The original schema (migration 0001) defined a
--   different, more enterprise-y set on the CHECK
--   constraint:
--
--     provisioning, active, degraded, suspended, decommissioned
--
--   The MemoryStore never tripped the DB constraint, so the
--   discrepancy was latent. The Phase 1 PgStore surfaces it
--   immediately: every Create fails the CHECK because
--   StateNew == "new" is not in the old allow-list.
--
--   The Go model is the public API (handlers, tests, agent
--   protocol) so the schema is the thing that has to
--   change. We drop the old CHECK and add a new one with
--   the Go model values. The migration is data-safe: no
--   production rows exist in Phase 0, and even if some
--   staging rows had an old value, the DOWN would restore
--   the old allow-list for symmetry.
--
-- The Go model's `validateState` in
-- internal/nodes/service.go is the canonical guard. The
-- DB CHECK is the last line of defence.

BEGIN;

-- +migrate Up

ALTER TABLE nodes DROP CONSTRAINT IF EXISTS nodes_state_check;

ALTER TABLE nodes ADD CONSTRAINT nodes_state_check
    CHECK (state IN ('new', 'online', 'draining', 'offline', 'disabled'));

-- +migrate Down

ALTER TABLE nodes DROP CONSTRAINT IF EXISTS nodes_state_check;

ALTER TABLE nodes ADD CONSTRAINT nodes_state_check
    CHECK (state IN ('provisioning', 'active', 'degraded', 'suspended', 'decommissioned'));

COMMIT;
