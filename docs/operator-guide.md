---
title: Operator guide
---

# Operator guide

The operator guide is the canonical document for the person who
**installs and runs the Aegis panel in production**. It is the
end-to-end "from a fresh VPS to a panel that serves real users"
flow.

If you are a developer who wants to hack on the panel itself, the
[developer guide](./developer/) is the right starting point. The
[getting started](./guide/getting-started) page is the dev-stack
entry (Postgres + Redis + NATS via docker compose on a laptop). This
page is the **operator path**: a single VPS behind a public
domain, secrets on disk, real users, real backups.

## Audience

You are running Aegis for one of these reasons:

- **Personal use** — you want a VPN for your own devices, and
  you trust yourself more than the hosted panels.
- **Small-team use** — you and a handful of friends / family /
  colleagues need privacy-preserving internet access and you'd
  rather run your own server than pay for a SaaS.
- **Commercial pilot** — you are validating the panel as the
  backbone of a small VPN product, before committing to a
  multi-tenant platform.

Aegis is **single-tenant**: one panel serves one operator. If you
need multi-tenant, this is not the project for you.

## TL;DR — five minutes from zero to "panel running"

```bash
# 0. Local prerequisites
brew install sops age     # or apt: sudo apt install sops age
pip install ansible       # or apt: sudo apt install ansible
go install github.com/aedificans/aegispanel/cmd/aegis-pg-backup@latest

# 1. One-time: generate the age keypair (BACK THIS UP)
age-keygen -o ~/.aegis/age.key
cat ~/.aegis/age.key.pub   # → paste into .sops.yaml

# 2. Fill in real secrets + encrypt
cp deploy/secrets/secrets.example.yml deploy/secrets/secrets.yml
$EDITOR deploy/secrets/secrets.yml           # see deploy/secrets/README.md
sops --encrypt --in-place deploy/secrets/secrets.yml

# 3. Stage on the target host
scp deploy/secrets/secrets.yml aegis-deploy@panel.example.com:/etc/aegis/secrets.yml.enc
scp ~/.aegis/age.key aegis-deploy@panel.example.com:/etc/aegis/age.key
ssh aegis-deploy@panel.example.com 'sudo chmod 0600 /etc/aegis/age.key'

# 4. Run the panel playbook
ansible-playbook -i deploy/ansible/inventories/prod/hosts.ini \
  deploy/ansible/playbooks/panel.yml

# 5. Smoke test (loopback; Caddy is the public ingress)
ssh aegis-deploy@panel.example.com \
  'curl -fsS http://127.0.0.1:8080/healthz'
```

The rest of this guide unpacks each step.

## Prerequisites

| Tool            | Version    | Why                                           |
| ---             | ---        | ---                                           |
| Linux x86_64    | kernel 5+  | host OS for the panel + nodes                 |
| Docker          | 24+        | the panel container runtime                   |
| Docker Compose  | v2 (`docker compose`) | the panel stack             |
| Ansible         | 9+         | install / configure playbooks                 |
| `sops`          | 3.7+       | encrypt the secrets file                      |
| `age`           | 1.2+       | the sops recipient keypair                    |
| `psql` / `pg_dump` | 16+    | backup / restore ops (or run aegis-pg-backup on the host) |
| Outbound HTTPS  | —          | pulls `ghcr.io/qadversif/aegispanel` and the sing-box tarball |

For dev-loop work on the panel itself, also install:

- Go 1.22+
- Node.js 20+ and pnpm 9+
- The pre-push gate from `tools/scripts/install-pre-push.sh` (so a
  red build is caught before the CI round-trip — see the developer
  guide).

## Architecture in one screen

```
                          ┌──────────────────────────────────┐
                          │  Caddy  (install_caddy role)     │
                          │  TLS termination on :443, :2053 │
                          │  decoy HTML on the default vhost │
                          └────────┬────────────┬────────────┘
                                   │            │
                          /secret-panel-path   /secret-sub-path
                                   │            │
                          ┌────────▼────────────▼────────────┐
                          │  aegis-panel container           │
                          │  ghcr.io/qadversif/aegispanel    │
                          │  loopback :8080, distroless,     │
                          │  nonroot, env_file=secrets.env   │
                          └────────┬────────────┬────────────┘
                                   │            │
                          Postgres  │            │  aegis-agent
                          (operator-│            │  (per-node Go
                          managed)  │            │  binary)
                                   │            │
                          ┌────────▼────┐  ┌────▼──────────┐
                          │  Postgres   │  │  sing-box      │
                          │  / Redis    │  │  (install_     │
                          │  / NATS     │  │   singbox role)│
                          └─────────────┘  └────────────────┘
```

The panel is the control plane. It does not relay traffic. Traffic
goes node → user (sing-box direct). The panel's job is to:

1. Provision nodes (Ansible over SSH).
2. Render per-user sing-box configs and push them to nodes.
3. Serve subscription URLs to user clients.
4. Provide the admin UI.
5. Audit log (who changed what, when).

## Install path

The canonical install is `deploy/ansible/playbooks/panel.yml`. The
playbook is three roles in order:

### 1. `bootstrap_node`

Creates the `aegis-deploy` user, installs the base packages, sets
up the `/etc/aegis/` directory tree with mode 0700. Idempotent.

### 2. `configure_secrets`

Installs `sops` and `age` (apt or download, configurable), then
runs:

```bash
sops --decrypt /etc/aegis/secrets.yml.enc > /etc/aegis/secrets.env
chmod 0600 /etc/aegis/secrets.env
```

The decrypted env file is mode 0600, owned by `aegis-deploy`. **It
is not in the container; the container bind-mounts it read-only.**
The panel's ENTRYPOINT (`/app/aegis`) is a distroless binary; the
container has no shell, so a compromise cannot `cat` the file.

### 3. `install_panel`

Drops `docker-compose.prod.yml` in `/etc/aegis/`, runs
`docker compose pull && docker compose up -d --remove-orphans`,
prints `docker compose ps` for the summary line. Refuses to run
without `/etc/aegis/secrets.env` (the container's
`env_file:` declaration has `required: true`).

## Secrets management

The full operator flow is in
[`deploy/secrets/README.md`](../deploy/secrets/README.md). The
short version:

1. Generate an age keypair **once**. Back up the private key
   offline. Lose it → lose every encrypted secret.
2. Paste the **public** key into `.sops.yaml` (repo root) and
   commit.
3. `cp deploy/secrets/secrets.example.yml deploy/secrets/secrets.yml`,
   edit the placeholders to real values, then
   `sops --encrypt --in-place deploy/secrets/secrets.yml`. Commit
   the encrypted file.
4. `scp` the encrypted file and the **private** key to each host
   that needs to decrypt it (the panel host, ideally nothing else).

The decrypted plaintext **never** leaves the target host. CI does
not decrypt; only the operator's local machine and the panel
host ever hold the private key.

## First node

A node is a VPS that runs the sing-box proxy daemon. The
`playbooks/node.yml` playbook installs the agent binary and
sing-box. Key steps:

1. **`install_singbox`** — pulls the sing-box tarball from
   `github.com/SagerNet/sing-box/releases`, looks up the SHA-256
   from the GitHub Releases API at install time (the
   `assets[].digest` field, format `sha256:<hex>`), and verifies
   the download with `get_url checksum:`. The v0.4.0 hardcoded
   hash is gone; bumping `aegis_singbox_version` in
   `group_vars/all.yml` is now a one-line change.
2. **`install_agent`** — drops the `aegis-agent` Go binary and
   the systemd unit. The agent connects to the panel, polls for
   config deltas, writes `/etc/sing-box/config.json`, reloads
   sing-box.
3. **`install_fail2ban`** — opinionated SSH brute-force defense.
   The fail2ban filters ship in the role; the operator picks the
   ban duration in `group_vars/all.yml`.

After the node playbook finishes, register the node in the
panel UI (or via the admin API). The panel will start pushing
configs as soon as the node is marked `online`.

## Daily operations

### Backups — `aegis-pg-backup`

`aegis-pg-backup` is the **operator-side** backup entry point. It
calls the `backups.Service` directly, bypassing the panel's HTTP
surface. Cron-friendly: every subcommand writes a single JSON
value to stdout and exits 0; errors go to stderr in
`{"error":"..."}` shape.

```bash
# Required env
export AEGIS_BACKUPS_DIR=/var/lib/aegis/backups
export AEGIS_POSTGRES_DSN='postgres://aegis:...@127.0.0.1:5432/aegis?sslmode=disable'

# Snapshot
aegis-pg-backup create
# → {"id":"bck_2026_07_29_xxx","status":"ok",...,"sizeBytes":12345678}

# List (pipe-friendly)
aegis-pg-backup list | jq -r '.[] | "\(.createdAt)  \(.id)  \(.sizeBytes)  \(.status)"'

# Download for off-host archival
aegis-pg-backup download bck_2026_07_29_xxx /backups/2026-07-29.dump.gz

# Drop a row + the dump file
aegis-pg-backup delete bck_2026_07_29_xxx
```

The canonical cron entry is in the `usage()` output of
`aegis-pg-backup` itself:

```cron
# /etc/cron.d/aegis-backup
0 2 * * *  aegis-pg-backup create >> /var/log/aegis/backup.log 2>&1
0 3 * * *  aegis-pg-backup list | jq -r '.[] | select(.status == "ok") | .id' \
                | head -n -30 | xargs -r aegis-pg-backup delete
```

The second line keeps a rolling 30-day window: every morning at
03:00 it lists all OK backups and deletes everything except the
30 most recent. The `head -n -30 | xargs` pattern is the standard
"keep N most recent" idiom.

### Restores — `aegis-pg-restore`

`aegis-pg-restore` is a **separate binary** from
`aegis-pg-backup`. The boundary is intentional: restore is
destructive (drops and recreates every object in the target
database). Keeping the binaries separate enforces the safety
boundary at the process level — an operator who types
`aegis-pg-backup restore <id>` gets an `unknown subcommand` error,
not a silent data wipe.

```bash
# 1. Dry run — see the SQL plan, no destructive effect
aegis-pg-restore bck_2026_07_29_xxx --dry-run

# 2. Real run (interactive) — type the id again to confirm
aegis-pg-restore bck_2026_07_29_xxx
About to DROP and recreate the database in:
  postgres://aegis:***@127.0.0.1:5432/aegis?sslmode=disable
from backup "bck_2026_07_29_xxx".
Type the backup id again to confirm: bck_2026_07_29_xxx
{"ok":true,"id":"bck_2026_07_29_xxx","restoredAt":"2026-07-29T22:48:31Z"}

# 3. Real run (non-interactive, e.g. disaster-recovery drill)
aegis-pg-restore bck_2026_07_29_xxx --yes
```

The two-step confirmation (type the id again) catches a typo from
the operator's shell history. The CLI also reads
`AEGIS_BACKUPS_ALLOW_UI_RESTORE` as a sanity check: if the flag
is not set to the literal `true`, the binary refuses. The DSN is
the actual security boundary; the flag catches a typo in the
operator's `EnvironmentFile`.

### Rotate the JWT secret

The JWT secret signs every API token. To rotate:

```bash
# 1. Generate a new secret
head -c 48 /dev/urandom | base64

# 2. Decrypt, edit, re-encrypt
sops --decrypt --in-place deploy/secrets/secrets.yml
$EDITOR deploy/secrets/secrets.yml       # paste into aegis.jwt_secret
sops --encrypt --in-place deploy/secrets/secrets.yml
git add deploy/secrets/secrets.yml
git commit -m "chore(ops): rotate JWT secret"

# 3. Re-run the role
ansible-playbook -i deploy/ansible/inventories/prod/hosts.ini \
  deploy/ansible/playbooks/panel.yml --tags configure_secrets,install_panel

# 4. Existing admin sessions are invalidated; log in again.
```

### Rotate the admin password

Same pattern, edit `aegis.admin_password` instead. The panel
hashes the new value on next startup.

## Disaster recovery

A full panel rebuild from scratch:

1. Re-provision the VPS (or stand up a new one).
2. Re-run the `panel.yml` playbook.
3. Pick the most recent OK backup from off-host storage (the
   `aegis-pg-backup download` flow).
4. `aegis-pg-restore <id> --yes`.

Total time: under 15 minutes if you have the encrypted secrets
file and a recent backup reachable. The age key + encrypted
secrets + an off-host backup is the **minimum recovery kit** —
keep all three.

## Upgrades

Pin the panel image tag in `group_vars/all.yml`:

```yaml
aegis_panel_image_tag: "0.5.0"   # no 'v' prefix; release.yml rewrites in #111
```

Then re-run the `install_panel` role. The role is idempotent:
`docker compose pull` is a no-op if the image is current, and
`up -d --remove-orphans` is a no-op if the running container's
config matches the new compose.

The release pipeline (`ghcr.io/qadversif/aegispanel`) pushes the
bare semver (`0.5.0`), the major.minor shorthand (`0.5`), and
`latest` (only for non-prerelease tags, per
`flavor: latest=auto`).

## Observability

### Health check

The panel exposes a `GET /healthz` that returns 200 when the
container is up and the Postgres connection is alive. Caddy
should be configured to use this as the healthcheck for
upstream probes.

### Logs

```bash
# Panel container
ssh aegis-deploy@panel.example.com 'docker logs --tail 200 aegis-panel'

# Node agent
ssh root@node.example.com 'journalctl -u aegis-agent -n 200 --no-pager'

# sing-box on the node
ssh root@node.example.com 'journalctl -u sing-box -n 200 --no-pager'
```

The panel uses zerolog with two output modes:

- **Development** (default): `ConsoleWriter` — colorised,
  human-readable.
- **Production** (`AEGIS_ENV=production`): JSON to stdout, one
  record per line. Pipe into your log shipper.

**v0.8.6 ops guard.** The shipped panel image bakes
`AEGIS_ENV=production` into the Dockerfile so the JSON
writer is the default out of the box. Two values override
that:

- `AEGIS_ENV=staging` — same JSON writer, useful for
  pre-prod drills where you want a "production-shaped"
  log stream but a non-prod colourised ANSI prompt for
  local debugging.
- `AEGIS_ENV=development` — the colourised writer.

The panel **refuses to boot** when `AEGIS_ENV` is the
default (`development`) AND any `AEGIS_*_BACKEND` is set
to `pg`. The rule exists because a pg-backed install is
production-shaped by definition: an operator who flips
`AEGIS_AUTH_BACKEND=pg` (or any of the eleven backend
flags) is signalling that the panel talks to a real
database, and a log shipper downstream is the
intended consumer of the panel's stderr. A
human-readable ConsoleWriter in that shape is a silent
misconfiguration — the panel is up, the requests are
flowing, but every log line is opaque to the shipper.
The guard converts that silent failure into a loud
boot-time error and tells the operator exactly which
env var to set:

```
AEGIS_ENV=development is not allowed when any AEGIS_*_BACKEND=pg
(set AEGIS_ENV=production or AEGIS_ENV=staging to confirm
logging intent; a memory-only dev install does not need this flag)
```

The pure-memory dev path (`go run ./cmd/aegis` with no
env-var setup) is unaffected — the `development` writer
is exactly what a memory-only dev install wants.

## Common pitfalls

- **`/etc/aegis/secrets.env` is bind-mounted read-only.** If you
  rotate a secret, the file on the host changes; the container
  picks it up on the next `docker compose up -d`. The
  `configure_secrets` role handles the re-decrypt; the
  `install_panel` role's handler restarts the container.
- **The age private key must be on the panel host, not the
  operator's local machine.** CI does not decrypt; the host
  decrypts on the operator's behalf. The operator's local
  machine only needs the private key to edit the encrypted
  file (re-encrypt after).
- **Postgres / Redis / NATS are NOT in the panel compose.** The
  `install_panel` role assumes the data services are
  operator-managed (managed RDS, a sibling compose stack, or
  systemd services on the same host). The panel's
  `aegis.postgres_dsn` env var wires to the external service
  through the sops+age secrets file.
- **Loopback port 8080** — the panel is not publicly accessible.
  Caddy is the public ingress. Do not change `127.0.0.1:8080` to
  `0.0.0.0:8080` in the compose file.
- **Backups are local-only** in v0.5.0. The `S3Backend` is a
  v0.5.x+ follow-up; for now, run `aegis-pg-backup download` to
  a separate host (or `rclone` the dumps off-site).
- **Restore from the UI is disabled by default.** The HTTP
  endpoint is gated by `AEGIS_BACKUPS_ALLOW_UI_RESTORE=true` (a
  sanity check, not a security boundary — the DSN is). The
  CLI is the canonical operator path.

## What this guide does NOT cover

- **Decoy sites, host pools, plans, webhooks, cascades, the
  cabinet API** — the v0.6.0+ and v1.x+ features. See the
  [roadmap](./roadmap).
- **S3-compatible backup storage** — v0.5.x+ follow-up. The
  `Store/Backend` interface is already in `internal/backups/`;
  the S3 implementation is the next PR.
- **Cosign-signed panel images** — v0.5.x+ follow-up. Sing-box
  itself is not GPG-signed by SagerNet; we trust the GitHub API
  response (TLS + GitHub's signing keys). For the panel
  images, the v0.5.x work is to add `cosign sign` + `cosign
  verify` in the release pipeline.
- **A high-availability topology.** Aegis is single-instance.
  A second panel host is not in scope; the canonical recovery
  path is restore-from-backup onto a fresh VM.

## Where to next?

- [Quickstart](./guide/quickstart) — a shorter "first 5 minutes"
  flow for the impatient.
- [Architecture](./guide/architecture) — the full design document.
- [Security policy](./security) — the disclosure policy and
  the threat model.
- [Secrets workflow](../deploy/secrets/README.md) — the sops+age
  flow, step by step.
- [Developer guide](./developer/) — for the panel's own
  contributors.
