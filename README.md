# Aegis — VPN Control Panel

> **Aegis** is a self-hosted control panel for multi-protocol VPN
> services. **v0.8.9** (latest tagged release, 2026-08-08) ships the
> full admin surface end-to-end: sing-box on every node, BYO Node
> bootstrap with real `aegis-agent`, user / host / plan CRUD,
> subscription render in sing-box / Clash / base64 / HTML formats,
> audit log, **backups** (with operator CLI), **outgoing webhooks**
> (HMAC-signed + DLQ), the **Phase 2 multi-user sing-box render**
> (per-(user, inbound) credentials, multi-user `users: [...]` arrays,
> narrowed BatchedApplier fan-out, per-user sub URL), and the
> **v0.8.x operations batch**: persistent panel SSH key
> (ed25519 + sops+age envelope), three-way auth radio (key / password
> / stored) on the provision form, HTTP `auth/me` pg-backend fix,
> full HTTP admin surface for `user_inbound_credentials`,
> `aegis admin node rotate-panel-key` CLI + HTTP mirror,
> "Show stored key" debug surface, JSON-logs-in-production config
> guard, `nodes.Service.RefreshAgentBearer` operator-recovery
> path, BatchedApplier 401→auto-refresh integration, and
> **cosign re-sign + verify on every release** (3 new
> `release.yml` steps: 30s settle + re-sign panel + re-sign UI;
> closes the v0.8.x `cosign re-signing on every release` row).
> The CoreProvider abstraction lets a second provider (Xray) ship
> in v2.0+ without UI surgery. AGPL-3.0. Single-tenant.
>
> **Stack:** Go 1.26+ backend, Vue 3 + TypeScript frontend
> (Vite + shadcn-vue), PostgreSQL, Caddy, fail2ban, sops+age
> secrets, cosign-signed OCI images. See
> [`ARCHITECTURE.md`](./ARCHITECTURE.md) (v9.5) for the full design
> and [`docs/adr/0003-mvp-singbox-vertical-slice.md`](./docs/adr/0003-mvp-singbox-vertical-slice.md)
> for the MVP strategy.

## Status

**v0.8.9 — cosign re-sign + verify on every release — shipped.**
The release pipeline now waits 30s after the first `cosign sign`
(let GHCR OIDC settle), then re-signs each image and runs
`cosign verify` with the same flags a consumer would use
(`--certificate-identity-regexp "https://github.com/QAdversif/AegisPanel/.*"`,
`--certificate-oidc-issuer https://token.actions.githubusercontent.com`).
Three failure modes closed: (1) tag-mutation drift on `latest`
between sign and pull; (2) sign-step OIDC flake recovery without
a full `workflow_dispatch` + rebuild; (3) explicit `cosign verify`
audit trail in the workflow log. Pure workflow change, no code
touched. Release workflow run #190 (commit `035c77e5`).

**v0.8.0–v0.8.8 — multi-user + operator-recovery batch — shipped.**
The full v0.8.x line:

- **v0.8.0** — Phase 2 multi-user sing-box render end-to-end
  (data model + renderer + builder + BatchedApplier narrow +
  per-user subscription render). The migration landscape is
  `0001..0019` here; migration `0020` lands in v0.8.1.
- **v0.8.1** — auto-deploy bootstrap batch: shared
  `internal/crypto/envelope` package
  (X25519+ChaCha20-Poly1305, multi-recipient for key rotation),
  password-based first auth for BYO Node, persistent panel
  SSH key (ed25519 + envelope encrypt + `authorized_keys` push).
  Three-way auth radio (key / password / stored) in the UI.
  Migration `0020_node_ssh_private_key.sql`. OpenAPI spec
  bumped to `0.8.1`.
- **v0.8.2** — server-side `auth.me` fix on pg backend
  (`auth.Store.GetByID` + `MemoryStore`/`PgStore` impl, closes
  the v0.8.0 `auth.me === null` 500 on pg); HTTP admin surface
  for `user_inbound_credentials` (`/api/v1/credentials/`
  mount + `ScopeCredentials` + OpenAPI + Credentials tab).
- **v0.8.3** — operator-side CLI `aegis admin node
  rotate-panel-key <node-uuid> --key <path>` for v0.3.0..v0.7.x
  nodes (generates fresh ed25519 keypair, pushes public half
  to `authorized_keys`, seals private half with the operator's
  age envelope).
- **v0.8.4** — HTTP mirror of v0.8.3: `POST
  /api/v1/nodes/{id}/rotate-panel-key` + NodesView dropdown
  entry (visible for `online`/`offline`/`draining`/`disabled`,
  hidden for `new`).
- **v0.8.5** — "Show stored key" debug surface: `GET
  /api/v1/nodes/{id}/stored-key` returns the public-key line
  - SHA-256 fingerprint (private key never leaves the panel
  process). The read is audited as `node.stored-key.read`.
- **v0.8.6** — JSON logs in production, hardened: the
  `AEGIS_ENV=production` → `zerolog.JSON` writer switch gets a
  `Config.validate()` guard that refuses to boot when
  `AEGIS_ENV` is `development` AND any `AEGIS_*_BACKEND=pg`
  (silent misconfig → loud boot error). New
  `backend/internal/config/config_test.go` with 8 test
  functions / 18 sub-tests covering the guard.
- **v0.8.7** — refresh agent bearer: `nodes.Service.RefreshAgentBearer`
  decrypts the stored panel SSH key, SSHes into the node,
  reads `/etc/aegis/agent.env`, parses `AEGIS_AGENT_BEARER`,
  updates `nodes.agent_bearer`. The recovery path for
  "agent regenerated its bearer out-of-band". 30 + 11 unit
  tests.
- **v0.8.8** — BatchedApplier 401→auto-refresh: a 401 from
  `POST /v1/apply` triggers a refresh + retry without operator
  intervention. `singbox.NodeResolver` extended with
  `RefreshBearer(ctx, id) (string, error)`. One retry only,
  no loop. 500/404 do NOT trigger refresh.

The release ladder:

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
| `v0.7.2` | **shipped** | Audit batch closeout: God-object `main.go` extracted into `internal/app.Build` (#156); real BatchedApplier FlushFn + Enqueue from user/inbound services (#157); end-to-end integration test against a real Postgres (#158) |
| `v0.8.0` | **shipped** | Phase 2 multi-user sing-box render end-to-end (#167 data model, #168 renderer, #169 builder + BatchedApplier narrow, #170 subscription per-user render); audit-log call-site wiring into every mutating service (#166); frontend dependency batch — TS / CSS / axios / vue-tsconfig / postcss (#159, #161, #163, #165) |
| `v0.8.1` | **shipped** | Auto-deploy bootstrap batch: shared `internal/crypto/envelope` package (#177 refactor); `brace-expansion` 5.0.8 → 5.0.9 CVE (#178 chore); password-based first auth + persistent node SSH key (#179 feat); three-way radio in the provision UI (#180 feat). Migration 0020 (`nodes.ssh_private_key_ciphertext` BYTEA). OpenAPI spec bumped to 0.8.1. |
| `v0.8.2` | **shipped** | Server-side `auth.me` fix on pg backend (PR #182); HTTP admin surface for `user_inbound_credentials` (`/api/v1/credentials/` mount + `ScopeCredentials` + OpenAPI + Credentials tab in the user detail page) — PR #183 |
| `v0.8.3` | **shipped** | Operator-side CLI `aegis admin node rotate-panel-key <node-uuid> --key <path>` for v0.3.0..v0.7.x nodes (PR #184) |
| `v0.8.4` | **shipped** | HTTP mirror of the v0.8.3 rotate-panel-key CLI: `POST /api/v1/nodes/{id}/rotate-panel-key` + NodesView dropdown entry (PR #185) |
| `v0.8.5` | **shipped** | "Show stored key" debug surface in NodesView: `GET /api/v1/nodes/{id}/stored-key` returns the public-key line + SHA-256 fingerprint (PR #186) |
| `v0.8.6` | **shipped** | JSON logs in production, hardened with `Config.validate()` guard (PR #187) |
| `v0.8.7` | **shipped** | Refresh agent bearer: `nodes.Service.RefreshAgentBearer` (PR #188) |
| `v0.8.8` | **shipped** | BatchedApplier 401→auto-refresh integration (PR #189) |
| `v0.8.9` | **shipped** | Release workflow hardening: cosign re-sign + verify on every release (PR #190). Pure workflow change, no code touched. |
| `v0.8.x` | in progress | Host → node mapping in Builder filter (PR #192 shipped; closes the v0.8.x `host→node mapping` row); subscription URL display in UsersView (PR #193 shipped; closes the v0.8.x `subscription URL display` row); inbound-templates work (per-tenant `Params` defaults); UX follow-ups (merged "Add node + Provision" dialog, shadcn-vue `RadioGroup` primitive); operations polish (pre-existing eslint warnings cleanup as a `chore` PR) |
| `v0.9.0` | planned | Smoke test on fresh VM in CI (terraform + ansible + boot log artifact) |
| `v1.0.0-mvp-soft-launch` | planned | GA tag — minimum surface for the public release (requires per-user credential filter in Builder — known security gap, see [KNOWN_LIMITATIONS.md](./KNOWN_LIMITATIONS.md)) |

See [`docs/ROADMAP.md`](./docs/ROADMAP.md) for the milestone ladder,
[`CHANGELOG.md`](./CHANGELOG.md) for the per-PR release notes, and
[`KNOWN_LIMITATIONS.md`](./KNOWN_LIMITATIONS.md) for the current gap
list.

## Repository layout (monorepo)

```
aegis/
├── ARCHITECTURE.md         # the design document (v9.5)
├── CHANGELOG.md            # per-version release notes (Keep a Changelog)
├── KNOWN_LIMITATIONS.md    # current gap list (v0.8.9)
├── README.md               # this file
├── LICENSE                 # AGPL-3.0
├── Makefile                # top-level orchestration
├── .gitattributes          # LF / CRLF policy (LF in repo, CRLF on .bat/.cmd/.ps1)
├── .markdownlint.json      # docs lint config
├── .markdownlint-cli2.yaml # markdownlint-cli2 scratch-file exclusions
├── backend/                # Go 1.26+ service
│   ├── cmd/
│   │   ├── aegis/          # the `aegis` panel binary (server + admin subcommands)
│   │   ├── aegis-agent/    # the per-node Go agent (writes sing-box config + reloads)
│   │   ├── aegis-pg-backup/    # operator-side backup CLI
│   │   └── aegis-pg-restore/   # operator-side restore CLI (separate binary, safety boundary)
│   ├── internal/           # 21 active packages: app, audits, auth, backups, bootstrap, config, cores, credentials, crypto, db, hosts, inbounds, migrations, nodes, obs, panelcfg, plans, ratelimit, router, subscription, users, webhooks (+ 9 doc.go-only placeholders: cabinet, caddy, cascades, decoy, events, mcp, notifications, stats, subscriptions)
│   ├── migrations/         # 20 SQL files (0001..0020; 0020 adds nodes.ssh_private_key_ciphertext)
│   └── testutil/           # shared Postgres test fixtures
├── frontend/               # Vue 3 + TS admin UI (shadcn-vue)
│   ├── src/components/ui/  # 25 base shadcn-vue components
│   ├── src/components/     # Form / DataTable / FormField (typed wrapper around vee-validate + zod)
│   ├── src/api/services/   # typed API clients (auth / backups / nodes / inbounds / hosts / users / plans / panelcfg / subscription / audits / webhooks / credentials)
│   ├── src/schemas/        # zod schemas
│   ├── src/views/          # Dashboard / Nodes / Inbounds / Hosts / Plans / Subscription / Users / Webhooks / Credentials / Backups / Settings / Audits / Profile / Login
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
│   ├── api/                # API reference (rendered from openapi.yaml, currently 0.8.1)
│   ├── archive/            # superseded docs (e.g. ARCHITECTURE_ADDENDUM_1)
│   ├── comparison/         # honest architecture comparisons (e.g. remnawave.md)
│   ├── developer/          # developer guide (module overview, testing, contributing)
│   ├── guide/              # rendered ARCHITECTURE.md + quickstart + getting-started
│   ├── internal/           # internal architecture deep-dives
│   ├── user-guide/         # operator-facing admin user guide
│   ├── operator-guide.md   # the canonical "fresh VPS → panel" reference (v0.8.x)
│   ├── SECURITY.md         # threat model + disclosure flow (v0.8.9)
│   ├── ROADMAP.md          # the milestone ladder (v0.8.9)
│   ├── README.md           # docs index
│   ├── KNOWN_LIMITATIONS.md  # (root) gap list
│   ├── RUNBOOKS/           # operator runbooks (deploy, restore-drill)
│   └── openapi.yaml        # OpenAPI 3.0 spec (codegen source of truth; currently 0.8.1)
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
`package-lock.json`; `pnpm` is no longer used), **Docker 24+** and
**Docker Compose v2**, **Make**.

## What's where

- **Architecture** — [`ARCHITECTURE.md`](./ARCHITECTURE.md) (v9.5).
  Source of truth for the design.
- **Roadmap** — [`docs/ROADMAP.md`](./docs/ROADMAP.md). Milestone ladder
  with per-PR status.
- **Operator guide** — [`docs/operator-guide.md`](./docs/operator-guide.md).
  The end-to-end "from a fresh VPS to a panel that serves real users"
  flow (v0.8.x: decrypt-on-operator + manual docker compose).
- **Quickstart** — [`docs/guide/quickstart.md`](./docs/guide/quickstart.md).
  The five-minute operator path.
- **Security policy** — [`docs/SECURITY.md`](./docs/SECURITY.md).
  Threat model (v0.8.9), supply-chain trust, disclosure flow.
- **API reference** — [`docs/api/`](./docs/api/index.md). Rendered from
  `docs/openapi.yaml` (currently 0.8.1; v0.8.2..v0.8.9 did not
  change the API surface).
- **CHANGELOG** — [`CHANGELOG.md`](./CHANGELOG.md). Per-version release
  notes (Keep a Changelog format).
- **Known limitations** — [`KNOWN_LIMITATIONS.md`](./KNOWN_LIMITATIONS.md).
  Open gaps and the milestone that closes each.
- **Runbooks** — [`docs/RUNBOOKS/`](./docs/RUNBOOKS/deploy.md).
  Operator runbooks (deploy, restore-drill).
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
- **Never publish deploy URLs, server IPs, admin credentials, JWT
  secrets, or SSH key paths** in PRs, issues, or commit messages.
  Operator-only state lives in `~/.aegis/deploy.local.md` (outside
  the repo) — see the privacy note in
  [`deploy/secrets/README.md`](./deploy/secrets/README.md).

## License

AGPL-3.0-or-later. See [LICENSE](./LICENSE).

The Aegis project is single-tenant and AGPL-licensed: any operator
who runs a modified version of the panel is required to publish the
modifications. The operator's modifications and the upstream Aegis
source are both governed by this license.
