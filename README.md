# Aegis — VPN Control Panel

> **Aegis** is a self-hosted control panel for multi-protocol VPN
> services. **v0.7.0** ships the full admin surface end-to-end:
> sing-box on every node, BYO Node bootstrap with real `aegis-agent`,
> user / host / plan CRUD, subscription render in sing-box / Clash /
> base64 / HTML formats, audit log, **backups** (with operator CLI),
> and **outgoing webhooks** (HMAC-signed + DLQ). The CoreProvider
> abstraction lets a second provider (Xray) ship in v2.0+ without
> UI surgery. AGPL-3.0. Single-tenant.
>
> **Stack:** Go 1.26+ backend, Vue 3 + TypeScript frontend
> (Vite + shadcn-vue), PostgreSQL, Caddy, fail2ban, sops+age
> secrets. See [`ARCHITECTURE.md`](./ARCHITECTURE.md) for the
> full design and [`docs/adr/0003-mvp-singbox-vertical-slice.md`](./docs/adr/0003-mvp-singbox-vertical-slice.md)
> for the MVP strategy.

## Status

**v0.7.1 — webhook follow-up batch — shipped.** v0.7.1 is the
follow-up to v0.7.0. It closes every "deferred to v0.7.x"
item: call-site wiring (`Service.Dispatch` from every
mutating handler), sops+age envelope on
`webhook_endpoints.secret`, background worker for the
retry loop, shared zod schema in
`frontend/src/schemas/webhook.ts`, and the events
multi-select in the WebhooksView create / edit dialogs.
The full release ladder:

| Milestone | Status | Notes |
| --- | --- | --- |
| `v0.1.0-mvp-render` | **shipped** | Renderable MVP — admin UI + subscription endpoint + sing-box (no-op core in dev) |
| `v0.2.0-mvp-agent` | **shipped** | Per-`sub_token` rate limit, OpenAPI codegen, audit log, operator CLI, per-resource handler surfaces |
| `v0.3.0-mvp-byo-node` | **shipped** | BYO Node flow: SSH probe + agent install + state machine |
| `v0.4.0-mvp-batched` | **shipped** | `BatchedApplier` + real apply transport + `install_singbox` Ansible role + `aegis-agent` writes config to disk and reloads sing-box |
| `v0.4.0-d` | **shipped** | `internal/users` data layer (d.1) + Path C consolidation (d.r1–d.r4) |
| `v0.4.0` (tag) | **shipped** | Aggregate of d.1 / d.r1 / d.r2 / d.r3 / d.r4 |
| `v0.4.0-post` | **shipped** | Release workflow fixes (#102 / #103 / #104 / #111) — no application code change |
| `v0.5.0` | **shipped** | sops+age secrets, backup/restore (pkg + UI + CLI), pre-PR gate, GitHub-API sing-box SHA-256, container wiring for secrets, operator guide + SECURITY + quickstart |
| `v0.6.0` | **shipped** | `internal/plans` — plan catalog promoted from the v0.3.0 table stub to a full CRUD surface |
| `v0.7.0` | **shipped** | `internal/webhooks` — outgoing-webhook surface with HMAC signing, retry with exponential backoff, DLQ |
| `v0.7.1` | **shipped** | Webhook call-site wiring, sops+age envelope on `webhook_endpoints.secret`, background retry worker, events multi-select, shared zod schema, plus the post-v0.7.0 Go+frontend dependency batch (#141–#144) and the docs sync (#145) |
| `v0.8.0` | planned | `internal/notifications` (Telegram + generic webhook via n8n) |
| `v0.9.0` | planned | Smoke test on fresh VM in CI |
| `v1.0.0-mvp-soft-launch` | planned | GA tag — minimum surface for the public release |

See [`docs/ROADMAP.md`](./docs/ROADMAP.md) for the milestone ladder,
[`CHANGELOG.md`](./CHANGELOG.md) for the per-PR release notes, and
[`KNOWN_LIMITATIONS.md`](./KNOWN_LIMITATIONS.md) for the current gap
list.

## Repository layout (monorepo)

```
aegis/
├── ARCHITECTURE.md         # the design document (v9.3)
├── CHANGELOG.md            # per-version release notes (Keep a Changelog)
├── KNOWN_LIMITATIONS.md    # current gap list (v0.7.1)
├── README.md               # this file
├── LICENSE                 # AGPL-3.0
├── Makefile                # top-level orchestration
├── .gitattributes          # LF / CRLF policy (LF in repo, CRLF on .bat/.cmd/.ps1)
├── .markdownlint.json      # docs lint config
├── backend/                # Go 1.26+ service
│   ├── cmd/
│   │   ├── aegis/          # the `aegis` panel binary
│   │   ├── aegis-agent/    # the per-node Go agent (writes sing-box config + reloads)
│   │   ├── aegis-pg-backup/    # operator-side backup CLI
│   │   └── aegis-pg-restore/   # operator-side restore CLI (separate binary, safety boundary)
│   ├── internal/           # 17 packages: audits, auth, backups, bootstrap, config, cores, db, hosts, inbounds, migrations, nodes, obs, panelcfg, plans, ratelimit, router, subscription, users, webhooks
│   ├── migrations/         # 16 SQL files (0001..0016)
│   └── testutil/           # shared Postgres test fixtures
├── frontend/               # Vue 3 + TS admin UI (shadcn-vue)
│   ├── src/components/ui/  # 25 base shadcn-vue components
│   ├── src/components/     # Form / DataTable / FormField (typed wrapper around vee-validate + zod)
│   ├── src/api/services/   # typed API clients (auth / backups / nodes / inbounds / hosts / users / plans / panelcfg / audits / webhooks)
│   ├── src/schemas/        # zod schemas
│   ├── src/views/          # Dashboard / Nodes / Inbounds / Hosts / Plans / Subscription / Users / Webhooks / Backups / Settings / Audits / Profile / Login
│   ├── src/i18n/           # vue-i18n (en + ru)
│   ├── src/types/          # aegis.ts (hand mirror) + api.d.ts (codegen from openapi.yaml)
│   └── tools/scripts/      # check-raw-text.mjs (i18n lint) + check-codegen.mjs (openapi-typescript freshness)
├── deploy/
│   ├── ansible/            # bootstrap_node / configure_secrets / install_agent / install_caddy / install_fail2ban / install_singbox / install_panel / setup_decoy roles + playbooks
│   ├── secrets/            # sops+age encrypted secrets (secrets.example.yml is committed encrypted; secrets.yml is gitignored)
│   ├── docker/             # docker-compose.prod.yml.j2 template
│   └── caddy/              # Caddyfile templates
├── docs/
│   ├── adr/                # Architecture Decision Records (0001–0004)
│   ├── api/                # API reference (rendered from openapi.yaml)
│   ├── archive/            # superseded docs (e.g. ARCHITECTURE_ADDENDUM_1)
│   ├── developer/          # developer guide (module overview, testing, contributing)
│   ├── guide/              # rendered ARCHITECTURE.md + quickstart + getting-started
│   ├── operator-guide.md   # the canonical "fresh VPS → panel" reference
│   ├── SECURITY.md         # threat model + disclosure flow
│   ├── ROADMAP.md          # the milestone ladder
│   ├── README.md           # docs index
│   ├── KNOWN_LIMITATIONS.md  # (root) gap list
│   └── openapi.yaml        # OpenAPI 3.0 spec (codegen source of truth)
└── tools/scripts/          # pre-pr.sh, install-pre-push.sh, branch-start.sh, smoke-frontend.sh, release.sh, backup.sh, restore.sh
```

## Quick start

For the **operator path** (a single VPS behind a public domain,
secrets on disk, real users, real backups) the
[**operator guide**](./docs/operator-guide.md) is the canonical
entry. The five-minute version is the
[**quickstart**](./docs/guide/quickstart.md).

For the **development path** (Postgres + Redis + NATS + panel +
UI on a laptop) the
[**getting started**](./docs/guide/getting-started.md) page is the
right entry. The TL;DR:

```bash
# 1. Clone
git clone https://github.com/QAdversif/AegisPanel.git aegis
cd aegis

# 2. Install the pre-push gate (recommended)
make pre-pr-install   # installs .git/hooks/pre-push delegating to tools/scripts/pre-pr.sh

# 3. Bring up the dev stack
make dev              # Postgres + Redis + NATS + panel + UI on :5173 (UI) / :8080 (panel)

# 4. Smoke
./tools/scripts/smoke-frontend.sh
```

Prerequisites: **Go 1.26+**, **Node.js 20+**, **npm** (the project
is standardized on `npm ci` against the committed
`package-lock.json`; `pnpm` is no longer used — see PR #87), **Docker
24+** and **Docker Compose v2**, **Make**.

## What's where

- **Architecture** — [`ARCHITECTURE.md`](./ARCHITECTURE.md) (Russian, v9.3).
  Source of truth for the design.
- **Roadmap** — [`docs/ROADMAP.md`](./docs/ROADMAP.md). Milestone ladder
  with per-PR status.
- **Operator guide** — [`docs/operator-guide.md`](./docs/operator-guide.md).
  The end-to-end "from a fresh VPS to a panel that serves real users"
  flow.
- **Quickstart** — [`docs/guide/quickstart.md`](./docs/guide/quickstart.md).
  The five-minute operator path.
- **Security policy** — [`docs/SECURITY.md`](./docs/SECURITY.md). Threat
  model, disclosure flow, supply-chain trust.
- **API reference** — [`docs/api/`](./docs/api/index.md). Rendered from
  `docs/openapi.yaml` (currently 0.7.1).
- **CHANGELOG** — [`CHANGELOG.md`](./CHANGELOG.md). Per-version release
  notes (Keep a Changelog format).
- **Known limitations** — [`KNOWN_LIMITATIONS.md`](./KNOWN_LIMITATIONS.md).
  Open gaps and the milestone that closes each.
- **Developer guide** — [`docs/developer/`](./docs/developer/index.md).
  Module overview, testing, contributing.

## Contributing

- **Branch naming:** `feat/<scope>/<name>`, `fix/<scope>/<name>`,
  `chore/<scope>/<name>`, `refactor/<scope>/<name>`, `docs/<scope>/<name>`.
  Branch off `main`; `main` is the integration branch (no `develop`).
- **Commits:** [Conventional Commits](https://www.conventionalcommits.org/).
  Avoid backticks in `-m` strings (PowerShell execution policy). Multi-line
  commits: write the message to a `.git-commit-*.txt` and `git commit
  --file <path>`. Throwaway drafts are gitignored.
- **PRs:** one PR per work unit. `gh pr create --body-file
  .github/pr-body-<name>.md`. Merges use
  `gh pr merge --admin --squash --delete-branch`.
- **Pre-PR gate:** run `tools/scripts/pre-pr.sh` (or the installed
  pre-push hook) before pushing. The gate runs gofmt, golangci-lint
  v2, vue-tsc, eslint, markdownlint-cli2, `go test -short`, and
  `npm run codegen:check`. It catches ~80% of the issues that would
  otherwise bounce in CI.
- **i18n:** every user-facing string goes through `t('key')`. Run
  `node frontend/tools/scripts/check-raw-text.mjs` locally; the CI gate
  runs the same script.
- **License header** in every source file:
  `// SPDX-License-Identifier: AGPL-3.0-or-later` (Go / shell / SQL) or
  `<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->` (Vue / TS).

## License

AGPL-3.0-or-later. See [LICENSE](./LICENSE).

The Aegis project is single-tenant and AGPL-licensed: any operator
who runs a modified version of the panel is required to publish the
modifications. The operator's modifications and the upstream Aegis
source are both governed by this license.
