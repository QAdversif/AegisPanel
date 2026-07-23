# Changelog

All notable changes to Aegis are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added (v0.3.0-mvp-byo-node, in progress)

- **`internal/bootstrap/`** package — SSH client (`x/crypto/ssh` +
  `pkg/sftp`), TOFU host-key policy, 32-byte bearer secret
  generation, 5-step install workflow, state machine, provisioner.
  Closes v0.3.0-a (backend). Closes #67.
- 11 reserved-package `doc.go` stubs for the Phase 2-4 slots
  (`cabinet`, `caddy`, `cascades`, `decoy`, `events`, `mcp`,
  `notifications`, `plans`, `stats`, `subscriptions`,
  `webhooks`). Closes #77.

### Fixed (cleanup batch, post-v0.3.0-a)

- **chi v5.2.4 → v5.3.1.** Replaced the deprecated
  `middleware.RealIP` (vulnerable to XFF spoofing, GHSA-3fxj-6jh8-hvhx
  family) with the chi v5.3 `ClientIPFrom*` + `GetClientIP` family.
  Closes #75.
- **`internal/audits/clientIP` re-pointed to `middleware.GetClientIP`.**
  No more local XFF parsing in the audit handler — single source
  of truth in the chi middleware. Same fixup as #75.
- **Trivy workflow: `ignorefile:` → `trivyignores:`** (the
  trivy-action input key, not the silent reject that was
  hiding the `.trivyignore` entries). Closes #74.
- **Frontend `eslint --fix`** across the six view files —
  171 auto-fixable warnings → 0. Closes #76.
- **Dependabot #68 (Go minor+patch)** superseded by #75; #69
  (frontend minor+patch) deferred to v0.4.0 cleanup window
  (transitively requires a TypeScript 5.8+ major).

### Documentation (v9.2 roadmap sync, #78)

- **ARCHITECTURE.md §21** markers synced with the code: v0.1.0
  and v0.2.0 marked `[done]`, v0.3.0 marked `[wip]`. §21
  timing table updated. New §25 entry v9.2 documenting the
  sync + the cleanup batch. See PR #78.
- **Tags created retroactively** (and pushed):
  - `v0.1.0-mvp-render` on `5840c13` (PR #50, last v0.1.0 commit).
  - `v0.2.0-mvp-agent` on `c2e773c` (PR #63, last v0.2.0 commit).
- **KNOWN_LIMITATIONS.md** restructured: previously-v0.1.0
  entries that closed in v0.2.0 moved to a "Closed" section;
  v0.3.0+ open items live under the v0.3.0 heading.
- **README.md** status table, Go version, repo layout, and
  frontend view list all updated to v0.3.0-era.

## [0.2.0-mvp-agent] - 2026-07-19

**Tag:** `v0.2.0-mvp-agent` on `c2e773c` (PR #63, last v0.2.0
commit). v0.2.0 delivers the `cmd/aegis-agent` placeholder
binary, all backend handler surfaces for the v0.1.0 UI, and
the OpenAPI codegen pipeline.

### Added (v0.2.0)

- **Backend handler surfaces** for the v0.1.0 admin UI:
  - `/api/v1/panelcfg` (PR-F, #59) — sub-path rotation.
  - `/api/v1/users` (PR-G, #60) — admin user CRUD.
  - `/api/v1/hosts` (PR-H, #61) — host create/edit dialogs.
  - `/api/v1/nodes/{id}/inbounds` (PR-I, #62) — per-node
    inbounds CRUD with JSONB `params` editor.
  - `/api/v1/audits` + `/api/v1/auth/me/password` (PR-M, #66)
    — audit log read surface + operator change-password.
- **Argon2id operator CLI** (PR-J, #63) — `aegis admin add
  <user>`, `aegis admin passwd <user>`, `aegis admin list`.
  Production seed guard: `AEGIS_ENV=production` refuses to
  start with the dev seed user.
- **Per-sub_token rate limiting** (PR-K, #64) — in-memory
  token bucket with `Retry-After` header.
- **OpenAPI 3.0 codegen** (PR-L, #65) — `pnpm run codegen`
  regenerates `frontend/src/types/api.d.ts`;
  `pnpm run codegen:check` enforces byte-equality in CI.
- **Sub-token rotation + URL prefix rotation** (#47) —
  Panel-side helpers that let the operator rotate a user's
  sub-token or the panel-wide sub-path without code changes.
- **Placeholder `cmd/aegis-agent`** — `sleep infinity`
  systemd unit so the Apply path can be smoke-tested
  end-to-end without a real agent binary. Real Go binary
  ships in v0.3.0-c.

### Fixed

- **i18n coverage gap** between RU/EN locales (PR-E, #58).
- **KNOWN_LIMITATIONS.md** v0.1.0 gap list (PR-E, #58) —
  the per-scope list of what was open at v0.1.0 cut.
- **postcss 8.4 → 8.5** for GHSA-qx2v-qp2m-jg93 (#57).
- **`.gitattributes` LF policy** for Windows contributors
  (#56) — eliminates CRLF noise in CI.
- **go-chi 5.0 → 5.2.4** (#13) — security baseline.

## [0.1.0-mvp-render] - 2026-07-17

**Tag:** `v0.1.0-mvp-render` on `5840c13` (PR #50, last
v0.1.0 commit). v0.1.0 ships the renderable MVP: every
surface except the actual `Apply` call works through the
API + UI. The Apply call is a stub returning
`ErrApplyNotImplemented` — that is **OK for v0.1.0** per
the DoD in `ARCHITECTURE.md §21 / MVP-0.1`.

### Added (v0.1.0)

- **Subscription `PgStore`** (#50) — `internal/subscription/store_pg.go`
  and migration. Subscription URL endpoint works end-to-end
  against Postgres (MemoryStore still available for dev).
- **Panelcfg `PgStore`** (#50) — same package split; sub-path
  config persists in `panel_path_config` table.
- **Frontend stack** (ADR-0004, PR-B, #51) — TailwindCSS,
  shadcn-vue, Reka UI, `@tanstack/vue-table` (DataTable),
  `vee-validate`, `zod` (forms), `lucide-vue-next`.
- **DataTable + form primitives** (PR-C, #54) —
  `frontend/src/components/{Form,FormField,FormFieldError,DataTable}.vue`
  and `frontend/src/composables/useZodForm.ts` typed wrapper.
- **CRUD pages + auth flow** (PR-D, #55) — Dashboard, Nodes,
  Inbounds, Hosts, Subscription, Users, Settings, Login views
  with full create/edit/delete flows.
- **Smoke test** (`tools/scripts/smoke-frontend.sh`,
  PR-E, #58) — runs `vite preview` and validates the
  served HTML + asset graph.

### Architecture (v9 + v9.1, prereq to v0.1.0)

- **ADR-0003** (`docs/adr/0003-mvp-singbox-vertical-slice.md`)
  — sing-box is the only MVP core. Xray deferred to v2.0+.
  Batched Apply is the primary user-enforcement strategy.
- **ADR-0004** (`docs/adr/0004-frontend-ui-kit-shadcn-vue.md`)
  — shadcn-vue + Reka UI stack fix. Alternatives (NaiveUI,
  PrimeVue, Element Plus, Vuetify) considered and rejected
  with rationale.
- **ADR-0001** (`docs/adr/0001-xray-as-production-core.md`)
  marked **Superseded by ADR-0003**. Kept in-tree for history.
- **ARCHITECTURE.md v9** (`#49`) — full rewrite after the
  ADR-0001 cancellation. §21 unified roadmap is the single
  source of truth for phases. v8 (Phase 4 split roadmap +
  addendum) folded in.
- **ARCHITECTURE.md v9.1** (`#48` followup) — UI stack fix
  in §1 + §21 Phase 1 / MVP-0.1.

### Known gaps (closed in v0.2.0)

These are documented in detail in `KNOWN_LIMITATIONS.md` under
the "Closed in v0.2.0" section. Top items:

- Per-node inbounds editor (closed by PR-I, #62).
- Host create / edit dialogs (closed by PR-H, #61).
- User CRUD (closed by PR-G, #60).
- Settings UI / panelcfg HTTP (closed by PR-F, #59).
- OpenAPI codegen (closed by PR-L, #65).
- Per-sub_token rate limiting (closed by PR-K, #64).
- Argon2id operator CLI (closed by PR-J, #63).

## [0.0.1] - 2026-07-13

Pre-alpha skeleton. Architecture v7 is finalised; the code tree is in
place. Nothing is wired up to run end-to-end yet; that is Phase 0 →
Phase 1.

### Added (skeleton)
- Repository skeleton (monorepo: `backend/`, `frontend/`, `docs/`, `deploy/`).
- Backend: Go 1.22+ service skeleton (`chi`, env config, structured
  logging, healthcheck, metrics stub, initial SQL migration).
- Frontend: Vue 3 + TS + Vite admin UI skeleton (Pinia, vue-i18n
  ru/en, dashboard view).
- Docs: VuePress 2 site (local-only, not published yet).
- Dev environment: Docker Compose stack (PostgreSQL 16, Redis 7,
  NATS 2.10, ClickHouse 24, MinIO, Caddy 2).
- Deploy: Ansible roles, Caddyfile templates for panel and node
  (with decoy + masquerade ports), fail2ban jails, systemd units.
- GitHub: workflows (ci, release), dependabot, issue / PR templates,
  community health files (CONTRIBUTING, CODE_OF_CONDUCT, SECURITY).
- Tooling: `tools/scripts/{release,restore,backup,branch-start}.sh`.
- Conventional Commits template (`.gitmessage.txt`).
- Architecture document (`ARCHITECTURE.md`, 28 sections).
