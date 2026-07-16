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

CREATE TABLE panel_path_config (
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

-- Seed the default row. The default `sub_path` is
-- the documented `/api/v1` prefix; the operator
-- rotates to a random 16-char hex on the first
-- admin action. The router treats the active row's
-- `sub_path` as a second mount point.
INSERT INTO panel_path_config (sub_path, is_active)
VALUES ('', TRUE);

-- +migrate Down

DROP TABLE IF EXISTS panel_path_config;

COMMIT;
