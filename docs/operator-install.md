# AegisPanel — Fresh install & upgrade

This is the canonical way to install or upgrade an AegisPanel panel +
UI pair on a Linux server (Ubuntu 24.04+ recommended). For the full
operator contract (Caddy, env, sops+age envelope, backups), see
`operator-guide.md`.

> **v0.8.32.1 baseline (this file was updated for):** the panel binary
> now `//go:embed`s the full set of migration files (the v0.8.30/31
> mTLS files 0023 + 0024 ship in the binary). The host mount at
> `/var/lib/aegis/migrations` is now an optional operator override
> (hot-fix path), not the only source of truth. See §"Schema
> migrations" below for the new contract. If you ran v0.8.28.6 or
> v0.8.30/31 on prod and used to scp migrations before every
> upgrade, you can stop doing that after upgrading to v0.8.32.1. The v0.8.32.1 release is a test/CI-hygiene baseline (no image change over v0.8.32); see `CHANGELOG.md` §[0.8.32.1] for the 8-PR release record.

## TL;DR

```bash
# 1. One-time server-user setup (as root, on the server).
#    See "Server-user setup" below.
sudo tools/scripts/aegis-deploy-setup.sh

# 2. Configure the host env (or write /opt/aegis/.env).
export AEGIS_PROD_IP="1.2.3.4"          # your server's public IP
export AEGIS_PANEL_TAG="0.8.32.1"       # aegispanel image tag
export AEGIS_UI_TAG="v0.8.32.1"         # aegispanel-ui image tag
export AEGIS_ENV_FILE="/tmp/aegis-v0.8.32.1.env"
export AEGIS_UI_CADDYFILE="/tmp/ui-Caddyfile"

# 3. Install / upgrade.
sudo tools/scripts/install-aegis-stack.sh
```

The install script is **idempotent** — running it again is a no-op (or, for an
upgrade, recreates the containers with the new image tags).

## Server-user setup (one-time, before first install)

`install-aegis-stack.sh` assumes:

- A user `aegis-deploy` exists (uid 1000), with bash shell, sudo NOPASSWD.
- That user's `~/.ssh/authorized_keys` contains the operator's deploy pubkey.
- `sshd` is hardened: `PasswordAuthentication no`, `AllowUsers aegis-deploy`,
  root password locked.

`tools/scripts/aegis-deploy-setup.sh` does all of that when run on the server
as root. The matching `aegis-deploy-setup-remote.py` does the same remotely
from your workstation (requires `AEGIS_ROOT_PASSWORD` env var for the very
first SSH; rotate / lock root password after).

## What `install-aegis-stack.sh` does

1. Creates the `aegis-net` Docker network (external, so this is a no-op on
   subsequent runs and doesn't conflict with the existing `aegis-postgres`,
   `aegis-redis`, `aegis-nats` containers which also use it).
2. Creates `/var/lib/aegis/{migrations,known_hosts,backups}` with
   `aegis-deploy:aegis-deploy` ownership.
3. Sets `/etc/aegis/age.key` to `chown 65532:65532, chmod 0640` — the
   distroless panel container runs as UID 65532 and needs to read this file.
4. Copies `tools/scripts/aegis-stack.yml` to `/opt/aegis/docker-compose.yml`
   if the latter doesn't exist. **Doesn't overwrite** a custom file the
   operator may have edited.
5. `docker compose pull && docker compose up -d` — pulls new image (if
   `AEGIS_PANEL_TAG` / `AEGIS_UI_TAG` changed) and recreates the two
   containers with the canonical mount set.
6. Smoke: `GET /api/v1/health` on the panel's port.

## Upgrading to a new version

1. On your workstation, push the new image to GHCR via the release workflow
   (`tools/scripts/release.sh` is the typical entry point).
2. On the server:
   ```bash
   export AEGIS_PANEL_TAG="0.8.32.1"  # new tag
   export AEGIS_UI_TAG="v0.8.32.1"
   sudo tools/scripts/install-aegis-stack.sh
   ```
3. Smoke through the public URL. If something's wrong:
   ```bash
   cd /opt/aegis && docker compose down
   # Rollback to the most recent `aegis-panel-prevN` / `aegis-ui-prevM`
   # (these are created by older bounce scripts; pre-0.8.28.6 deploys).
   docker rename aegis-panel-prev4 aegis-panel
   docker start aegis-panel
   ```

## Why compose (and not plain `docker run`)

The 4 canonical mounts for `aegis-panel` — `migrations`, `known_hosts`,
`age.key`, `backups` — and the `0.0.0.0:8081:8080` publish for `aegis-ui`
(needed for the top-level Caddy to reach the UI without source-IP races)
are declared **once** in the compose file. Plain `docker run` invocations
have to be retyped or re-copied on every deploy, and a missing mount
silently breaks a feature (real example: the 2026-08-24 v0.8.28.6 prod
deploy forgot `-v /var/lib/aegis/backups:/app/var/backups`, and the
backup list went empty in the UI even though the `.sql.gz` files were
right there on the host).

With compose, the contract is the file. `docker compose up -d` will never
forget a mount, will recreate the right container image, and will pick up
new env-file paths on demand.

## Files added by this contract

- `tools/scripts/aegis-stack.yml` — production compose (template)
- `tools/scripts/install-aegis-stack.sh` — install/upgrade wrapper
- `docs/operator-install.md` — this file

## What this contract does NOT cover

- **Postgres / Redis / NATS** — these are still plain `docker run` for
  historical reasons (long-lived volumes, stable config, low churn). They
  live on the same `aegis-net` network and the panel talks to them via
  Docker DNS. Migrating them under compose is a separate project; only do
  it if you have a reason (e.g. moving to a new host with the rest of the
  stack).
- **Top-level Caddy** — runs on the host as a systemd service, not as a
  container. See `operator-guide.md` for the Caddyfile contract.
- **Schema migrations** — the panel binary now `//go:embed`s the full
  set of migration files (v0.8.31.1 hotfix; v0.8.32.1 unchanged). The host mount at
  `/var/lib/aegis/migrations` is an optional operator override for
  hot-fixing a migration without rebuilding the image. If the host
  mount is non-empty but missing any embedded migration, the migrator
  **fails loud** with the missing filenames + install-contract
  remediation rather than silently falling back. See the
  "Schema migrations" section below for the full new contract.
- **sops+age envelope** — `AEGIS_WEBHOOKS_SECRET_AGE_*` and similar go
  through the env file at `/tmp/aegis-v0.8.32.1.env`, which the operator
  builds offline with `sops` and ships to the server. The env file is
  never committed to the repo.

## Schema migrations (v0.8.31.1+, unchanged in v0.8.32.1)

The panel binary bundles the full migration set via
[`//go:embed`](https://pkg.go.dev/embed) at
`backend/internal/migrations/sql/*.sql`. The `migrations.Up(dir)`
function in `internal/migrations/migrator.go` picks the source
deterministically:

1. **No host mount / empty mount / missing dir** — reads from the
   embedded FS. The canonical install contract for v0.8.31.1+ (v0.8.32.1 ships the same binary):
   do nothing. The panel just works.
2. **Non-empty host mount with every embedded migration** —
   reads from the host mount. This is the operator's hot-fix
   path (replace a migration file in the host mount, restart
   the panel, the next apply picks up the new file).
3. **Non-empty host mount missing some embedded migrations** —
   **fails loud** at boot with the missing filenames + the
   install-contract remediation (see the `resolveSource` doc in
   `migrator.go:150-170`). The panel will NOT start. This is
   the safety net for the 2026-08-25 v0.8.30/31 prod incident
   where the host mount had 0001-0022 but not 0023-0024, and
   the panel crashed on the first query against
   `nodes.agent_transport`.

### For operators with no hot-fix in flight

You can remove the `/var/lib/aegis/migrations` host mount from
the compose (and from the `aegis-stack.yml` template) once
you're on v0.8.31.1+ (or v0.8.32.1). The panel binary ships everything; the
mount becomes pure ceremony. A follow-up compose cleanup
PR is tracked separately.

### For operators with a hot-fix in flight

If you have a custom migration file in `/var/lib/aegis/migrations/`
(typically a `9999_*.sql` or a tweaked 00XX file):

1. **Always** keep the full set of embedded migrations in the
   mount too. The migrator's fail-loud check refuses a partial
   override, so missing even one embedded file blocks boot.
2. After the panel release that includes the hot-fix in
   source, **remove** the custom file from the mount (the
   embedded one takes over) and the next deploy will Just Work.

### Validating the install

After a fresh `docker compose up -d aegis-panel`, the
migrator logs should show every migration being applied
(NOT skipped — the partial-override check only fires for
files in the host mount, not for embedded ones). A quick
sanity:

```bash
docker exec aegis-postgres psql -U aegis -d aegis -tAc \
  "SELECT count(*) FROM schema_migrations WHERE name LIKE '00%'"
# expect 24 (0001..0024) on a v0.8.31.1+ / v0.8.32.1 deploy
```

If the count is < 24 and the panel is on v0.8.31.1+ / v0.8.32.1, the
embedded migrator did not run (e.g. someone added a `--no-migrate`
flag in a future refactor — this would be a regression
caught by `TestResolveSource_EmbeddedIsNonEmpty` in
`migrator_test.go`).

### Why this changed

Pre-v0.8.31.1, the panel binary did not ship the SQL files
at all. The install contract (PR #297) required the operator
to `scp backend/migrations/*.sql` to
`/var/lib/aegis/migrations/` on the host before pulling the
new image. The 2026-08-25 v0.8.30/31 prod deploy hit this:
the host had 0001-0022 but not 0023-0024, the panel
silently fell through, and the v0.5.0 singbox wiring
crashed on `SELECT n.agent_transport FROM nodes` with
`column does not exist`. The v0.8.31.1 hotfix (v0.8.32.1 unchanged) (a) embeds
the SQL files in the binary and (b) fail-loud-checks any
host mount override, so the failure mode is now impossible.

### `psql -f` direct-execute warning

**Do not run the migration files directly via `psql -f`.**
Each `00XX_*.sql` file in `backend/internal/migrations/sql/`
contains BOTH a `-- +migrate Up` section (the schema
creation) AND a `-- +migrate Down` section (the rollback
path). The panel's migration runner
(`internal/migrations/migrator.go:Up`) reads only the Up
section per file; running the file directly with
`psql -f` executes BOTH sections, which creates the
schema and then immediately drops it. The result: the
panel boots with the old schema, the next query against
a new column crashes (`column n.agent_transport does not
exist` or `relation nodes does not exist`).

If you must apply a migration out-of-band (e.g. on a
prod box where the panel can't boot), trim the file to
the Up section first:

```bash
# Extract just the Up section to /tmp/0023_up.sql:
sed -n '/-- +migrate Up/,/-- +migrate Down/p' \
  /var/lib/aegis/migrations/0023_agentca.sql \
  | sed '/-- +migrate Down/d' > /tmp/0023_up.sql

# Then apply:
docker exec -i aegis-postgres psql -U aegis -d aegis -v ON_ERROR_STOP=1 < /tmp/0023_up.sql
```

The cleanest path is still to let the panel's migrator
do the work — restart the panel after pulling the new
image and `migrations.Up` will apply the missing files
from the embedded FS. The trim-and-apply dance above
is only for the post-mortem scenario where the panel
won't boot without the new schema already in place.

## Privacy

The compose template in `tools/scripts/aegis-stack.yml` does **not** contain
the public IP, the admin password, the JWT secret, the age private key,
or any other banned value. The operator passes them via:

- **Public IP**: `AEGIS_PROD_IP` env var (not in any committed file).
- **Admin password / JWT / age**: `AEGIS_ENV_FILE` (operator-side, in
  `/tmp/...`, never committed).
- **SSH key path / age key path**: the canonical paths `/var/lib/aegis/*`
  and `/etc/aegis/age.key` are in the compose file; the contents of those
  files are operator-side only.

Public IPs and hostnames are in the agent's `BANNED_PATTERNS` (see
`AGENTS.md` at the repo root). Do not paste them into a PR, issue, commit
message, or chat — pass them through operator-side files instead.
