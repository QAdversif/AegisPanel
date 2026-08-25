-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Aegis migration 0008 — multi-port inbound (`listen_ports`).
--
-- Why:
--
--   The PR series' subscription package picks a random
--   port per fetch to defeat per-port DPI correlation
--   (3X-UI / Marzban convention). A single Inbound
--   row in `inbounds` now binds to a primary
--   `listen_port` AND any number of additional ports
--   declared in `listen_ports` (PostgreSQL INTEGER[]).
--   The subscription renderer picks a random element
--   from the union `{listen_port} ∪ listen_ports`.
--   The agent will bind every entry with the same
--   protocol / params; that work lands in a future
--   agent-side PR.
--
-- Why INTEGER[] and not a child table:
--
--   - The ports belong to the inbound; there is no
--     per-port metadata that would justify a separate
--     row. PostgreSQL's array support is the natural
--     fit for "set of integers on a row".
--   - The renderer needs to read all entries at once;
--     an array avoids a second round trip.
--   - The set is small (typically 2–4 entries) and
--     indexed linearly; an array GIN index is overkill.
--
-- Why NOT NULL with `DEFAULT '{}'`:
--
--   The Go Inbound model's `ListenPorts []int` uses
--   `omitempty` — a single-port inbound has no
--   `listen_ports` field in JSON. The renderer
--   treats `nil` and `[]` identically. We pick
--   `NOT NULL DEFAULT '{}'` so the column always
--   carries a typed empty array (the PgStore's
--   `nullableIntArray` helper explicitly returns
--   `[]int{}` for the empty case so pgx binds
--   the array type rather than SQL NULL). A
--   `NULL`-allowed column would also work; the
--   tradeoff is that the typed-empty path makes
--   the Go → SQL round-trip uniform (every read
--   returns a `[]int`, never a nil that has to
--   be re-coerced to `[]int{}` in the renderer).
--
-- Why we do NOT add a per-port uniqueness check:
--
--   The existing UNIQUE (node_id, listen_port) only
--   catches collisions on the primary port. A future
--   PR could add a row-level trigger that walks
--   `listen_ports` and rejects collisions with other
--   rows' primary ports, but the maintenance burden
--   (trigger fires on every UPDATE of every inbound)
--   outweighs the benefit for Phase 1. The operator
--   is expected to keep port sets non-overlapping;
--   the agent surfaces the OS-level bind() EADDRINUSE
--   at apply time.

BEGIN;

-- +migrate Up

ALTER TABLE inbounds ADD COLUMN listen_ports INTEGER[] NOT NULL DEFAULT '{}';

-- +migrate Down

ALTER TABLE inbounds DROP COLUMN listen_ports;

COMMIT;
