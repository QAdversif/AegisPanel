# chore(release): cut v0.8.11 — CHANGELOG surgery, closes 3-PR gap (#196, #197, #198)

## Что внутри

CHANGELOG-only. По образцу PR #195 (v0.8.10 cut).

1. `## [Unreleased]` → пустой
2. Контент `[Unreleased]` (PR #198: per-user credential filter) → `## [0.8.11] - 2026-08-09`
3. Добавлены секции для PR #196 (frontend-deps) и PR #197 (Tailwind v4) — они попадают в v0.8.11 вместе с #198

## Семантика v0.8.11

**Consolidation release** (как v0.8.10). Содержит:
- **1 security gap closure**: per-user credential filter (PR #198) — единственный оставшийся high-severity security gap из deep-state analysis, **unblocks v1.0.0 GA tag**
- **2 frontend cosmetic batches**: #196 (vueuse+vite+jsdom), #197 (Tailwind v4 migration) — без openapi/env/schema изменений
- **0 backend API changes**, **0 schema migrations**, **0 env var changes**

## Что НЕ в этой PR

- **Live deploy action** (rotate admin password) — это отдельная операция, делается по `~/.aegis/deploy.local.md` workflow
- **v0.8.11 tag** — ставится ПОСЛЕ merge по образцу v0.8.10: cron сам tag'нет main + пустит release workflow + создаст GH release
- **Frontend bundle rebuild** — на on-disk prod ничего не меняется, v0.8.9 deploy уже работает на этом коде

## Privacy

Никаких privacy-rule items. Только CHANGELOG.
