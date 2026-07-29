---
title: Getting started
---

# Getting started

> The skeleton is still being assembled (Phase 0). This page documents
> the *intended* workflow; commands marked with `🚧` are not yet wired
> up.

## Prerequisites

- **Go 1.22+** — for the panel backend.
- **Node.js 20+** and **pnpm 9+** (or `npm`) — for the admin UI.
- **Docker 24+** and **Docker Compose v2** — for the local dev stack.
- **Make** — to drive the top-level targets.
- **Ansible 9+** — for the panel / node provisioning playbooks.

## Clone the repository

```bash
git clone <your-fork-or-clone-url> aegis
cd aegis
```

## Bring up the dev stack

```bash
make dev
```

This runs, in parallel:

- `make docker-dev` — Postgres, Redis, NATS, ClickHouse, MinIO, and
  Caddy via `deploy/docker/docker-compose.dev.yml`.
- `make backend` — the Go panel on `:8080`.
- `make frontend` — the Vite dev server on `:5173`.

The first run will:

1. Pull docker images.
2. Apply migrations from `backend/migrations/`.
3. Generate a self-signed Caddy certificate for `localhost`.

## Sanity checks

- `curl -k https://localhost/healthz` — returns 200 from Caddy →
  the panel.
- Open `http://localhost:5173` — the admin UI should render the
  dashboard with `Panel: …` and zero nodes / users.

## Tear down

```bash
make dev-down
```

## Where to next?

- [API reference](../api/) — once endpoints land in Phase 1.
- [Architecture](./architecture) — the full design.

## Operator quickstart (v0.5.0+)

The two-step "install the panel" / "register a node" loop is the
canonical first-run path on a fresh VPS. The Ansible roles live
under `deploy/ansible/roles/`; `tools/scripts/install-pre-push.sh`
in the repo root installs a git hook that runs the CI-equivalent
checks locally before any push (so a red build is caught before
the CI round-trip).

```bash
# 1. Pull the sops+age encrypted secrets onto the panel host
#    (the sops+age workflow is documented in
#    `deploy/secrets/README.md`; v0.5.0 ships the
#    `configure_secrets` role that does this in one step).
ansible-playbook -i deploy/ansible/inventories/prod/hosts.ini \
  deploy/ansible/playbooks/panel.yml

# 2. From the panel UI, register the first node (or run
#    `playbooks/node.yml` against an existing SSH endpoint).
#    The role installs sing-box from the GitHub release tarball,
#    looks up the SHA-256 via the GitHub Releases API at install
#    time, and verifies the download with `get_url checksum:`.
#    The v0.4.0 hardcoded hash is gone — bumping
#    `aegis_singbox_version` in `group_vars/all.yml` is now a
#    one-line change.
ansible-playbook -i deploy/ansible/inventories/prod/hosts.ini \
  deploy/ansible/playbooks/node.yml
```

The sops+age indirection is the v0.5.0+ default. Operators on
v0.4.0 still set `AEGIS_*_SECRET` env vars directly on the
host; the `configure_secrets` role is a no-op on those hosts.
