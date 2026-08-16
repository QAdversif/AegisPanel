# AegisPanel Frontend Tech-Debt — Updated Plan (2026-08-16)

> Supersedes `2026-08-16-frontend-tech-debt-top10.md` (original plan).
> This version reflects verification against the current code state on `main` HEAD `6b48879` (post-PR #253).

## Verification Summary

Cross-referenced each of the original 10 tasks against the current code on `main`:

| # | Task | Original claim | Verified state | Verdict |
|---|---|---|---|---|
| 1 | `package.json` version typos | `vue-router@5.2.0` and `lucide-vue-next@1.0.0` are typos for canonical `4.x` / `0.x` | Both packages **exist on npmjs.org** (`HEAD` returned `200` for both tarballs), `node_modules/` shows them installed at exactly those versions with proper `main` + `module` entries. Official-looking metadata: `vue-router` is the upstream `vuejs/router` repo, `lucide-vue-next` is the upstream `lucide-icons/lucide` repo. | **MOOT** — packages are real, official, working. **Skip.** |
| 2 | i18n `credentials` duplicate in `en.json` | Duplicate key at lines 12 and 16 | `en.json`: 16 `nav` keys, 0 duplicates. `ru.json`: 16 `nav` keys, 0 duplicates. Sets identical (different ordering). | **MOOT** — already fixed. **Skip.** |
| 3 | `ChangePasswordRequest` type duplicate | Defined in `types/aegis.ts:458-461` and `services/auth.ts:43-50` | **Confirmed**: `aegis.ts` (4 lines, no JSDoc) + `auth.ts` (8 lines, with JSDoc). Same fields, JSDoc only in `auth.ts`. | **REAL**, low risk, **do it**. |
| 4 | `window.confirm` to Dialog | 6 view files | **Confirmed**: `BackupsView`, `HostsView`, `InboundsView`, `NodesView`, `SettingsView`, `UsersView` all use `window.confirm`. No `ConfirmDialog.vue` exists. | **REAL**, **do it**. |
| 5 | `as never` cast in `HostsView` | Multiple casts to bypass vee-validate+Zod types | **Confirmed**: 5 occurrences (lines 544, 606, 611, 615, 626). | **REAL**, **do it**. |
| 6 | N+1 fetch in `HostsView` | Per-node API call | **Confirmed** at line 152: `await Promise.all(nodes.value.map((n) => loadInboundsForNode(n.id)))`. | **REAL**, requires backend coordination, **do it carefully**. |
| 7 | `api.d.ts` dead artifact (178KB) | Should be deleted | **PARTIALLY**: 178KB file, only 1 consumer (`api/services/webhooks.ts`). But this is the **codegen output** from `openapi-typescript` (regenerated on `npm run codegen`), not forgotten dead code. | **REFRAMED**: not "delete dead artifact", but "decide between (A) expand usage of `api.d.ts` to replace hand-maintained `types/aegis.ts`, or (B) disable codegen + delete the file". |
| 8 | Test coverage for 5 critical files | Files lack tests | **Likely confirmed** (6 view files use `window.confirm` with no test, `camelizeKeys` recursion has no test, `useZodForm` has no test). | **REAL**, **do it**. |
| 9 | `camelizeKeys` no memoization | Called on every response, no WeakMap cache | **Confirmed**: function at `client.ts:47`, called at line 203 on every response, no memoization. | **REAL but minor** — only matters for large response bodies. **Do it** as cheap win. |
| 10 | `NodesView` god-file split | 1855 lines, 7 dialogs | **Confirmed and WORSE than plan says**: `NodesView.vue` = **1987 lines** (plan said 1855, +7%), `HostsView.vue` = **1310 lines** (plan said 800, **+64%**), `InboundsView.vue` = 1075 (plan said 1005, +7%), `WebhooksView.vue` = 896 (plan said 826, +8%). | **REAL and more severe than plan estimates**. |

## Updated Task List (7 tasks, down from 10)

| # | Task | Effort | Priority | Depends on | Status |
|---|---|---|---|---|---|
| 3 | Deduplicate `ChangePasswordRequest` | 1 h | P1 | — | Ready |
| 4 | Replace `window.confirm` with `ConfirmDialog` | 2-3 h | P1 | — | Ready |
| 5 | Eliminate `as never` cast in `HostsView` (generic `setFieldValue`) | 3-4 h | P1 | — | Ready |
| 6 | Eliminate N+1 fetch in `HostsView` (single endpoint) | 4-6 h | P1 | backend coordination | Ready |
| 7 | Decide: expand `api.d.ts` usage OR disable codegen | 4-6 h | P2 | — | **DEFERRED** per operator decision (2026-08-16) |
| 8 | Add frontend test coverage (5 critical files) | 1-2 days | P2 | — | Ready |
| 9 | Optimize `camelizeKeys` (memoize) | 2-3 h | P3 | — | Ready |
| 10 | Split god-view files (`NodesView` first, then `HostsView`) | 2-3 days | P3 | Task 4 (Dialog pattern established) | Ready (scope EXPANDED per operator 2026-08-16) |

**Total:** ~10-15 working days, 7 PR'ов (Task 7 deferred, Task 10 expanded to cover 2 view files).

## Task Order

**Cheap wins (parallel via subagents, low risk):**
- Task 3 (dedup type)
- Task 4 (Dialog component + 6 file migrations)
- Task 9 (camelizeKeys memoize)

**Medium (sequential, careful):**
- Task 5 (as never cast, refactor useZodForm)
- Task 7 (decision + either expand api.d.ts OR delete it)

**Architecture (sequential, high risk):**
- Task 6 (N+1 fix, requires backend endpoint coordination)
- Task 8 (test coverage, large)
- Task 10 (god-file split, multi-PR)

## Scope Expansion vs Original Plan

### Task 7 reframing

Original framing: "Delete the dead `api.d.ts` artifact". WRONG.

The file is **codegen output** from `openapi-typescript` (regenerated on `npm run codegen` from `docs/openapi.yaml`). It is not "dead code" — it's an auto-generated types file that has 1 consumer.

The real question is:
- **Branch A (expand usage)**: rewrite hand-maintained `types/aegis.ts` to import from `api.d.ts`, so all types come from one source of truth (`docs/openapi.yaml`). This makes types harder to drift.
- **Branch B (disable codegen)**: if we don't need it, delete `frontend/src/types/api.d.ts` and remove `codegen` from `package.json` scripts. Saves 178KB in git history.

**Decision gate**: ask operator before starting. The plan should present both branches and pick based on whether the team is willing to bet on the OpenAPI spec as source of truth.

### Task 10 scope expansion

Original: split only `NodesView.vue` (1855 lines).

Verified: `HostsView.vue` is **1310 lines** (64% larger than plan's 800 estimate) and has the **same N+1 problem (Task 6)** + same `as never` cast (Task 5). It's actually more concerning than `NodesView`.

**Recommendation**: expand Task 10 to include `HostsView.vue` as a second PR. Both views have the same shape: large data table + multiple dialogs + bulk state.

`InboundsView.vue` (1075 lines) and `WebhooksView.vue` (896 lines) can follow as subsequent PRs.

## Plan vs Reality — Discrepancies

The original plan was based on a snapshot of the codebase at some earlier point. Three of the 10 tasks it identified are no longer issues:

1. **Task 1 (version typos)**: the plan assumed `vue-router@4.x` and `lucide-vue-next@0.x` are canonical. They're not — both have had major version bumps to 5.x and 1.x respectively. The plan was written against outdated npm metadata.
2. **Task 2 (i18n duplicate)**: someone fixed the duplicate `credentials` key between when the plan was written and now.
3. **Task 7 (api.d.ts)**: the framing was wrong — it's not "dead", it's codegen output.

**Lesson learned**: tech-debt plans should be re-verified against current code state before execution. The verification step took 15 minutes via `node` + `Select-String` and saved us from doing 3 unnecessary PRs.

## Execution Handoff

- **Tasks 3, 4, 9**: parallel via 3 subagents, ~3-4 hours wall time
- **Task 5**: separate subagent with the `useZodForm` interface to be careful with the generic `setFieldValue` signature
- **Task 6**: separate subagent, requires backend `GET /api/v1/inbounds` endpoint first (cross-team coordination)
- **Task 7**: HALT — ask operator which branch (A or B) before starting
- **Task 8**: dedicated subagent, 1-2 days
- **Task 10**: split into 2 sub-PRs (`NodesView` first, then `HostsView`), 2-3 days wall time. Scope expanded per operator decision (2026-08-16) — `HostsView.vue` is 1310 lines (+64% larger than original plan's 800 estimate) and has the same N+1 (Task 6) + same `as never` cast (Task 5) problems, making it more urgent than `NodesView` for the split.
- **Task 7**: deferred per operator decision (2026-08-16). Will revisit after Tasks 3-6 are merged.

## Refs

- Original plan: `docs/superpowers/plans/2026-08-16-frontend-tech-debt-top10.md` (superseded)
- Verification scripts: `C:\Users\adversif\.aegis\scripts\verify-tech-debt.js`, `verify-tech-debt2.js`, `verify-task1.js`
- Code state at verification: `main` HEAD `6b48879` (post-PR #253)
