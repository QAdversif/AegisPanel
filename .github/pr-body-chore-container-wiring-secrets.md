<!--
This file is the PR body for #124. It is committed
alongside the code so the body is part of the PR's
git history (the `gh pr create --body-file` path
mirrors the `git log` PR for posterity).
-->

# chore(ops): install_panel role + prod compose + secrets.env mount

The third leg of the v0.5.0 sops+age indirection.
PR #119 added the `configure_secrets` role that
writes `/etc/aegis/secrets.env` on the panel host.
PR #120/121 shipped the v0.5.0 backup surface.
This PR adds the
three pieces that consume `secrets.env` end-to-end on
a panel host:

- A production-only `docker-compose.prod.yml` that
  bind-mounts `/etc/aegis/secrets.env:ro` into the
  panel container via `env_file:` and publishes
  8080 on the loopback only (Caddy or any other
  reverse proxy is the public ingress).
- An `install_panel` Ansible role (defaults plus
  tasks plus handlers) that drops the compose
  file, pulls the image, and starts the stack.
- A `playbooks/panel.yml` that runs `bootstrap_node`
  → `configure_secrets` → `install_panel` on a
  fresh panel host.
- A secondary `EnvironmentFile=-/etc/aegis/secrets.env`
  in `aegis-agent.service` (the `-` prefix tells
  systemd to silently skip a missing file; dev
  hosts without `configure_secrets` are
  unaffected).

## What this PR ships

- `deploy/docker/docker-compose.prod.yml` (new, ~80
  lines) — the production panel stack.
  - Image: `ghcr.io/qadversif/aegispanel:${AEGIS_PANEL_IMAGE_TAG}`,
    default `latest` (the v0.5.0 release pipeline
    pushes the panel under the bare semver tag
    per the release workflow rewrite in #111).
  - `env_file: - path: /etc/aegis/secrets.env,
    required: true` — the secrets file is
    read-only mounted into the container; the
    file is mode 0600 on the host, the
    container's `nonroot:nonroot` user (uid
    65532 in distroless) reads it, and the mount
    is RO so a process that escapes the panel
    cannot tamper with the file.
  - `ports: 127.0.0.1:8080:8080` — loopback
    only. The reverse proxy is the public
    ingress; the panel does not bind the
    public interface directly.
  - `volumes: /var/lib/aegis:/var/lib/aegis:rw` —
    reserved for the v0.5.x backups volume
    (the current PR mounts the directory but
    the panel does not yet write to it).
  - No data services (Postgres / Redis / NATS)
    in this compose. The operator manages
    those out-of-band; the panel's DSN lives
    in `aegis.postgres_dsn` in the sops+age
    secrets file.

- `deploy/ansible/roles/install_panel/` (new, three
  files):
  - `defaults/main.yml` — `aegis_panel_image_tag`,
    `aegis_panel_compose_path`,
    `aegis_panel_compose_src`.
  - `tasks/main.yml` — refuses to run without
    `/etc/aegis/secrets.env`, drops the compose
    file, `docker compose pull` (idempotent),
    `docker compose up -d --remove-orphans`,
    prints `docker compose ps` as a summary.
  - `handlers/main.yml` — `restart aegis-panel`
    (compose restart) for re-renders.

- `deploy/ansible/playbooks/panel.yml` (new) — the
  canonical three-role deploy for a panel host.
  Run:
  ```bash
  cd deploy/ansible
  ansible-playbook -i inventories/prod/hosts.ini playbooks/panel.yml
  ```

- `deploy/ansible/roles/install_agent/files/aegis-agent.service`
  — added
  `EnvironmentFile=-/etc/aegis/secrets.env` after
  the per-node `EnvironmentFile=/etc/aegis/agent.env`.
  The leading `-` means "silently skip if missing",
  so dev hosts without `configure_secrets` are
  unaffected. On panel hosts the agent picks up
  any future AEGIS_* secret (e.g. a shared metrics
  endpoint URL) from the canonical secrets
  source. Per-node values in `agent.env` still
  take precedence on a key collision (systemd's
  later-`EnvironmentFile`-wins rule).

## What this PR does NOT ship

- **The data services (Postgres, Redis, NATS) are
  not in this compose.** The panel's data layer
  is operator-managed. A future PR can ship a
  sibling `docker-compose.data.yml` for
  single-host dev/prod paths; v0.5.0 ships
  panel-only.
- **The reverse proxy (Caddy) is still installed
  per-node** by `install_caddy`. A future PR adds
  a panel-side Caddy that fronts the panel
  container on `127.0.0.1:8080`.
- **A healthcheck for the panel container.** The
  distroless image has no shell; a v0.5.x
  follow-up ships a tiny healthcheck binary
  inside the image, or a sibling `wget` shim
  via buildx.

## Operator workflow

```bash
# 1. One-time: provision the panel host
#    (idempotent re-runs are no-ops).
ansible-playbook -i deploy/ansible/inventories/prod/hosts.ini \
  deploy/ansible/playbooks/panel.yml

# 2. The role drops /etc/aegis/docker-compose.prod.yml,
#    runs `docker compose pull`, and `docker compose up -d`.
#    The panel reads /etc/aegis/secrets.env (mounted RO
#    into the container via env_file) and starts.

# 3. Verify
docker compose -f /etc/aegis/docker-compose.prod.yml ps
curl -s http://127.0.0.1:8080/api/v1/health
```

## Verification

Local:

```bash
# YAML syntax
python -c "import yaml; yaml.safe_load(open('deploy/docker/docker-compose.prod.yml')); yaml.safe_load(open('deploy/ansible/roles/install_panel/defaults/main.yml')); yaml.safe_load(open('deploy/ansible/roles/install_panel/tasks/main.yml')); yaml.safe_load(open('deploy/ansible/playbooks/panel.yml'))"

# ansible-lint runs in CI
ansible-lint deploy/ansible/roles/install_panel/
ansible-lint deploy/ansible/playbooks/panel.yml
```

The CI matrix runs the same playbook on the
Ubuntu + Debian test hosts. v0.5.0 will add a
test host that exercises the secrets.env mount
end-to-end (decrypt → mount → panel reads
AEGIS_JWT_SECRET) once the secrets workflow
itself is part of the CI matrix; that is a
v0.5.x follow-up.
