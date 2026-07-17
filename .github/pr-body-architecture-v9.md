# docs(architecture): v9 — sing-box-only MVP pivot + UI stack fix

## Summary

Strategic pivot from the v8 roadmap to a vertical-slice MVP plan.
ADR-0001 (Xray as production core) is **superseded by ADR-0003** one
day after acceptance — see ADR-0003 §"Open questions" for the
post-mortem on that.

**Bottom line:** MVP v1.0 ships on **sing-box 1.8+ as the only core**,
with **Batched Apply as the primary** user-lifecycle strategy.
Xray becomes a second provider in **v2.0.0+** through the existing
CoreProvider abstraction. The UI stack is also fixed: **shadcn-vue +
Reka UI + TailwindCSS**.

## Changes

### ADR-0003 (NEW) — Sing-box only MVP, Batched Apply as primary

- **MVP v1.0 ships on sing-box as the only core.** ADR-0001 reversed.
- `internal/cores/singbox/` already implements render/diff/validate/
  capabilities. `cores/xray/` is empty. Building Xray = 3-4 PR of
  pure plumbing (gRPC client, proto parser, integration tests with
  Xray-in-docker) **before** any user-visible feature.
- **Batched Apply is the primary strategy**, not a fallback.
  Window 20 sec default (`AEGIS_BATCHED_APPLY_WINDOW`). Required
  metrics: `core_reload_total`, `core_reload_lost_sessions_total`,
  `core_user_apply_latency_seconds`.
- **CoreProvider abstraction preserved** = Xray added in v2.0+ = no
  DB migration, no front-end rewrite, no breaking change for
  sing-box installs.
- **Vertical-slice roadmap** v0.1.0 → v0.4.0 → v1.0.0 (5-7 weeks solo).
- **Definition of Done** with 10 concrete criteria applied to every
  release in the roadmap (≥70% coverage on new code, integration
  test with real Postgres, E2E with docker-compose, smoke test on
  fresh VM, lint/sqlfluff/markdownlint = 0 errors, etc.).

### ADR-0001 — Superseded banner added

- Status changed from `Accepted` → `Superseded by ADR-0003`.
- Header now contains a deprecation block pointing to ADR-0003.
- Kept in `docs/adr/` for history; **not deleted** so the
  accept → reverse cycle is visible.

### ADR-0004 (NEW) — Frontend stack: shadcn-vue + Reka UI + TailwindCSS

- **shadcn-vue** (copy-paste components, owned by project) +
  **Reka UI** (Radix-Vue renamed) for headless a11y primitives +
  **TailwindCSS** for styling.
- Plus: `@tanstack/vue-table` (DataTable), `vee-validate` + `zod`
  (forms), `lucide-vue-next` (icons), `class-variance-authority`,
  `clsx` + `tailwind-merge`, `cn()` helper.
- **Rejects** NaiveUI / PrimeVue / Element Plus / Vuetify with
  reasoning (vendor lock-in, bundle size, opinionated theming).
- Tailwind v3 vs v4: take v3.4.x on v0.1.0 (stable, shadcn-vue
  integration well-trodden), migrate to v4 = v1.5+ task.
- 6 PR-level implementation plan in v0.1.0 (init → basic components →
  forms/validation → DataTable → pages → i18n).

### ARCHITECTURE.md updates (v8 → v9)

- **§0 (Terms):** Core definition rewritten — "MVP v1.0 — sing-box
  1.8+ (единственный core на релизе) … v2.0+ — Xray добавляется как
  second provider". Cascade marked "v2.1+ (Xray-only)".
- **§1 (Vision):** "Один core provider: sing-box 1.8+" explicit.
  UI-стек конкретизирован.
- **§7 (Core abstraction):** "MVP core: sing-box 1.8+" explicit.
  Removed "Production core: Xray" framing.
- **§7.5 (Batched Apply):** Title changed to "primary-стратегия MVP".
- **§21 (Unified Roadmap):** Completely rewritten with vertical-slice
  phases:
  - Phase 1 (MVP-0.x) → v0.1.0-mvp-render → v0.4.0-mvp-batched
  - Phase 2 (Post-MVP hardening) → v1.1.0 → v1.8.0
  - Phase 3 (Second core + Advanced) → v2.0.0 → v2.8.0
  - Phase 4+ (Backlog)
  - Realistic timing: **5-7 weeks solo** to v1.0.0-mvp-soft-launch
    (vs 25-35 weeks in v8).
- **§25 (History):** v9 entry added.

### CHANGELOG.md

- New `[Unreleased]` section with two sub-entries:
  - **v9, вариант A** (sing-box only MVP)
  - **v9.1, UI-стек** (shadcn-vue decision)

### README.md

- Tagline updated: "sing-box on MVP v1.0, Xray as second provider
  in v2.0+". Cross-references ADR-0003 and §21 v9 entry.

## Why this pivot (1-line summary per ADR-0003)

> "Xray CoreProvider = 3-4 weeks of plumbing work before any
> user-visible feature. Batched Apply covers 100% of MVP scenarios.
> CoreProvider abstraction keeps the door open for v2.0."

## Open questions still pending

- `core_reload_lost_sessions_total` formula (deferred to v0.4.0
  implementation, see PR #48 review).
- HY2 connection migration under sing-box reload (load-test plan
  described in PR review).
- TreeSelect / Cascader for deep host hierarchies (deferred to v1.x).

## What comes after this PR

Per ADR-0003 / §21 Phase 1:
- **v0.1.0-mvp-render** — SubscriptionPgStore + PanelcfgPgStore +
  UI pages (Nodes/Inbounds/Hosts/Users/Subscription) +
  shadcn-vue init. ~1 week.

## Files changed

- `ARCHITECTURE.md` (+229 / -140)
- `CHANGELOG.md` (+47)
- `README.md` (+9 / -1)
- `docs/adr/0001-xray-as-production-core.md` (+17 / -0) —
  superseded banner
- `docs/adr/0003-mvp-singbox-vertical-slice.md` (NEW, +336)
- `docs/adr/0004-frontend-ui-kit-shadcn-vue.md` (NEW, +242)

## Checklist

- [x] ADR-0001 marked Superseded (history visible, not deleted)
- [x] ADR-0003 + ADR-0004 created with full context, alternatives,
      consequences
- [x] ARCHITECTURE.md §0, §1, §7, §7.5, §21, §25 updated consistently
- [x] CHANGELOG.md Unreleased section added (Keep a Changelog format)
- [x] README.md updated to reflect new strategy
- [x] All cross-references normalized (Xray = v2.0+, Cascade = v2.1+)
- [x] No code changes (doc-only PR)

## References

- [ADR-0003](docs/adr/0003-mvp-singbox-vertical-slice.md) — the new
  MVP plan in full
- [ADR-0004](docs/adr/0004-frontend-ui-kit-shadcn-vue.md) — UI stack
- [ADR-0002](docs/adr/0002-node-profile-separation.md) — still in force
  (Node Profile separation: reality-direct vs caddy-fronted)
- [ADR-0001](docs/adr/0001-xray-as-production-core.md) — superseded
- ARCHITECTURE.md §21 (Unified Roadmap v9) + §25 (v9 entry)
- PR #48 review (v8 — review-driven fixes that established
  the architecture baseline this v9 refines)
