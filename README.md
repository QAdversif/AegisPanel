# Aegis — VPN Control Panel

> **Aegis** is a self-hosted control panel for multi-protocol VPN
> services. MVP v1.0 runs sing-box on every node; the CoreProvider
> abstraction lets a second provider (Xray) ship in v2.0+ without
> UI surgery. BYO Node, Cascade topology (Xray-only, v2.2+), MCP-driven
> (v2.6+), full-client compatibility (Hiddify, v2rayNG/N, Streisand,
> Clash), anti-censorship via Caddy + decoy sites + port masquerading.
>
> **Stack:** Go 1.22+ backend, Vue 3 + TypeScript frontend, Caddy,
> fail2ban, PostgreSQL, Redis. **License:** AGPL-3.0.
> **Tenancy:** single-tenant (one panel = one operator, multiple admin
> accounts). See [`ARCHITECTURE.md`](./ARCHITECTURE.md) for full
> design and [`docs/adr/0003-mvp-singbox-vertical-slice.md`](./docs/adr/0003-mvp-singbox-vertical-slice.md)
> for the MVP strategy.

## Status

**v0.1.0-mvp-render.** The renderable MVP is the first of three
sub-phases on the path to v1.0.0-mvp-soft-launch:

| Milestone | Status | What |
|---|---|---|
| v0.1.0-mvp-render | **shipped** | The renderable MVP — admin UI + subscription endpoint + sing-box (no-op core in dev) |
| v0.2.0-mvp-agent | next | Real `cores/singbox.Apply` (dev) + per-node agent lifecycle |
| v0.3.0-mvp-byo-node | planned | BYO Node flow + Ansible provisioning |
| v0.4.0-mvp-batched | planned | Batched Apply + HY2 reconnect-under-reload load-test |
| v1.0.0-mvp-soft-launch | planned | Polish + clean-VM smoke + on-prem install |

See [`ARCHITECTURE.md`](./ARCHITECTURE.md) §21 for the full roadmap
and [`KNOWN_LIMITATIONS.md`](./KNOWN_LIMITATIONS.md) for the v0.1.0
gap list.

## Repository layout (monorepo)

```
aegis/
├── ARCHITECTURE.md         # the design document (v9)
├── CHANGELOG.md            # per-version release notes
├── KNOWN_LIMITATIONS.md    # v0.1.0 gap list
├── README.md               # this file
├── LICENSE                 # AGPL-3.0
├── Makefile                # top-level orchestration
├── .gitattributes          # LF / CRLF policy (LF in repo, CRLF on .bat/.cmd/.ps1)
├── backend/                # Go 1.22+ service
│   ├── cmd/aegis/          # the `aegis` binary entrypoint
│   ├── internal/           # auth / config / cores / db / hosts / inbounds / migrations / nodes / panelcfg / router / subscription
│   ├── migrations/         # native migrator + 0001..NNNN.sql
│   └── testutil/           # shared Postgres test fixtures
├── frontend/               # Vue 3 + TS admin UI (shadcn-vue)
│   ├── src/components/ui/  # 15 base shadcn-vue components (PR-B)
│   ├── src/components/     # Form / DataTable / FormField (PR-C)
│   ├── src/api/services/   # typed API clients (PR-D)
│   ├── src/schemas/        # zod schemas (PR-C)
│   ├── src/views/          # Dashboard / Nodes / Inbounds / Hosts / Subscription / Users / Settings / Login
│   ├── src/i18n/           # vue-i18n (en + ru)
│   └── tools/scripts/      # check-raw-text.mjs (i18n lint)
├── deploy/                 # Ansible, Caddy, fail2ban, docker, systemd
├── docs/
│   ├── adr/                # Architecture Decision Records
│   └── guide/              # rendered ARCHITECTURE.md (for GitHub Pages)
└── tools/scripts/          # branch-start.sh, smoke-frontend.sh
```

## Quick start (development)

Prerequisites: Go 1.22+, Node 20+, npm (or pnpm), docker + docker compose.

### 1. Backend

```bash
cd backend
# Test (memory stores, fast):
go test ./...
# Test with Postgres (full integration):
docker run -d --name aegis-test-pg -p 5432:5432 \
  -e POSTGRES_USER=aegis -e POSTGRES_PASSWORD=aegis \
  -e POSTGRES_DB=aegis_test postgres:16-alpine
AEGIS_HOST_BACKEND=pg AEGIS_NODES_BACKEND=pg \
  AEGIS_HOSTS_BACKEND=pg AEGIS_INBOUNDS_BACKEND=pg \
  AEGIS_SUBSCRIPTION_BACKEND=pg AEGIS_PANELCFG_BACKEND=pg \
  AEGIS_DATABASE_URL=postgres://aegis:aegis@localhost:5432/aegis_test \
  go test ./...
# Run the dev panel (memory stores by default):
go run ./cmd/aegis
```

### 2. Frontend

```bash
cd frontend
npm install
npm run dev          # vite dev server on :5173, proxies /api + /sub to :8080
npm run type-check   # vue-tsc
npm run lint         # eslint + check-raw-text.mjs
npm run build        # vue-tsc + vite build
```

### 3. End-to-end smoke

```bash
# Backend on :8080, frontend dev or build:
./tools/scripts/smoke-frontend.sh
# (or with a custom port)
./tools/scripts/smoke-frontend.sh --port 5180
```

The smoke builds the frontend, starts `vite preview`, and verifies
the served HTML + asset graph. It does NOT exercise the CRUD
flows — those have Go integration tests.

## Contributing

- **Branch naming:** `tools/scripts/branch-start.sh <type> <scope/name>`.
  Examples: `feat frontend/login`, `fix backend/argon2id`,
  `chore repo/gitattributes-lf`, `docs architecture-rev10`.
- **Commits:** conventional commits (`feat:` / `fix:` / `docs:` /
  `refactor:` / `chore:` / `test:`). Multi-line commits via
  `git commit --file .git/COMMIT_EDITMSG.<name>`.
- **PRs:** one PR per work unit. `gh pr create --body-file
  .github/pr-body-<name>.md`. Merges use
  `gh pr merge --squash --delete-branch --admin`.
- **i18n:** every user-facing string goes through `t('key')`. Run
  `node tools/scripts/check-raw-text.mjs` locally; the CI gate
  runs the same script.

## License

AGPL-3.0-or-later. See [LICENSE](./LICENSE).

The Aegis project is single-tenant and AGPL-licensed: any operator
who runs a modified version of the panel is required to publish
the modifications. The operator's modifications and the upstream
Aegis source are both governed by this license.
