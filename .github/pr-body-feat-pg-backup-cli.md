# feat(cli): aegis-pg-backup + aegis-pg-restore CLI

The operator-side entry point for the v0.5.0
backup surface. The HTTP endpoints shipped in
PR #120 are for the UI; the CLI is for the
operator's own scheduler (crontab, systemd-
timer, etc.) plus the disaster-recovery
restore path.

## What this PR ships

- `cmd/aegis-pg-backup/main.go` (~250 LOC) — five
  subcommands:
  - `list` — emit every backup row as a JSON
    array to stdout
  - `get <id>` — emit one row as JSON
  - `create [--trigger manual|scheduled]` —
    spawn a fresh backup, emit the row as
    JSON
  - `delete <id>` — drop the row plus dump file
  - `download <id> <path>` — write the
    `.dump.gz` to `<path>` (refuses to
    write into the backups dir itself;
    binary is read-only on the source)
  All subcommands write a single JSON value to
  stdout, errors go to stderr in
  `{"error":"..."}` shape. Cron-friendly: exit
  code is the canonical "this command failed"
  signal; `jq` on stdout does not see stderr.

- `cmd/aegis-pg-restore/main.go` (~200 LOC) —
  one positional `<id>` plus `--yes` /
  `--dry-run` flags. Two-step confirmation:
  the operator must type the backup id again
  before the destructive op runs. `--dry-run`
  runs `pg_restore --list` for a quick eyeball
  check.

- `.gitignore` — added `.git-commit-*.md` (the
  commit-message-draft convention; matches the
  existing `.git-commit-*.txt`).

## What this PR does NOT ship

- **The HTTP-level restore endpoint** is still
  `ScopeBackups` plus `AEGIS_BACKUPS_ALLOW_UI_RESTORE`
  gated (see #120). The CLI is the
  operator-only path; the UI path is
  intentionally NOT exposed in v0.5.0.
- **Restore to a point-in-time** (`--to
  <timestamp>`) — would need a separate
  basebackup plus WAL-replay workflow. v0.5.x
  follow-up.
- **Shell completion** for the subcommands.
  Cosmetic, daily-driver nice-to-have.

## Why two binaries

Restore is destructive (drops and recreates
the target database). Keeping the binaries
separate enforces the safety boundary at the
process level: an operator who types
`aegis-pg-backup restore <id>` gets an
`unknown subcommand` error, not a silent data
wipe. `aegis-pg-backup` is the safe default;
`aegis-pg-restore` is the intentional one-off
path.

## Operator workflow

```bash
# Cron entry — daily dump at 02:00.
0 2 * * *  aegis-pg-backup create \
    >> /var/log/aegis/backup.log 2>&1

# Cron entry — purge backups older than 30 days.
0 3 * * *  aegis-pg-backup list \
    | jq -r '.[] | select(.createdAt < (now - 2592000 | todate)) | .id' \
    | xargs -r aegis-pg-backup delete

# Manual — list available backups.
aegis-pg-backup list

# Manual — see what a restore would do (no
# destructive action).
aegis-pg-restore bck_2026_07_28_xxx --dry-run

# Manual — restore (operator types the id again
# when prompted, OR pass --yes to skip the
# second prompt for non-interactive use).
aegis-pg-restore bck_2026_07_28_xxx --yes
```

Both binaries read the same env vars as the
panel container does:
- `AEGIS_BACKUPS_DIR` (default `./var/backups`)
- `AEGIS_POSTGRES_DSN` (required for `create`
  and `restore`)
- `AEGIS_BACKUPS_ALLOW_UI_RESTORE` (must be
  the literal `true` for `restore`; the same
  flag the HTTP handler checks, so a single
  `EnvironmentFile` controls both paths)

## Verification

Local:

```bash
go build ./...
go vet ./...
GOFLAGS=-tags=integration golangci-lint run \
  --config .golangci.yml \
  ./cmd/aegis-pg-backup/ ./cmd/aegis-pg-restore/
go test -count=1 -timeout 60s ./...

# Smoke test the subcommands against a temp
# dir (no real Postgres needed for the
# list / get / delete paths).
AEGIS_BACKUPS_DIR=/tmp/aegis-test \
  ./aegis-pg-backup list    # → []
AEGIS_BACKUPS_DIR=/tmp/aegis-test \
  ./aegis-pg-backup --help  # → usage
```

The CI matrix (this PR's pipeline) runs the
standard backend Go tests plus golangci-lint v2.
The `aegis-pg-backup` and `aegis-pg-restore`
binaries have no dedicated test files (the
underlying `backups.Service` is fully covered
by the #120 test set; the CLI is thin).
