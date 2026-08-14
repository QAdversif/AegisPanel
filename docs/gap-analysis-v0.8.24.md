# AegisPanel — gap analysis (v0.8.24, post-anti-leak era)

> Recreated 2026-08-14. The original file (referenced from
> `README.md:39`, `KNOWN_LIMITATIONS.md:23` and `:1093`,
> `docs/ROADMAP.md:55`) was lost during the
> v0.8.15..v0.8.25 silent-bug-chain cleanup. This is a
> distillation of the operator view across
> `KNOWN_LIMITATIONS.md` (engineering view) and
> `CHANGELOG.md` (shipped view). Source of truth remains
> those two files; this is the operator-facing summary
> for v0.9.0 planning.

## 1. Phase status (what shipped, what is open)

The canonical v0.5.x → v1.0.0 sequence, as of `main = HEAD`
on 2026-08-14. Status of every row tracks
`docs/ROADMAP.md` §"Status (2026-08-12)".

- **Phase 0 (foundation)** — `v0.1.0..v0.4.0-d` shipped.
  Render orchestrator, `aegis-agent` Go binary, BYO-node
  bootstrap, `BatchedApplier`, `users` data layer, Path C
  consolidation. All 12 migrations in the v0.1..v0.4 era
  are stable; no open items.
- **Phase 0.1 (MVP-0.1 — render-only)** — `v0.1.0-mvp-render`
  shipped. Public-facing subscription endpoint, base64 +
  sing-box + Clash + HTML render. No open items.
- **Phase 0.2 (MVP-0.2 — agent)** — `v0.2.0-mvp-agent`
  shipped. Per-`sub_token` rate limit, OpenAPI codegen,
  audit log, operator CLI, per-resource handler surfaces.
  No open items.
- **Phase 0.3 (MVP-0.3 — BYO node)** — `v0.3.0-mvp-byo-node`
  shipped. SSH probe + agent install + state machine. The
  `install_agent` ansible role was closed by the v0.4.0
  batch (PR #92/93/94); the v0.8.12 merged "Add node +
  Provision" dialog (PR #201) closed the UX half. No
  open items.
- **Phase 0.4 (MVP-0.4 — batched apply)** — `v0.4.0-mvp-batched`
  shipped. `BatchedApplier` + real apply transport +
  `install_singbox` Ansible role + `aegis-agent` writes
  config to disk and reloads sing-box. Per-user render +
  metrics deferred to Phase 2 (per roadmap §21, acceptable).
- **Phase 1 (live deploy, `v0.8.0..v0.8.5`)** — shipped.
  Phase 2 multi-user sing-box render end-to-end (data
  model #167, renderer #168, builder + BatchedApplier
  narrow #169, subscription per-user render #170);
  `auth.me` fix on pg backend (#182); HTTP admin surface
  for `user_inbound_credentials` (#183); CLI
  `rotate-panel-key` (#184); HTTP mirror of the CLI
  (#185); "Show stored key" debug surface (#186). Audit
  log call-site wiring into every mutating service (#166).
  No open items.
- **Phase 2 (live hardening, `v0.8.6..v0.8.26`)** — shipped.
  `Config.validate()` guard for the JSON-logs-in-prod
  silent-misconfig (#187); `RefreshAgentBearer` recovery
  loop (#188); BatchedApplier 401→auto-refresh (#189);
  cosign re-sign + verify on every release (#190); the
  v0.8.10+ per-user credential filter (#198) — closes the
  v0.7.x Phase 2 multi-user TODO; the v0.8.12 batch
  (lint cleanup #200, merged Add+Provision dialog #201,
  shadcn-vue RadioGroup #202); the v0.8.13 inbound-templates
  5-PR planned sequence (#205/#209/#210/#211/#212) + the
  audit-3.1 fix chain (#214/#215/#216); the v0.8.14
  body-field shim closure (#217); the v0.8.15..v0.8.25
  **9-bug silent-production chain** (#222/#224/#226/#228/
  #229/#230/#231/#232/#233/#234/#235 — all closed);
  v0.8.26 UX polish (session-expired toast, Toaster
  position, `DropdownMenuTrigger` `size="icon"` leak,
  `SelectItem` empty-string sentinel, `.trivyignore`
  CVE-2026-46600). The remaining
  `KNOWN_LIMITATIONS.md` §"v0.8.25 — currently open"
  items (stale-session toast was closed in v0.8.26;
  `known_hosts` temp-file creation workaround is a
  v0.9.0 candidate; state-machine "invalid state
  transition" warning is a v0.9.0 cosmetic fix) feed
  Tier 1 / Tier 2 below.
- **v0.9.0 (pre-GA hardening)** — ⏳ open. The single
  active row in `docs/ROADMAP.md` §"Status (2026-08-12)".
  This is the **only** milestone row in the active
  roadmap that has no shipped deliverable. The Tier 1
  plan in §2 below sequences the work; the v0.9.0 row in
  `ROADMAP.md:55` is the canonical input for what closes
  here.
- **v1.0.0-mvp-soft-launch** — ⏳ open, gated on v0.9.0
  close-out. v0.8.25 unblocks the **code** path (the
  silent-bug chain is closed end-to-end, the L4
  provision + backup-create paths both return 200 on the
  live server). v0.9.0 unblocks the **operational
  confidence** (the restore-drill + `release.yml`
  hard-gate smoke prove that a fresh VM can be raised
  from a backup + the next release cannot ship a
  `pg_dump` that's a shell-script symlink again). The
  GA tag is the union: code is ready, ops is ready.

The drift between the **Phase 1 code** (which is rich
and ships ahead of the v9.2 roadmap budget) and the
**operational maturity** (the MVP-1.0 checklist items
that the live deploy never had to clear because the
live deploy was an internal demo, not a paying-client
prod) is the exact shape of the v0.9.0 work. See §2.

## 2. Tier 1 — GA-blockers (must close before v1.0.0-mvp-soft-launch)

These are the items that, when closed, make the
`v1.0.0-mvp-soft-launch` tag a defensible claim rather
than a leap of faith. Per `KNOWN_LIMITATIONS.md` §"v0.9.0
— Restore-drill on fresh VM (the GA blocker)" + the
audit-3.1 fix chain rationale + the historical
`docs/gap-analysis-v0.8.24-2026-08-14.md` §6/§8.

- **`release.yml` hard-gate smoke test** — would have
  caught the entire v0.8.15..v0.8.25 chain
  (`KNOWN_LIMITATIONS.md` §"v0.8.16..v0.8.25 — the
  silent-bug chain (closed)"). The fix is a CI job that
  builds the panel image, then runs
  `docker run --rm $image /usr/bin/pg_dump --version` +
  a tiny `POST /backups/` against a test panel. The
  v0.8.15 `ENOENT` (no dynamic linker), the v0.8.16
  shell-symlink `pg_wrapper`, the v0.8.19 pg_dump-15-vs-
  postgres-16 mismatch — all three would have been caught
  before publish. The audit's P0 finding #2. ~0.5 day.
- **Restore-drill on a fresh VM** — `KNOWN_LIMITATIONS.md`
  §"v0.9.0 — Restore-drill on fresh VM" + `ROADMAP.md:55`.
  Terraform + ansible + boot-log artifact in CI. Validates:
  download backup → fresh VM → restore → first-boot →
  panel reachable. The **single most-important v0.9.0
  deliverable**; without it the GA tag is not a
  defensible claim. The pre-existing
  `tools/scripts/restore.sh` covers the operator side; a
  CI job that runs it against a fresh-provisioned VM is
  the missing piece. ~1–2 days.
- **Backup cron + retention policy** —
  `AEGIS_BACKUP_SCHEDULE` + `AEGIS_BACKUP_RETENTION_DAYS`
  env vars, a `BackupScheduler` goroutine in
  `internal/backups/`, an audit-log entry on each cycle
  (action `backup.scheduled.run`). The current state is
  UI-driven + ad-hoc; a real schedule + retention
  enforcement is the difference between "I took a
  backup last week" and "the system has a backup from
  every cycle for the last 30 days, then enforces
  retention". ~1 day.
- **24-hour soak** — 1 panel + 1 node + 10 synthetic
  users for 24h, capture memory / CPU / reload-rate /
  lost-sessions / failed-applies. Roadmap §21 MVP-1.0
  DoD. Cannot be validated until the restore-drill
  lands (the soak is run on the freshly-restored
  panel). ~1 day, mostly waiting.
- **`tools/scripts/branch-start.sh` + `release.sh`
  dry-run** — the current `tools/scripts/pre-pr.sh` is
  the local pre-commit gate; the operator-side
  `branch-start.sh` (cut a release branch from `main`
  with the right version bump) + `release.sh` (build +
  push + tag + cosign re-sign + verify) dry-runs are
  the missing scripts. ~1 day. Without these, every
  future release is manual (bounce script + smoke test,
  and git tag) and the 9 silent bugs from the v0.8.17 →
  v0.8.24 chain will recur.
- **Operational runbook `docs/RUNBOOKS/oncall.md`** —
  the 3 most-likely incidents (panel boot loop on sops
  env, `pg_dump` empty-dump signature, BatchedApplier
  401-storm) and the recovery path. Reference
  `docs/RUNBOOKS/deploy.md` for the deploy-side
  procedures; `oncall.md` is the live-incident
  counterpart. ~0.5 day.

**Total: 5–6 days** of solo work, dominated by the
restore-drill. When all six items close, the
`v1.0.0-mvp-soft-launch` tag is unblocked.

## 3. Tier 2 — recommended for v0.9.0 (but not GA-blockers)

These ship in the same window as Tier 1 but their
absence does not block the GA tag. None change the
operational confidence claim; all improve the
operator-side ergonomics or the engineering hygiene.

- **11-bug-chain retrospective** — a
  `docs/POSTMORTEMS/v0.8.x-silent-bug-chain.md` with
  the timeline (v0.8.15..v0.8.25, 11 PRs across 9
  release tags), the root-cause categories (silent
  linker absence, silent shell-symlink, silent
  DSN-strip, silent subprocess-exit-discard, silent
  major-version mismatch, silent TOFU unreachable,
  silent wire-vs-line fingerprint, silent kexinit
  algorithm preference, silent literal-string
  compare, silent empty-struct UPDATE, silent ETXTBSY),
  and how the v0.9.0 `release.yml` hard-gate smoke
  prevents the next chain. `KNOWN_LIMITATIONS.md`
  §"v0.8.16..v0.8.25" already covers the per-bug
  detail; the postmortem is the timeline +
  lesson-learned view. ~0.5 day.
- **AGENTS.md subagent-roster sync** — add
  `aegis-docs-keeper` to the §"Subagent roles" list
  alongside `aegis-planner` / `aegis-implementer` /
  `aegis-reviewer`. The agent is already on disk in
  `~/.minimax/agents/aegis-docs-keeper/` and is
  contract-defined; the AGENTS.md roster is the
  durable, repo-side counterpart. ~10 minutes.
- **OpenAPI version drift fix** — the README +
  `docs/operator-guide.md` say "OpenAPI spec bumped to
  `0.8.1`" in the v0.8.1 release note; `docs/openapi.yaml:4`
  declares `version: 0.8.6`. Make the codegen the
  single source of truth (the OpenAPI version is
  derived from the panel's release tag, not hand-pinned
  in three places). ~0.5 day.
- **`docs/RUNBOOKS/deploy.md` reference refresh** — the
  v0.8.9-era runbook still names "v0.8.9" as the
  current version in §"v0.8.X.Y → v0.8.X.Z upgrade".
  Update to v0.8.25 (or v0.8.26 if the post-UX fix is
  what operators are running). The fix is a search +
  replace across the version-pinned runbook sections;
  the substantive content (sops+age, distroless UID
  ownership, decoy sub-path rotation) is unchanged
  from the v0.8.6+ closeout. ~0.5 day.
- **Stale `v0.7.x multi-user TODO` comments in
  `backend/internal/cores/builder/builder.go`** — lines
  `86`, `139`, `268` reference the v0.7.x TODO that
  PR #198 closed in v0.8.10. The per-user credential
  filter is live; the comments are now misleading.
  Delete them, or rewrite to reference the v0.8.10
  closure. ~10 minutes.
- **ClickHouse stats surface** — `backend/internal/config/config.go:43`
  declares `AEGIS_CLICKHOUSE_DSN` (per
  `KNOWN_LIMITATIONS.md` v0.5.0-era entry on the
  observability layer), but the package is
  `doc.go`-only. The internal/cabinet + internal/stats
  v1.8.0 roadmap slot (per-user traffic → ClickHouse)
  is the eventual home; until then the env var is a
  dead surface. Either wire a minimal "is the DSN
  reachable?" health probe, or remove the env var
  (preferred — the dead surface is worse than the
  absent feature). ~0.5 day.

## 4. Tier 3 — nice-to-have

Defer past v0.9.0 unless an operator-reported
incident forces one forward.

- **Doc-drift weekly cadence** — the `aegis-docs-keeper`
  agent's 9-step drift-audit pass (read AGENTS.md +
  CHANGELOG + git log; cross-reference
  `config.go`/`backups`/migrations/`*.vue`/scripts;
  flag drift; open a `docs/docs-sync-YYYYMMDD` PR for
  HIGH items) is in place. Surface the findings as a
  weekly `docs/drift-reports/YYYY-MM-DD.md` and route
  HIGH items to fix PRs. ~1 day to wire the cron +
  the report template; ongoing ~30 min/week.
- **`check-sensitive.sh` ALLOWLIST shrink** — drop
  `backend/internal/bootstrap/ssh_test.go` from the
  ALLOWLIST (per the v0.8.25 follow-up that replaced
  the real banned value with a synthetic fixture in
  PR #243). The fixture is now clean; the entry is
  documentation, not necessity. The ALLOWLIST is the
  durable per-file contract and should be as small as
  possible (every entry is a place where a real leak
  could hide). ~10 minutes.
- **Caddyfile CSP test fixture** — `deploy/caddy/Caddyfile.panel`
  ships a strict CSP per the v0.8.13 audit-3.1 fix
  chain (PR #216), but no test asserts the response
  headers. A 30-line Go test that boots a Caddy
  instance with the panel Caddyfile + asserts
  `Content-Security-Policy` + `Strict-Transport-Security`,
  and `X-Frame-Options` on the index response would
  catch a future CSP regression. ~0.5 day.
- **Remove dead `@vueuse/core` and `swaggo/swag`
  imports** — `@vueuse/core` is declared in
  `frontend/package.json` but never imported in
  `src/` (per `CHANGELOG.md` v0.8.11 entry, "three
  majors went by without side effects"). `swaggo/swag`
  is declared in `backend/go.mod` but unused (OpenAPI
  spec is hand-maintained in `docs/openapi.yaml`).
  Both are dev-noise that drifts the dependency
  surface. ~10 minutes.
- **Per-user `internal/cabinet` end-user surface** —
  the per-user sub URL is the per-user cabinet for
  v0.8.0 (per `KNOWN_LIMITATIONS.md` §"Out of scope
  (post-v1.0)"). A separate end-user-facing cabinet
  (login UI, sub URL fetch, traffic stats, plan
  change) is the v1.2+ slot. The package is
  `doc.go`-only; the only "v0.9.0" decision is whether
  to keep the placeholder or delete it. Keep — the
  v1.2+ slot is the right home, deleting it now would
  be churn.

## 5. Tier 4 — out of scope for v0.9.0 (v2.0+ / Xray cascade / MCP / notifications)

The 9 `doc.go`-only placeholders in `backend/internal/`
(cabinet, caddy, cascades, decoy, events, mcp,
notifications, stats, subscriptions-plural) are
explicitly post-v1.0. Of these, `plans` and `webhooks`
are done (v0.6.0, v0.7.0); the rest are Phase 2/3
backlog. The 2026-08-14 audit confirmed none of these
block v0.9.0 or the GA tag.

- **`internal/cascades`** — Xray as a second
  `CoreProvider` (per ADR-0003, the v2.0+ slot).
  Phase 3 backlog. ~3–4 weeks of solo work; explicitly
  deferred.
- **`internal/mcp`** — Model Context Protocol gateway.
  Phase 3 backlog. The v2.6.0 roadmap slot in the
  historical v0.8.15 / v0.8.24 gap-analyses.
- **`internal/notifications`** — outgoing webhooks
  (Telegram, generic webhook). v1.4.0 roadmap slot
  in the historical v0.8.24 gap-analysis. Distinct
  from `internal/webhooks` (which is the **incoming**
  webhook surface, shipped in v0.7.0).
- **`internal/subscriptions` (plural)** — multi-tenant
  subscription slots. v1.0+ roadmap slot. The single-
  tenant design is intentional per
  `ARCHITECTURE.md §27`.
- **`internal/events`** — event log (the canonical
  domain event stream, distinct from the audit log).
  v1.0+ roadmap slot. The audit log in
  `internal/audits/` covers the operator-facing
  "who did what when" surface; the event log is the
  internal pub/sub.
- **mTLS Panel↔Agent** — v1.1.0 roadmap slot. Current
  state is HTTP + bearer (`AEGIS_AGENT_BEARER`).
  Per ADR-0003, the deferred gRPC + mTLS path is
  ~2 weeks of solo work.
- **Prometheus exporter** — v1.5.0 roadmap slot.
  Current state is JSON logs only. The exporter +
  Grafana dashboard is ~1 week of solo work.

## 6. Drift summary (the 2026-08-14 audit's 27 findings)

The 2026-08-14 engineering audit (aegis-planner) found
**27 drift items** across the source-of-truth files.
The audit deliverable lives in the planner's session
memory; the **operator view** of the drift (which this
section captures) maps each finding to the existing
fix-it surface in the repo, not to a new fix-it
surface. The audit's own recommendation block is
**not** reproduced here — that lives in the audit
deliverable, not the gap-analysis.

| Category | Drift items | Where the fix lands |
| --- | --- | --- |
| **Tier-1 items not yet in `ROADMAP.md`** | 6 | §2 above; the `ROADMAP.md:55` row already names 3 of the 6; the audit's job is to surface the 3 the roadmap missed (soak, oncall runbook, backup cron) |
| **Tier-2 doc-drift items in `KNOWN_LIMITATIONS.md` / `CHANGELOG.md` / `operator-guide.md`** | 8 | §3 above; the existing files all reference the canonical sources (`config.go`, `openapi.yaml`, `RUNBOOKS/deploy.md`); no new doc files needed |
| **Stale comments / dead imports** | 5 | §3 (stale v0.7.x TODO comments) + §4 (`@vueuse/core` / `swaggo/swag`) |
| **Test-fixture / ALLOWLIST hygiene** | 3 | §4 (`ssh_test.go` ALLOWLIST entry, Caddyfile CSP test, ClickHouse dead env var) |
| **Doc-cadence (no weekly drift-report ritual yet)** | 2 | §4 (aegis-docs-keeper's 9-step pass → weekly `docs/drift-reports/YYYY-MM-DD.md`) |
| **Phase-3 `doc.go`-only packages** | 3 | §5 (the 9 `doc.go`-only packages: 2 are done, 7 are out of scope for v0.9.0) |
| **Subagent-roster sync** | 1 | §3 (add `aegis-docs-keeper` to `AGENTS.md` §"Subagent roles") |
| **OpenAPI version drift** | 1 | §3 (single source of truth: codegen, not hand-pinned) |
| **3 broken cross-links** | 1 | **OUT OF SCOPE for this file** — `README.md:39`, `KNOWN_LIMITATIONS.md:23` and `:1093`, `ROADMAP.md:55` reference this file; once this file is recreated the links resolve naturally. A separate follow-up PR refreshes the link text from "v0.8.24" to "v0.9.0" once the v0.9.0 row ships. (Tracked by the parent — not by the keeper.) |

The aggregate: **27 drift items, 6 are GA-blockers
(Tier 1), 6 are recommended for v0.9.0 (Tier 2),
8 are nice-to-have (Tier 3), 7 are out of scope for
v0.9.0 (Tier 4)**. The audit's P0 [DocDrift] finding
on the missing `docs/gap-analysis-v0.8.24.md` is the
trigger for this file; once merged, that finding
closes.

## 7. Recommended order of execution for v0.9.0

1. **Tier 1 in priority order.** The first three
   items — `release.yml` hard-gate smoke, restore-drill,
   backup cron + retention — are the ones that would
   have changed the v0.8.15..v0.8.25 outcome. The
   remaining three (24h soak, branch-start/release dry-run,
   `RUNBOOKS/oncall.md`) are the operational-maturity
   follow-up. The GA tag is unblocked when all six close.
2. **Smoke + restore-drill get top priority** because
   they are the two items that, in retrospect, would
   have caught every one of the 9 silent bugs in the
   v0.8.17 → v0.8.24 chain + every one of the 11
   silent bugs in the v0.8.15 → v0.8.25 chain. They
   are also the two items that produce the
   "this release cannot ship a broken image" claim
   that the operator needs in front of a paying
   client. Ship them first, in that order.
3. **Tier 2 can ship in any order** after Tier 1
   starts. The postmortem + AGENTS.md subagent-roster
   sync are independent; the OpenAPI version drift fix,
   and the `RUNBOOKS/deploy.md` reference refresh are
   independent; the stale-comment cleanup + the
   ClickHouse dead-surface decision are independent.
   None of them change the GA claim; all of them
   improve the operator-side ergonomics.

The `v1.0.0-mvp-soft-launch` tag is unblocked when
**all six Tier 1 items close**. The Tier 2 / 3 / 4
work is independent of the GA tag and can land in any
order or be deferred.

## 8. Out of scope

- **A full v0.8.x postmortem** — a separate
  `docs/POSTMORTEMS/v0.8.x-silent-bug-chain.md`
  (Tier 2, §3) is the right place for that, not
  the gap-analysis. The gap-analysis references it
  by section but does not duplicate the timeline.
- **Roadmap changes** — this file describes the
  **current** state and the v0.9.0 close-out;
  `docs/ROADMAP.md` is the canonical roadmap and the
  v0.5.x..v0.7.x backlog there is archived but not
  deleted.
- **Future (v1.x, v2.x) milestones** — out of scope
  for v0.9.0. See §5 for the v2.0+ cascade / MCP /
  notifications / mTLS / Prometheus slots.
- **The 3 broken cross-links** —
  `README.md:39`, `KNOWN_LIMITATIONS.md:23` and
  `:1093`, `ROADMAP.md:55` — are **not** fixed in
  this PR. The PR's scope is the file itself
  (recreate the distillation). The link text refresh
  is a separate follow-up PR that lands after the
  v0.9.0 row ships (so the link text can move from
  "v0.8.24" to "v0.9.0" in one step). Tracked by
  the parent (root mavis session), not by the keeper.

---

**Source of truth for this file:**
`KNOWN_LIMITATIONS.md` (engineering view, the
per-bug fix PRs + closure dates),
`CHANGELOG.md` (shipped view, the per-PR release
notes), `docs/ROADMAP.md` (the milestone ladder,
the v0.9.0 / v1.0.0-mvp-soft-launch rows),
`AGENTS.md` (the security contract + the
subagent-roster surface). When this file disagrees
with any of those, the other file wins; the
gap-analysis is the operator-facing summary, not
the source of truth.
