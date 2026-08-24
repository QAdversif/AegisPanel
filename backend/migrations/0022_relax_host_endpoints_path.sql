-- 0022_relax_host_endpoints_path.sql
--
-- Symptom (2026-08-24, found when first host creation was attempted
-- on the v0.8.28.6 prod deploy):
--   POST /api/v1/hosts/  -> 500
--   {"error":"create: insert endpoint: ERROR: new row for relation
--    \"host_endpoints\" violates check constraint
--    \"host_endpoints_path_check\" (SQLSTATE 23514)"}
--
-- Root cause:
--   The `host_endpoints` table, created in migration 0004_hosts_v3.sql,
--   declared `path TEXT NOT NULL DEFAULT ''` together with
--   `CHECK (path <> '')`. Those two are mutually inconsistent: the
--   DEFAULT violates the CHECK on every row that does not explicitly
--   supply a non-empty path. The Go layer (Endpoint.Path is a plain
--   string) treats path as optional and validates only its length
--   (<=256), so a Create with path="" passes service-side validation
--   and trips the DB CHECK on INSERT.
--
--   For non-WebSocket protocols (vless / vmess / trojan without
--   transport=ws), path is a legitimate empty value: a Create
--   without a path is valid input. The original constraint was
--   overly strict and the DEFAULT was a relic from an earlier
--   iteration of the hosts v3 model.
--
--   Since 0004 this code path has been silently broken: any
--   `Path: ""` insert would 500. It did not surface earlier only
--   because no host was ever created through the v3 schema on prod
--   (verified: `SELECT COUNT(*) FROM hosts` returned 0 at the time
--   of the 2026-08-24 prod-deploy smoke).
--
-- Fix:
--   Drop the default and the CHECK, and make `path` NULLable. The
--   Go layer continues to send `""` for unset path; that is
--   preserved as-is on the row (the column accepts both '' and
--   NULL — we keep the existing NOT-NULL behaviour on `''` to
--   minimise downstream read-code changes).
--
-- Notes:
--   * No data migration: there are zero existing host_endpoints
--     rows on prod, so we do not need to back-fill anything.
--   * No Go code change: pg_store.go passes ep.Path directly, and
--     `ep.Path == ""` is now accepted by the relaxed schema.
--   * `validateEndpointOverrides` in
--     backend/internal/hosts/validate.go is intentionally NOT
--     tightened to require non-empty path: the protocol model
--     legitimately allows empty path for non-WS transports, and
--     tightening it would be a wire-format break.
--   * If a future migration wants to require non-empty path for
--     specific protocols, that is a per-protocol CHECK, not a
--     column-level one.
--
-- Down-stream compatibility:
--   * subscription service (subscription/pg_store.go) reads
--     `e.Path` and treats empty string the same as a missing
--     path for non-WS protocols (see `isWebsocketTransport`).
--   * The sing-box provider at render time skips path-rendering
--     when the resolved transport is not websocket, so an empty
--     `Path` is functionally a no-op for those protocols.

ALTER TABLE host_endpoints
ALTER COLUMN path DROP DEFAULT;

ALTER TABLE host_endpoints
ALTER COLUMN path DROP NOT NULL;

ALTER TABLE host_endpoints
DROP CONSTRAINT host_endpoints_path_check;
