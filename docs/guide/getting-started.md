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
