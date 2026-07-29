---
title: Getting started
---

# Getting started

This page documents the **dev stack** — Postgres, Redis, NATS,
and the panel + UI on your laptop. If you are an **operator**
installing the panel on a VPS, the
[quickstart](./quickstart) is the right entry; the
[operator guide](../operator-guide) is the long form.

## Prerequisites

- **Go 1.22+** — for the panel backend.
- **Node.js 20+** and **pnpm 9+** (or `npm`) — for the admin UI.
- **Docker 24+** and **Docker Compose v2** — for the local dev stack.
- **Make** — to drive the top-level targets.
- **Ansible 9+** — for the panel / node provisioning playbooks
  (only needed for the operator path).

## Clone the repository

```bash
git clone <your-fork-or-clone-url> aegis
cd aegis
```

## Install the pre-push gate (recommended)

```bash
make pre-pr-install
```

This installs a `.git/hooks/pre-push` hook that runs the
CI-equivalent checks locally before every push. A red build is
caught at the laptop, not 4 minutes into a CI run. The script
lives at `tools/scripts/pre-pr.sh`; the same script is also
the entry point for ad-hoc checks (`tools/scripts/pre-pr.sh
--backend`, `--frontend`, `--docs`, `--quick`).

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

- [Quickstart](./quickstart) — 5 minutes from a fresh VPS to a
  panel running.
- [Operator guide](../operator-guide) — the full install +
  daily-ops reference.
- [Security policy](../security) — the threat model and
  disclosure flow.
- [Architecture](./architecture) — the full design.
- [Developer guide](../developer/) — for the panel's own
  contributors.
