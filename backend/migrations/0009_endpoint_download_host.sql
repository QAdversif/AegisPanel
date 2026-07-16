-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Aegis migration 0009 — endpoint XHTTP download host.
--
-- Why:
--
--   The subscription package's sing-box renderer emits
--   a `download_settings` block on a VLESS outbound
--   when the inbound's transport is XHTTP. The block
--   points at a separate "download farm" host (a CDN
--   endpoint whose only job is to serve the XHTTP
--   download URL). The reference lives on the
--   endpoint, not the inbound, because the same
--   inbound can be reused across hosts and the
--   download farm is per-deployment.
--
--   `download_host_id` is an FK into `hosts(id)` with
--   `ON DELETE SET NULL` so deleting a download host
--   row silently degrades the endpoint to "no
--   download_settings" rather than cascading into
--   endpoint rows. The download host is operator-
--   controlled and is NOT in any user's pool; the
--   Service looks it up by id directly.
--
-- Why a nullable column (not a child table):
--
--   Most endpoints do not need a download host. A
--   nullable column on the existing row is the
--   natural shape; a child table would add a join
--   for the common case.

BEGIN;

-- +migrate Up

ALTER TABLE host_endpoints ADD COLUMN download_host_id UUID NULL REFERENCES hosts(id) ON DELETE SET NULL;

CREATE INDEX host_endpoints_download_host_id_idx ON host_endpoints (download_host_id) WHERE download_host_id IS NOT NULL;

-- +migrate Down

DROP INDEX IF EXISTS host_endpoints_download_host_id_idx;
ALTER TABLE host_endpoints DROP COLUMN download_host_id;

COMMIT;
