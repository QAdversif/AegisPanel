<!--
This file is the PR body for #120. It is committed
alongside the code so the body is part of the PR's
git history (the `gh pr create --body-file` path
mirrors the `git log` PR for posterity).
-->

# feat(backups): internal/backups package + admin router

Closes one of the two remaining GA blockers
(`backups` — the other is restricting
`aegis-deploy` sudo to specific commands, a
Phase 2 secret-hygiene chore). The v0.5.0 panel
can now dump its own Postgres on demand, keep a
retention window of the most recent N dumps, and
stream the dump back to an operator over the
admin API.

## What this PR ships

- A new `internal/backups` package (≈950 LOC
  + 540 LOC tests, 11 tests passing) with:
  - `Backup` row struct (snake_case JSON),
    `Trigger` (`manual`/`scheduled`), `Status`
    (`running`/`ok`/`failed`).
  - `Store` interface + `LocalStore`
    implementation. Metadata is a single
    `<backupsDir>/_index.json` next to the
    dumps — deliberately orthogonal to
    Postgres so a restore is exactly the case
    where the panel DB is unavailable.
  - `Backend` interface + `osBackend` rooted at
    `BackupsDir` (rejects `..`, absolute paths,
    backslashes).
  - `Service` orchestrator: single-flight
    `Create` (`ErrBackupInProgress` on
    concurrent), SHA-256 + `<id>.sha256`
    sidecar, retention cleanup, per-backup
    metadata counts, optional in-process cron
    scheduler.
  - `Handler` (mounted at `/api/v1/backups`)
    with: create, list, get, download, delete,
    restore. All endpoints behind
    `auth.RequireScope(auth.ScopeBackups)`.
- A new `ScopeBackups = "backups"` in
  `internal/auth/scopes.go`, granted only to
  the `admin` role.
- Five new env vars in `internal/config`:
  - `AEGIS_BACKUPS_DIR` (default
    `./var/backups`).
  - `AEGIS_BACKUPS_ALLOW_UI_RESTORE` (`false`
    in production; CLI bypasses the HTTP path
    entirely).
  - `AEGIS_BACKUPS_RETENTION_DAYS` (30).
  - `AEGIS_BACKUPS_MAX_COUNT` (0 = off).
  - `AEGIS_BACKUPS_CRON` (empty = scheduler
    disabled; typical production
    `0 2 * * *`).
- Wiring in `cmd/aegis/main.go` (constructs
  the `backups.Service` from config and
  spawns the scheduler goroutine when
  `AEGIS_BACKUPS_CRON` is set) and
  `internal/router/router.go` (mounts the
  handler).

## What this PR does NOT ship

- The BackupsView.vue UI is **#121**. This PR
  ships the API surface (curl-able end-to-end),
  but the buttons and download links live in
  the follow-up. The `AEGIS_BACKUPS_ALLOW_UI_RESTORE`
  default of `false` means the restore endpoint
  is unreachable from the UI until a deliberate
  opt-in.
- The `cmd/aegis-pg-backup` / `aegis-pg-restore`
  CLI binaries are **future PRs**; the Service
  API is stable enough to add them without
  touching the handler or the wire format.
- Wiring `AEGIS_BACKUPS_*` through the
  `secrets.env` indirection is part of the
  post-#119 container-wiring chore (not in
  this PR). The env names are stable; a
  follow-up adds the `EnvironmentFile` mount to
  the panel container.
- An S3 (or any S3-compatible) `Backend` is a
  follow-up. The `Backend` interface was
  designed so the swap is a single PR.

## Operator workflow

```bash
# Dev — manual-only, dump goes to ./var/backups
AEGIS_POSTGRES_DSN=... \
AEGIS_JWT_SECRET=... \
AEGIS_REDIS_ADDR=... \
AEGIS_NATS_URL=... \
./bin/aegis

# Trigger a backup (Bearer-protected)
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/backups

# Production — daily at 02:00 panel-local time
AEGIS_BACKUPS_CRON="0 2 * * *" \
AEGIS_BACKUPS_RETENTION_DAYS=30 \
AEGIS_BACKUPS_MAX_COUNT=14 \
AEGIS_BACKUPS_DIR=/var/lib/aegis/backups \
./bin/aegis
```

## Test summary

11 new tests, all passing on the standard
`go test -count=1 ./internal/backups/` run:

- `TestLocalStoreCRUD` — Insert/Get/List/Update/Delete
- `TestOSBackendMkdir` — directory creation
- `TestPathValidationInOSBackend` — rejects `..`,
  absolute paths, backslashes
- `TestPathTraversalRejected` — Stat / Remove
  also rejected
- `TestParseCron_Valid` / `TestParseCron_Invalid`
  — custom 5-field parser
- `TestNewBackupIDFormat` — `bck_<14>_hex` shape
- `TestDSNParse` — URL-parse helper
- `TestServiceCreateHappyPath` — full create +
  hash + sidecar + list
- `TestServiceCreateSingleFlight` — concurrent
  `Create` returns `ErrBackupInProgress`
- `TestServiceCreateFailure` — dumpFn error
  marks row `failed` and persists
- `TestServiceDeleteRemovesFile` — Delete
  removes dump + sidecar, idempotent
- `TestServiceOpenStreamsBytes` — gzip magic
  bytes on the stream
- `TestServiceCleanupRetentionAge` /
  `TestServiceCleanupRetentionCount` — both
  retention axes
- `TestServiceRestoreBlockedByDefault` —
  `ErrBackupDisabled` when flag is off
- `TestHashFile` — SHA-256 helper

`go test -count=1 ./...` is green across the
whole backend (no regressions in any other
package).
