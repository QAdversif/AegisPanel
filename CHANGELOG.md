# Changelog

All notable changes to Aegis are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

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

## [Unreleased]

### Added (v9.1, UI-стек)

- **[ADR-0004](docs/adr/0004-frontend-ui-kit-shadcn-vue.md) зафиксирован.**
  UI-стек Aegis v0.1.0+: **shadcn-vue + Reka UI + TailwindCSS** +
  `@tanstack/vue-table` (DataTable) + `vee-validate` + `zod` (формы) +
  `lucide-vue-next` (иконки). Альтернативы (NaiveUI, PrimeVue, Element Plus,
  Vuetify) рассмотрены и отклонены с обоснованием.
- **ARCHITECTURE.md** обновлён: §1 явно перечисляет UI-стек; §21 Phase 1 /
  MVP-0.1 заменяет «NaiveUI / PrimeVue (выбор — в PR)» на конкретный список
  зависимостей; §25 добавлена v9.1 запись.

### Changed (v9, вариант A — sing-box only MVP)

- **Архитектурный поворот (v9)**: MVP v1.0 ships на sing-box как
  единственном core. Batched Apply — primary-стратегия энфорсмента
  юзеров. Xray перенесён в v2.0+ как second provider через
  CoreProvider абстракцию. См. [ADR-0003](docs/adr/0003-mvp-singbox-vertical-slice.md).
- **[ADR-0001 отменён](docs/adr/0001-xray-as-production-core.md)**
  (Xray as production core). Помечен `Superseded by ADR-0003`.
- **ARCHITECTURE.md** обновлён до v9:
  - §0 (Core, Cascade) — термины приведены в соответствие.
  - §1 (Границы MVP) — явно перечислено что входит / что out of scope.
  - §7 + §7.5 — MVP core = sing-box 1.8+, Batched Apply = primary.
  - §21 (Roadmap) — полностью переписан: MVP-0.1 … MVP-1.0 + Phase 2
    (v1.1.0–v1.8.0) + Phase 3 (v2.0.0–v2.8.0) + Phase 4+ backlog.
    Таймлайн до MVP-1.0: **5–7 недель solo** (vs 25–35 недель в v8).
  - §25 — добавлена v9 запись.

### Planned roadmap (v9)

- `v0.1.0-mvp-render` — Subscription `PgStore` + Panelcfg `PgStore` +
  UI страницы (Nodes/Inbounds/Hosts/Users/Subscription). Apply остаётся
  stub (`ErrApplyNotImplemented`) — **OK для 0.1**.
- `v0.2.0-mvp-agent` — `cmd/aegis-agent` (Go, musl), HTTP-bearer
  транспорт, `cores/singbox.Apply/ParseStatus/ParseStats` реальные,
  Ansible `install_agent` доводится.
- `v0.3.0-mvp-byo-node` — `internal/bootstrap/`, UI «Add node» flow.
- `v0.4.0-mvp-batched` — `internal/cores/batched.go` + метрики.
- **`v1.0.0-mvp-soft-launch`** — polishing, healthchecks, JSON-logs,
  backup-restore, `docs/user-guide/admin/quickstart.md`.
- `v1.1.0` … `v1.8.0` — mTLS, users/plans, webhooks, notifications,
  observability, decoy sites v1.
- **`v2.0.0`** — Xray CoreProvider как second provider (если после
  MVP выстрелит).
- `v2.1.0+` — Cascade Topology, MCP, Subscription Profile, SRH Inspector,
  Response Rules, ACL.

### Added
- Architecture document (`ARCHITECTURE.md`, 28 sections).
- Backend skeleton (`backend/`).
- Frontend skeleton (`frontend/`).
- Docs skeleton (`docs/`).
- Deploy assets (`deploy/`).
- Top-level `Makefile`, `README.md`, `LICENSE` (AGPL-3.0),
  `CHANGELOG.md`, `.gitignore`, `.editorconfig`.
