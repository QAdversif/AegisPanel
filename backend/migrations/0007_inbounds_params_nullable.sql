-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Aegis migration 0007 — make `inbounds.params` nullable.
--
-- Why:
--
--   The Go Inbound model (internal/inbounds/inbound.go)
--   declares:
--
--       Params map[string]any `json:"params,omitempty"`
--
--   The `omitempty` tag is the public API: the panel
--   serialises an inbound without a `params` field when
--   the operator has not configured any per-protocol
--   parameters. Round-tripping this through a NOT NULL
--   column with a default `'{}'::JSONB` is lossy — the
--   DB layer cannot preserve the "operator has not
--   configured params" vs "operator has explicitly
--   configured an empty object" distinction.
--
--   Phase 1's PgStore (PR 38) round-trips a Go `nil`
--   map as SQL NULL through `mustMarshalOrNil`, which
--   preserves the distinction in the Go <-> SQL path
--   (a typed-nil pointer / slice / map becomes SQL
--   NULL; a real value is JSON-marshaled and stored as
--   a non-NULL JSONB object). To make the DB
--   constraint consistent with the model, the column
--   must accept NULL.
--
--   The DEFAULT is preserved, so an INSERT that omits
--   the column still produces `'{}'::JSONB`. The only
--   difference: an explicit `params = $8` with `nil`
--   (SQL NULL) is now accepted by the constraint.
--
-- This is data-safe: a NOT NULL column cannot already
-- contain NULL, so dropping the NOT NULL cannot
-- invalidate any existing row.

BEGIN;

-- +migrate Up

ALTER TABLE inbounds ALTER COLUMN params DROP NOT NULL;

-- +migrate Down

ALTER TABLE inbounds ALTER COLUMN params SET NOT NULL;

COMMIT;
