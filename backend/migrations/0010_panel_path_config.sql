-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Aegis migration 0010 — panel path config (URL prefix rotation).
--
-- Why:
--
--   The subscription package's `panel_path_config` (a
--   3X-UI / X-UI convention) is the operator's defence
--   against URL-scraping bots: the panel's subscription
--   endpoints sit behind a 16-char hex prefix that the
--   operator can rotate. The default path
--   `/api/v1/sub/<token>` is documented and easily
--   scraped; the rotated path is a moving target.
--
--   The config is global — one row per panel. Rotation
--   generates a new random path and deactivates the old
--   one. The router reads the active row at boot and
--   mounts the subscription handler at the rotated path
--   in addition to the default path.
--
-- Why a single-row table, not a key-value `settings`:
--
--   - the path config is the only global config we
--     have today; a single-purpose table reads cleaner
--     than a generic `kv` table with one row;
--   - the row id is fixed (`00000000-0000-0000-0000-
--     000000000001`) so the Service does not need a
--     `List` step to find the current value;
--   - future per-tenant paths land in a separate
--     `panel_path_config_tenant` table.
--
-- Why the sub_path is a 16-char hex:
--
--   The path is what the operator shares with end users
--   (typically via a single message at signup). 16 hex
--   characters = 64 bits of entropy, which is enough to
--   defeat a casual scraper and short enough to type
--   from a phone. The path lives in the URL between
--   the panel hostname and the `sub/<token>` segment:
--
--     https://panel.example.com/<sub_path>/sub/<token>
--
--   The `sub` segment is the subscription package's
--   own mount; the rotated prefix is a chi mount that
--   delegates to the same handler.

BEGIN;

-- +migrate Up

-- The CREATE TABLE uses IF NOT EXISTS so a re-apply
-- of the migration (a flaky test setup, a hand-roll
-- replay) is a no-op rather than a hard error. The
-- tracking-table idempotency in the migrator handles
-- the common case (a re-run skips this file entirely);
-- the IF NOT EXISTS is the second line of defence.
CREATE TABLE IF NOT EXISTS panel_path_config (
    id          UUID PRIMARY KEY DEFAULT '00000000-0000-0000-0000-000000000001'::UUID,
    sub_path    TEXT NOT NULL UNIQUE,
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Optional grace window. NULL = no expiry. The
    -- default rotation policy is "old path stops
    -- working immediately"; the grace is a safety
    -- valve for the operator who rotates by mistake.
    expires_at  TIMESTAMPTZ,
    CHECK (id = '00000000-0000-0000-0000-000000000001'::UUID)
);

-- Seed the default row. ON CONFLICT DO NOTHING so
-- a re-apply does not duplicate the sentinel row.
INSERT INTO panel_path_config (sub_path, is_active)
VALUES ('', TRUE)
ON CONFLICT (id) DO NOTHING;

-- +migrate Down

DROP TABLE IF EXISTS panel_path_config;

COMMIT;
