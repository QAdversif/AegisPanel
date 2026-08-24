# AegisPanel — Fresh install & upgrade

This is the canonical way to install or upgrade an AegisPanel panel +
UI pair on a Linux server (Ubuntu 24.04+ recommended). For the full
operator contract (Caddy, env, sops+age envelope, backups), see
`operator-guide.md`.

## TL;DR

```bash
# 1. One-time server-user setup (as root, on the server).
#    See "Server-user setup" below.
sudo tools/scripts/aegis-deploy-setup.sh

# 2. Configure the host env (or write /opt/aegis/.env).
export AEGIS_PROD_IP="1.2.3.4"          # your server's public IP
export AEGIS_PANEL_TAG="0.8.28.6"       # aegispanel image tag
export AEGIS_UI_TAG="v0.8.28.6"         # aegispanel-ui image tag
export AEGIS_ENV_FILE="/tmp/aegis-v0.8.28.env"
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
   export AEGIS_PANEL_TAG="0.8.28.7"  # new tag
   export AEGIS_UI_TAG="v0.8.28.7"
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
- **The DB schema migrations** — `backend/migrations/*.sql` are copied
  to `/var/lib/aegis/migrations/` on the host by `tools/scripts/aegis-deploy-setup.sh`
  (or a separate step) before the panel can start. Without them, the panel
  fails on boot with a "migrations: read dir" error.
- **sops+age envelope** — `AEGIS_WEBHOOKS_SECRET_AGE_*` and similar go
  through the env file at `/tmp/aegis-v0.8.28.env`, which the operator
  builds offline with `sops` and ships to the server. The env file is
  never committed to the repo.

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
