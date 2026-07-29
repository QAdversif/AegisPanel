<!--
This file is the PR body for #121. It is committed
alongside the code so the body is part of the PR's
git history (the `gh pr create --body-file` path
mirrors the `git log` PR for posterity).
-->

# feat(backups): BackupsView.vue + API client

The SPA surface for the v0.5.0 backup package
shipped in #120. The view is reachable from the
sidebar under a Database icon between `Audit log`
and `Profile`.

## What this PR ships

- A new admin surface (`/backups`) with a
  toolbar (Refresh + Create), a six-column
  DataTable (id, createdAt, size, trigger,
  status badge, node/user/host counts), and
  per-row download + delete buttons.
- A new `frontend/src/api/services/backups.ts`
  module that wraps the v0.5.0 wire surface
  shipped in #120. Exports: `listBackups`,
  `getBackup`, `createBackup`, `deleteBackup`,
  `restoreBackup` (not yet wired into the UI),
  and `downloadBackup` (blob + ObjectURL +
  anchor.click() dance for browser-side file
  save with a Bearer-authenticated GET).
- Polling while at least one row is in
  `running` status (2s interval). The poll
  stops the tick after the last `running`
  row settles to `ok` or `failed`, so the
  transition shows up without a manual
  refresh. The interval is cleared in
  `onBeforeUnmount`.
- Full i18n coverage (en + ru): title,
  subtitle, actions, statuses, triggers,
  error messages, plus a `backups` entry
  under `nav` and `profile.scopes`.
- A new `Database` lucide icon for the
  sidebar entry.

## What this PR does NOT ship

- The `Restore` action is intentionally not
  in the v0.5.0 UI: a UI-driven restore is
  dangerous (it drops the panel DB) and the
  operator's safer path is the future
  `cmd/aegis-pg-restore` CLI binary. The
  endpoint is already wired in
  `api/services/backups.ts` so a follow-up
  PR can surface it behind a confirmation
  dialog without touching the wire format.
- The `cmd/aegis-pg-backup` /
  `aegis-pg-restore` CLI binaries are
  future PRs.
- The container wiring for `AEGIS_BACKUPS_*`
  is part of the post-#119 chore, not in
  this PR.

## Operator workflow

1. Sign in to the panel as an admin.
2. Click `Backups` in the sidebar.
3. Click `Create backup` to spawn a fresh
   `pg_dump`. The button is disabled while a
   backup is `running` (matches the backend
   `ErrBackupInProgress` -> HTTP 409 single-flight
   guard).
4. Watch the row's status badge transition
   from `Running` (warning) to `OK` (success)
   or `Failed` (destructive). The table
   polls the list endpoint every 2s; the
   poll stops automatically on the next tick
   after the last `running` row settles.
5. Click the download icon on an `OK` row to
   pull the `.dump.gz` file. The filename is
   `<id>.dump.gz` per the backend's `Path`
   field; the bearer token is attached via
   the axios request interceptor (we never
   expose it in the URL).
6. Click the trash icon to delete a row.
   Confirms with a `window.confirm` dialog
   before posting the `DELETE` (the panel
   returns 204 on success and on a missing
   id; the UI removes the row from the
   list either way).

## Verification

Local dev loop (without a Postgres to back it):

```bash
cd frontend
pnpm install --frozen-lockfile
pnpm run build      # vite build, BackupsView lands in dist/
pnpm run lint       # eslint + prettier
pnpm run typecheck  # vue-tsc
```

End-to-end loop (against a live panel):

```bash
# 1. Boot a panel with backups enabled:
AEGIS_POSTGRES_DSN=... AEGIS_JWT_SECRET=$(openssl rand -hex 32) \
  AEGIS_REDIS_ADDR=... AEGIS_NATS_URL=... \
  AEGIS_BACKUPS_DIR=./var/backups \
  go run ./cmd/aegis

# 2. Sign in, click Backups, click Create.
#    The row appears in `running`; ~1s later
#    the badge flips to `OK` (or `Failed` if
#    the dump is empty / pg_dump is missing).
# 3. Click the download icon — the file
#    `<id>.dump.gz` lands in your Downloads
#    directory. Verify with:
gunzip -c <id>.dump.gz | pg_restore --list | head
```

## Test summary

No unit tests for the new view (the project
ships no frontend test files today — the
PR is reviewed for type-safety via
`vue-tsc --noEmit` and for the
build success via `vite build`, both of
which are green).
