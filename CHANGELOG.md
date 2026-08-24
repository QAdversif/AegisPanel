# Changelog

All notable changes to Aegis are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [0.8.28.6] - 2026-08-24

No image change — pure ops + code-quality batch. The `ghcr.io/qadversif/aegispanel:0.8.28` image is the same as v0.8.28; this entry covers a migration applied to the live prod database and a series of code-quality / infra PRs.

### Fixed

- **agent response body leak (issue #289 / C4)** — `nodes/handler_refresh_bearer.go` + `bootstrap/handler.go:postApply` were sending an SSH client response through a connection close without `defer resp.Body.Close()`. The leak manifested as `file descriptor exhaustion` on long-running agent-bearer / provisioner calls — silent for hours, then panel-side goroutines couldn't open new files / sockets. Fix: defer close in both call sites. PR #292 (squash `7fc57c3`).
- **cross-user subscription credential cache leak (issue #289 / C3)** — `subscription/service.go` cached a per-user `map[uuid.UUID]credentials.Credential` on the long-lived `Service` struct, populated by the first `/render` call, then served on every subsequent call across all users. A user who happened to be the first render after restart would silently receive their creds on a different user's next render. Fix: build the per-user `map` locally inside the render path (C3b added a 400-goroutine data-race patch in `precomputeUserCreds` to prove the equivalence before deleting the field). PR #293 (squash `211f126`).
- **Phase 2 dead wiring — `Subs.WithCreds` after `Credentials` is built (issue #289 / C2)** — `app.go` called `Subs.WithCreds(ctx, ...)` BEFORE `Credentials` had been built, so the subscriber always saw `credentials: nil` and silently emitted placeholder credentials. Fix: moved the call to step `14c` (after `Credentials.Build()` at step `13`). PR #294 (squash `a5b47fe`).
- **schema: `host_endpoints.path_check` self-contradiction** — migration `0004_hosts_v3.sql` declared `path TEXT NOT NULL DEFAULT ''` together with `CHECK (path <> '')`; every INSERT without an explicit path produced a row that violated its own DEFAULT. Only surfaced on the first host creation in prod (the v0.8.28.6 smoke). Fix migration `0022_relax_host_endpoints_path.sql` drops the DEFAULT, drops NOT NULL, drops the CHECK. Applied to prod out-of-band (`docker exec aegis-postgres psql`) and recorded in `schema_migrations` so the panel's own migration runner skips it. PR #298 (squash `65af85c`). **Net Go change: zero.** Pure DB-schema relaxation.
- **hand-rolled JSON escapers × 11 handler files (#290 D3)** — `jsonString` / `jsonEscape` in `audits` / `auth` / `bootstrap` / `backups` / `hosts` / `inbounds` / `inboundtemplates` / `nodes` / `panelcfg` / `users` all formatted non-BMP runes (emoji) as `"\u1F680"` 5-hex-digit, which is **not a valid JSON string escape** — strict parsers (`jq`, Rust `serde_json`, Go `encoding/json.Unmarshal` with `DisallowInvalidUTF8`) rejected the whole response, lenient parsers (JavaScript's `JSON.parse`) silently corrupted the character. `bootstrap/handler.go:501-523` was the worst offender — escaped *every* letter and truncated non-BMP runes to their low 16 bits. Fix: introduce `internal/httpjson` package (`WriteJSON` / `WriteError` / `String` on `encoding/json`) and migrate all 11 packages. 23 unit tests cover ASCII, BMP non-ASCII (Cyrillic), non-BMP runes (rocket, grinning face, regional indicators), HTML-unsafe chars, control chars, long messages, Content-Type, and the `{"error": msg}` envelope shape. PR #300 (squash `a86c878`).

### Changed

- **compose install contract (#297)** — `tools/scripts/aegis-stack.yml` (production compose: aegis-panel + aegis-ui, 4 canonical mounts, `0.0.0.0:8081:8080` for UI) + `tools/scripts/install-aegis-stack.sh` (idempotent wrapper) + `docs/operator-install.md` (TL;DR, fresh install, upgrade workflow, privacy rules). `cd /opt/aegis && docker compose up -d` is now the canonical install / upgrade entry point. The 2026-08-24 v0.8.28.6 prod-deploy lessons (502 on `<IP>:8081` because docker-proxy bound only on `<IP>` + missing `/var/lib/aegis/backups` mount) are encoded in the canonical mounts and the publish flags — neither can be re-broken by a future hand-written `docker run`. PR #297 (squash `7b946d4`).
- **typescript: drop duplicate `NodeProvisionResponse` (#290 D2-immediate)** — `frontend/src/types/aegis.ts` declared `NodeProvisionResponse` twice (L127 with `node_id: UUID`, L546 with `node_id: string` + JSDoc). Because `type UUID = string`, TypeScript's interface-merging was silently accepting both declarations as a single nominal type — the exact "linter doesn't catch it, type-check doesn't catch it, IDE doesn't catch it" trap. The second declaration (the one with the JSDoc) is the canonical one; the first is removed. PR #299 (squash `f9dc9fe`).

### Backlog

- **#290 D1 (MemoryStore/PgStore divergence)** — moved to **backlog** per operator (2026-08-24). 12 packages (`auth`, `audits`, `credentials`, `hosts`, `inbounds`, `inboundtemplates`, `nodes`, `panelcfg`, `plans`, `subscription`, `users`, `webhooks`) each have a `MemoryStore` shortcut that disagrees with the prod `PgStore` semantics. Subscription is the documented case (`every pool with member is attached to every plan` vs the real `plan_pool` join). Plan: per-package `RunStoreContract(t, newStore)` called twice (Memory + Pg, "Pg wins"), start with `subscription` → `users/credentials` (security) → 9 remaining. **Out of scope for v0.8.28.6.**

## [0.8.28] - 2026-08-21

Tier 3 dialog extraction closeout + Tier 1 #3 (backup
cron) closeout + Tier 2 dependency batch + tests +
anti-leak infra hardening. The 17-PR Tier 3 batch
(#254-#270) lands the 8-dialog split of HostsView and
NodesView; the 3-PR Tier 1 #3 batch (#273-#275) ships
the cron-parser step + range + list syntax, the
scheduler goroutine test coverage, and the
`GET /api/v1/backups/schedule` endpoint + BackupsView
"Schedule" section. PR #272 closes the 2026-08-20
incident loop by adding a `ghp_` / `github_pat_` regex
to the secret scanner. Dialogs split: HostsView and
NodesView each shed their per-action dialogs into 8
self-contained Vue components under
`frontend/src/views/dialogs/`; the view files keep
only the trigger refs + per-row pointers, the
dialogs own the form state + wire-payload builder +
success card surface. Backup cron: the parser now
supports the full Vixie `*`, `N`, `N-M`, `N-M/S`,
`*/S`, `N,M,K` construct set; the scheduler goroutine
is covered by 33 tests (4 new test functions
including the `IdempotentWithinMinute`,
`AdvancesLastEvenOnNonMatch`,
`RespectsCancelledContext`, `TriggersAtScheduledTime`
coverage); the new read-only `GET
/api/v1/backups/schedule` endpoint surfaces the
active schedule (admin-scoped, `backups` scope); the
`Backups → Schedule` section in the admin UI renders
the active cron + retention + `scheduleActive` flag
so the operator can audit at a glance.

### Added

- feat(backups): extend cron parser with step + range + list syntax — `N-M`, `N-M/S`, `*/S`, `N,M,K` now supported in `parseCronField`; gocritic `unnamedResult` compliance (named returns + 2 body `:=` → `=`) (#273)
- feat(backups): add hot-reload + admin UI for schedule + retention — `Service.ReloadCron` + `GET /api/v1/backups/schedule` endpoint (admin-scoped, `backups` scope) + `BackupsView.vue` "Schedule" section + 10 i18n keys (`backups.schedule.*` in en.json + ru.json) + OpenAPI schema bump to `0.8.28` + auto-regenerated `api.d.ts` (#275)

### Changed

- refactor(frontend): dedup `ChangePasswordRequest` type (#254)
- refactor(frontend): replace `window.confirm` with `ConfirmDialog` component (HostsView + InboundsView) (#256)
- refactor(frontend): replace `as never` with typed `as Parameters<...>` casts in HostsView + NodesView (#263)
- refactor(frontend): extract `HostCreateDialog` + `HostEditDialog` from HostsView (#265)
- refactor(frontend): extract `NodeCreateDialog` + `NodeEditDialog` from NodesView (#266)
- refactor(frontend): extract `NodeProvisionDialog` from NodesView (#267)
- refactor(frontend): extract `NodeRotateDialog` from NodesView (#268)
- refactor(frontend): extract `NodeRefreshDialog` + `NodeInspectDialog` from NodesView (#269)

### Performance

- perf(frontend): memoize `camelizeKeys` for large response bodies (#255)
- perf(frontend): batch inbounds preload via new `GET /api/v1/inbounds` endpoint (one round-trip replaces the per-row `GetByNode` fan-out) (#264)

### Tests

- test(frontend): add vitest coverage for 8 extracted dialogs (52 new tests, 39 existing → 91 total) (#270)
- test(backups): add scheduler goroutine test coverage — 33 tests across 4 new test functions (`IdempotentWithinMinute`, `AdvancesLastEvenOnNonMatch`, `RespectsCancelledContext`, `TriggersAtScheduledTime`) in `scheduler_test.go` (#274)

### Security

- chore(ci): add `ghp_` / `github_pat_` regex to `BANNED_PATTERNS` in `tools/scripts/check-sensitive.sh` (and AGENTS.md mirror) — closes the 2026-08-20 3-PAT incident loop; the scanner now catches classic `ghp_`/fine-grained `github_pat_`/OAuth `gho_`/`ghu_`/`ghs_`/`ghr_` tokens. All three leaked tokens from the 2026-08-20 session have been rotated by the operator (#272)

### Chore

- chore(ci): bump node 20 → 24.19.0 (jsdom30/undici8 engines requirement) (#257)
- chore(deps): bump the minor-and-patch group in `/frontend` with 6 updates (#259)
- chore(deps): bump `golang.org/x/mod` 0.37.0 → 0.40.0 (CVE-2026-56864, CVE-2026-56865) (#261)
- chore(deps): bump the minor-and-patch group across 1 directory with 2 updates (`/backend`) (#262)

## [0.8.27] - 2026-08-16

### Added

- feat(scripts): add --dry-run to branch-start.sh, harden release.sh --snapshot (#250)
- feat(repo): anti-leak infrastructure (AGENTS.md + scanner + pre-commit + CI) (#241)
- feat(frontend): withCredentials + drop refresh from localStorage + access in-memory (#215)
- feat(auth): store refresh token in HttpOnly cookie, not the JSON body (#214)
- feat(frontend): InboundTemplatesView + Template dropdown in InboundsView (#212)
- feat(inbounds): reject inbound with TemplateID pointing at template of different protocol (#211)
- feat(renderer): sing-box BuildCoreConfigForNode reads template.params when inbound.template_id is set (#210)
- feat(inbound-templates): per-tenant Params defaults — foundation (v0.8.x) (#205)
- feat(ui): shadcn-vue RadioGroup primitive + migrate NodesView auth-method radios (#202)
- feat(ui): merge "Add node + Provision" into a single dialog (#201)
- feat(builder): per-user credential filter (closes v0.7.x Phase 2 multi-user TODO) (#198)
- feat(styles): migrate to tailwindcss v4 (CSS-first config + @tailwindcss/vite) (#197)
- feat(ui): subscription URL display in UsersView (v0.8.x) (#193)
- feat(builder): host->node mapping in Builder + user fan-out (v0.8.x) (#192)
- feat(cores): BatchedApplier 401 auto-refresh integration (v0.8.8) (#189)
- feat(nodes): refresh-agent-bearer (v0.8.7) (#188)
- feat(ui): NodesView Rotate panel key button (v0.8.3 item 2, HTTP mirror of #184) (#185)
- feat(cli): `aegis admin node rotate-panel-key` for v0.3.0..v0.7.x nodes (#184)
- feat(credentials): HTTP admin surface + UI for user_inbound_credentials (#183)
- feat(ui): password / stored-key radio for the node provision form (#180)
- feat(bootstrap): password-based first auth + persistent node SSH key (#179)
- feat(subscription): per-user credential render (Phase 2 step 4, multi-user sub URL) (#170)
- feat(credentials+cores): wire credentials through builder and narrow BatchedApplier fan-out (Phase 2 step 3) (#169)
- feat(cores): multi-user sing-box renderer (Phase 2 step 2) (#168)
- feat(credentials): Phase 2 multi-user sing-box render data model (#167)
- feat(audits): wire audit_log call-sites into every mutating service (#166)
- feat(cores): real BatchedApplier FlushFn + Enqueue from user/inbound services (#157)
- feat(ci): local docker-compose end-to-end smoke script (#152)
- feat(ui-webhooks): events multi-select in create + edit dialogs (#150)
- feat(webhooks): wire Service.Dispatch to all mutating handlers (#148)
- feat(webhooks): age envelope on endpoint secret (#147)
- feat(webhooks): background retry worker (#146)
- feat(ui-webhooks): WebhooksView + sidebar nav + i18n en/ru + Webhook icon (#139)
- feat(webhooks): admin HTTP handler + ScopeWebhooks + AEGIS_WEBHOOKS_BACKEND + wiring (#137)
- feat(webhooks): internal/webhooks package — Endpoint + Delivery + DLQ + HMAC + retry + Service (#136)
- feat(ui): PlansView + sidebar nav + i18n en/ru (#134)
- feat(plans): admin HTTP handler + ScopePlans + router/main wiring (#132)
- feat(plans): internal/plans package — core types, store, service (#131)
- feat(cli): aegis-pg-backup + aegis-pg-restore operator CLI (#125)
- feat(backups): BackupsView.vue + API client (#121)
- feat(backups): internal/backups package + admin router (#120)

### Changed

- refactor(crypto): extract internal/crypto/envelope from webhooks/secret (#177)
- refactor(backend): extract internal/app.Build from main.go (#156)
- refactor(ui-webhooks): extract shared zod schema (#149)

### Fixed

- fix(ui): inject VITE_VERSION at build time (was hardcoded v0.0.0-dev) (#253)
- fix(ui): SVG width=icon leak + SelectItem empty value (#239)
- fix(ui): surface session-expired toast + auto-redirect to /login (#238)
- fix(bootstrap): UploadAndSwap (SFTP temp+rename) for ETXTBSY-safe binary replacement (v0.8.25) (#235)
- fix(nodes): BootstrapNodeProvider.Update propagates State field (v0.8.24) (#234)
- fix(bootstrap): strip SHA256:/MD5: prefix in fingerprint compare (v0.8.23) (#233)
- fix(bootstrap): pin HostKeyAlgorithms to ed25519 so fingerprint compare is unambiguous (#232)
- fix(bootstrap): compute SSH fingerprint from binary wire format, not authorized_keys line (#231)
- fix(bootstrap): TOFU policy was unreachable when known_hosts file exists (#230)
- fix(backups): upgrade pg_dump to 16 via PGDG repo (v0.8.19) (#229)
- fix(backups): Dumper/Restorer interfaces, full DSN, propagated Close errors (#228)
- fix(backups): replace /usr/bin/pg_dump with symlink to real binary (#226)
- fix(backups,provision): copy real pg_dump binary + joinHostPort handles host:port (#224)
- fix(backups,provision): bundle pg_dump + aegis-agent in panel image (multi-stage distroless/base) (#222)
- fix(backups,inbounds): admin missing backups scope + SelectItem empty value (#221)
- fix(ui): dialog content overflow on content-heavy dialogs (v0.8.x) (#220)
- fix(docs): merge duplicate [Unreleased] in CHANGELOG (markdownlint MD024)
- fix(auth): add GetByID to Store interface, wire /me through it (#182)
- fix(ui): show write affordances when /me is broken (#172)
- fix(cli): suppress echo on aegis admin password prompts (#154)
- fix(nodes): pin State enum to migration 0006 with a regression guard (#153)
- fix(ci): verify-images handles workflow_dispatch re-runs (#130)
- fix(ci): emit `latest` tag on tag-push for non-prerelease versions (#127)
- fix(ui): four runtime quirks the Phase 1 deploy surfaced (#117)

### Documentation

- docs(runbooks): sync deploy.md §3.3/§5 to match production state (#252)
- docs(runbooks): add oncall.md (incident response playbook) (#251)
- docs: recreate docs/gap-analysis-v0.8.24.md (3 broken links) (#246)
- docs(deploy): sops+age runbook fixes + distroless UID 65532 gotcha (#191)
- docs: sync to v0.8.0 (Phase 2 multi-user sing-box render milestone) (#171)
- docs: sync to v0.7.2 (audit batch closeout)
- docs: sync to v0.7.1 (#151)
- docs: sync to v0.7.0 and the post-v0.7.0 4-PR dependency batch (#145)
- docs(v0.7.0): CHANGELOG + ROADMAP + api reference for /webhooks (#140)
- docs(webhooks): OpenAPI /webhooks endpoints + hand-mirrored services/webhooks.ts (#138)
- docs: v0.6.0 CHANGELOG + ROADMAP + plans API reference (#135)
- docs(openapi): add /plans endpoints + Plan schema + PlanResetPeriod (#133)
- docs: operator guide + security policy + quickstart (v0.5.0) (#126)

### CI

- ci(release): add smoke-test gate before cosign re-sign (#247)

### Tests

- test(cores): end-to-end integration test for BatchedApplier + FlushFn (#158)
- test(ui): add vitest suite for zod schemas (#155)

### Chore

- chore(repo): gitignore .local/ directory (gh CLI state etc.) (#249)
- chore(deps): bump go 1.26.5 → 1.26.6 (govulncheck 6 stdlib advisories) (#248)
- chore(docs): scrub historical banned-value leaks in RUNBOOKS/deploy.md + fix scanner regex anchor (#245)
- chore(repo): scrub historical banned-value leaks (14 docs/code files) (#244)
- chore(test): replace real banned value in ssh_test.go with synthetic fixture (#243)
- chore(repo): gitignore release-notes drafts + sync AGENTS.md (#242)
- chore(release): cut v0.8.26 - CHANGELOG surgery (#240)
- chore(repo): drop tracked .git-pr-title.txt scratch + broaden .gitignore (#237)
- chore(docs): sync to v0.8.25 (the silent-bug chain closeout) (#236)
- chore(release): cut v0.8.25 - CHANGELOG surgery
- chore(release): cut v0.8.24 - CHANGELOG surgery
- chore(release): cut v0.8.23 - CHANGELOG surgery
- chore(release): cut v0.8.22 - CHANGELOG surgery
- chore(release): cut v0.8.21 - CHANGELOG surgery
- chore(release): cut v0.8.20 - CHANGELOG surgery
- chore(release): cut v0.8.19 - CHANGELOG surgery
- chore(release): cut v0.8.18 - CHANGELOG surgery
- chore(release): cut v0.8.17 - CHANGELOG surgery (#227)
- chore(release): cut v0.8.16 - CHANGELOG surgery (#225)
- chore(release): cut v0.8.15 - CHANGELOG surgery (#223)
- chore(docs): sync to v0.8.14 (release cut + body-field shim closure) (#219)
- chore(release): cut v0.8.14 — CHANGELOG surgery, closes 4-PR audit-3.1 fix + body-drop gap (#214, #215, #216, #217) (#218)
- chore(auth): drop refresh_token from login/refresh JSON bodies (v0.8.14) (#217)
- chore(caddy): add Content-Security-Policy header for the admin path (#216)
- chore(release): cut v0.8.13 — CHANGELOG surgery, closes 4-PR inbound-templates gap (#205, #209, #210, #211, #212) (#213)
- chore(docs): sync to v0.8.12 + inbound-templates foundation (PR #205) (#209)
- chore(release): cut v0.8.12 — CHANGELOG surgery, closes 3-PR gap (#201, #202, #203) (#204)
- chore(docs): close shadcn-vue RadioGroup primitive entry (PR #202 follow-up) (#203)
- chore(frontend): lint cleanup  auto-fix 105 vue warnings in 5 files (#200)
- chore(release): cut v0.8.11  CHANGELOG surgery, closes 3-PR gap (#196, #197, #198) (#199)
- chore(frontend-deps): bump @vueuse/core 11>14, vite 7>8, jsdom 25>30 (#196)
- chore(release): cut v0.8.10  CHANGELOG surgery, closes 3-PR gap (#192, #193, #194) (#195)
- chore(docs): sync to v0.8.9 (README, operator-guide, SECURITY, quickstart, deploy/secrets) (#194)
- chore(release): cosign re-sign + verify on every release (v0.8.9) (#190)
- chore(ops): JSON logs in production, hardened (v0.8.6) (#187)
- # feat(ui): NodesView "Show stored key" debug surface (v0.8.5, read-side mirror of the v0.8.1 persistent key) (#186)
- chore(docs): sync to v0.8.1 (#181)
- chore(frontend-deps): bump brace-expansion 5.0.8 → 5.0.9 (CVE GHSA-rgw5-rvv9-x895) (#178)
- chore(frontend-deps): bump @vue/tsconfig 0.8.1 → 0.9.1 + postcss 8.5.24 → 8.5.25 (#165)
- chore(frontend-deps): bump axios 1.18.1 → 1.19.0 (CVE-2026 GHSA-hmw2-7cc7-3qxx) (#163)
- chore(frontend-deps): bump css/sass toolchain (3 packages) (#161)
- chore(frontend-deps): bump ts/types toolchain (5 packages) (#159)
- chore(deps): bump vue-i18n to 11.4.8 (#144)
- chore(deps-dev): bump vue-tsc to 3.3.8 and fix WebhooksView DataTable props (#143)
- chore(deps): bump pinia to 4.0.2 and add @vue/devtools-api (#142)
- chore(deps): bump Go minors (prometheus 1.24.1, env 11.4.1, zerolog 1.35.1) (#141)
- chore(release): cosign sign + verify for panel and UI images (#129)
- chore(obs): JSON logs in production via AEGIS_ENV (#128)
- chore(ops): install_panel role + prod compose + secrets.env mount (#124)
- chore(ops): install_singbox — runtime SHA-256 via GitHub Releases API (#123)
- chore(ops): tools/scripts/pre-pr.sh + pre-push hook (#122)
- chore(ops): secrets via sops+age (#119) (#119)
- chore(repo): gitignore operator deploy scripts under tools/scripts/ (#118)

[0.8.28]: https://github.com/QAdversif/AegisPanel/releases/tag/v0.8.28
[0.8.27]: https://github.com/QAdversif/AegisPanel/releases/tag/v0.8.27
