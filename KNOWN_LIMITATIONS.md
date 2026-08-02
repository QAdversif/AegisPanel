# Known Limitations — AegisPanel v0.7.2

This document tracks the gaps between what the latest shipped
milestone delivers and the full design in `ARCHITECTURE.md` §21.
Every open entry points to the milestone that closes it.
**Closed** items are kept for context — the PR that closed each
one is named so future readers can find the diff.

The current state of the project is **v0.7.2** (the
v0.7.0 + v0.7.1 `internal/webhooks` surface + the v0.7.2
audit-batch closeout: real BatchedApplier FlushFn +
Enqueue + end-to-end integration test, plus the
God-object main.go extraction into `internal/app`).
The next candidate release is v0.8.0
(`internal/notifications`).

## v0.7.2 — currently open

### Operations

#### Audit log call-site wiring — v0.7.x+

Every mutation in the admin handler stack should write an
`audit_log` row. The `Service` struct pattern that PR #148
used to wire `webhooks.Dispatch` is the same pattern a
future PR would use to wire `audits.Record` (per-handler
audit log write). Not in scope for v0.7.2; tracked as a
separate batch.

#### Phase 2 multi-user sing-box render — Phase 2

The v0.4.0-mvp-batched BatchedApplier FlushFn
re-renders the full node config on every flush
(via `internal/cores/builder/BuildCoreConfigForNode`),
but the v0.4.0-era sing-box renderer is single-user
per inbound: the protocol-level `users` array inside
the rendered config carries the operator's credential
from `inbound.Params["uuid"]` or `["password"]`. The
BatchedApplier infrastructure (cancel/replace
semantics, per-node goroutines, the FlushFn closure)
is in place; a Phase 2 PR fills in the per-user
mapping so user CRUD events actually move users
into and out of the rendered config. v0.7.2 already
emits the right `cores.Delta` for the future
renderer to consume (`DeltaAddUser` /
`DeltaRemoveUser` / `DeltaSetLimit{Bytes: <int64>}`).

#### Inbound-templates work — Phase 2

A future "inbound templates" feature is the natural
home for the per-user render. Today, every
`inbounds` row carries its own `Params` blob
(operator's credential); the templates work is
the per-tenant credentials layer that lets one
inbound serve many users.

#### Pre-existing `vue/max-attributes-per-line` template
warnings — chore

The v0.7.0 view templates (WebhooksView, PlansView, a
handful of dialogs) carry pre-existing eslint warnings for
`vue/max-attributes-per-line` and
`vue/singleline-html-element-content-newline`. They're
auto-fixable with `pnpm lint --fix`; not in scope for any
release PR.

## v0.7.2 — closed in v0.7.2

| Item | Closed by |
| --- | --- |
| Audit #1 — God-object main.go (composition root extracted). `cmd/aegis/main.go` is now 199 lines (was 728). The composition root moved to a new `internal/app` package exposing `Build(ctx, cfg) (*App, error)` + `App.Close()`. The pattern matches the wire sweet-spot for ~11 services: a generic `MustBuild[T]` helper with a `StoreBuilder[T]` struct + centralized production-vs-memory check. `router.Build` now takes a `ctx context.Context` first parameter (the `panelcfgSvc.GetActive` read at the rotated sub_path mount was hardcoded `context.Background()`; the boot context applies, so a SIGINT during boot aborts the read). 14 unused imports removed, 2 duplicate helpers removed (`mustHash`, `newSubscriptionRateLimiter`). | PR #156 |
| Audit #2 — BatchedApplier no-op stub (real FlushFn + Enqueue). The v0.4.0-mvp-batched BatchedApplier shipped with a `log.Info + return nil` FlushFn AND Enqueue was never called outside tests. v0.7.2 wires the v0.5.0 real path end-to-end. New `internal/cores/builder/builder.go` with `BuildCoreConfigForNode` + `NewFlushFn` (per-node closure: `Build → Render → Apply` with structured error logging). `users.Service.WithBatchApplier` + `enqueueUserDelta` fan out to every registered applier. `inbounds.Service.WithBatchApplier` + `enqueueForNode` narrows to the single applier for the inbound's node. `App.BatchedAppliers` map + `App.AddNodeBatchedApplier` (registers the per-node applier, spawns the Run goroutine, owns the cancel funcs). `App.Close()` cancels every BatchedApplier goroutine alongside the existing webhook worker cancel + pg pool close. `AEGIS_BATCHED_APPLIER_ENABLED` (default `true`) gates the per-node wiring loop. | PR #157 |
| Audit #2 — end-to-end integration test for the BatchedApplier + FlushFn. `internal/cores/builder/flushfn_integration_test.go` behind `//go:build integration`. Self-skips when `INTEGRATION_DATABASE_URL` is unset (local `go test ./...` skips; CI's backend job runs it). The headline test drives a real `users.Service.Create` against a real pg (via `testutil.MustNewPool`); the post-commit enqueue reaches the per-node BatchedApplier; the 200ms window fires; the FlushFn re-renders the sing-box config (reading through the inbounds PgStore); the fake agent receives a POST /v1/apply whose JSON envelope contains exactly the vless inbound we seeded with the UUID we put in `inb.Params`. The test pins the panel→agent wire contract end-to-end. | PR #158 |

## v0.7.1 — closed in v0.7.1

| Item | Closed by |
| --- | --- |
| Webhook call-site wiring — `webhooks.MustDispatch` (non-blocking, nil-safe, 5s-bounded) called from every mutating handler in `internal/{users,plans,nodes,hosts,inbounds,backups}` AFTER the row is persisted; `WithWebhooks(svc)` setter pattern preserves the 167+ existing test fixtures; 6 `dispatcher_test.go` files via the new `webhooks.Spy` test double | PR #148 |
| Background worker for webhook retry — `webhook_pending_retries` table (FK cascade on `webhook_deliveries.id`, `ON CONFLICT DO UPDATE`), `Store.EnqueueRetry/DequeueRetry/ListDueRetries`, `Service.ProcessDueRetries`, `internal/webhooks/worker.go` goroutine with per-tick context bounded to the interval, `AEGIS_WEBHOOKS_RETRY_WORKER_ENABLED` (default true) + `AEGIS_WEBHOOKS_RETRY_WORKER_INTERVAL` (default 5s) | PR #146 |
| `sops+age` envelope on `webhook_endpoints.secret` — `SecretCipher` interface + `AgeSecretCipher` (filippo.io/age v1.3.1, X25519+ChaCha20-Poly1305, multi-recipient for key rotation) + `NoopSecretCipher` (dev); migration 0018 destructive rename `secret → secret_ciphertext BYTEA`; `AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS` (csv `age1...`) + `AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE`; `NewPgStore(pool, nil)` panics so a misconfigured boot is loud | PR #147 |
| Webhook events multi-select in UI — `WebhookEventsPicker.vue` (native checkbox grid, 18 closed event types, grouped by entity, "N of 18 selected" header badge), wired into both the create and edit dialogs; i18n en + ru | PR #150 |
| Shared zod schema at `frontend/src/schemas/webhook.ts` — `webhookEventTypeSchema` (z.enum of the 18 closed types), `webhookUrlSchema`, `webhookSecretSchema` (16-256 chars, fixed a latent length-bypass bug in the previous inline edit schema), `webhookCreateSchema`, `webhookUpdateSchema` (`.partial().strict()`; secret is `z.union([z.literal(''), webhookSecretSchema]).optional()` so the empty-string "leave unchanged" path is preserved); re-exported from `frontend/src/schemas/index.ts` | PR #149 |
| Audit #3 — No UI tests (vitest suite for zod schemas). New `frontend/src/schemas/schemas.test.ts` with 38 vitest tests across `primitives.ts` (uuid, isoDateTime, tag), `user.ts` (create + update, `.partial().strict()` + unknown-keys rejection), `webhook.ts` (create + update + closed 18-event enum + url/secret rules + empty-string-secret "leave unchanged" affordance). `npm run test` uncommented in `.github/workflows/ci.yml`. | PR #155 |
| Audit #4 — `aegis admin` password prompts leaked to terminal. `golang.org/x/term v0.45.0`; `promptPassword` opens `/dev/tty` directly and calls `term.ReadPassword` which toggles `ECHOCTL`/`ICANON` so the kernel suppresses the echoed bytes. Non-tty fallback to legacy `bufio.Reader` preserves the `echo pw \| aegis admin add user --email …` automation in `deploy/ansible/`. On Windows the fallback is a known limitation (the platform line discipline does not honour the same ECHOCTL contract as Unix; documented in the `promptPassword` docstring). | PR #154 |
| Audit #6 — `nodes.State` enum vs migration 0006 `nodes_state_check` CHECK constraint. The mismatch was a false alarm (migration 0006 added in PR #37 already aligned them), but the only existing test (`TestPgStore_Create_RoundTrip`) only exercised `StateNew`. v0.7.1 added `TestPgStore_Create_AllStatesPassStateCheck` (table-driven: every member of the closed `State` enum flows through `Store.Create`) + the `node.go` docstring names the migration + the test. The enum↔CHECK agreement is now pinned permanently. | PR #153 |
## v0.7.0 — closed in v0.7.0

| Item | Closed by |
| --- | --- |
| `internal/webhooks` package — Endpoint + Delivery + DLQ models, EventType closed enum (18 types), HMAC sign/verify, retry schedule (1s / 5s / 25s / 2m15s / 11m15s, MaxAttempts=6), Service (`Dispatch` + `RetryDelivery` + `ReplayDLQEntry` + `SendTestEvent`), Store (MemoryStore + PgStore), Migrations 0014 (webhook_deliveries + webhook_dlq) + 0015 (webhook_endpoints.updated_at) + 0016 (`UNIQUE (url)`) | PR #136 |
| `webhooks.AdminRouter` — 11 endpoints (CRUD + deliveries + test + DLQ CRUD + replay) behind `auth.RequireScope(ScopeWebhooks)`, `AEGIS_WEBHOOKS_BACKEND` env flag (memory / pg), secret redaction (verbatim on Create, `***` on every read) | PR #137 |
| OpenAPI spec — 11 paths + 12 schemas under `/api/v1/webhooks/*`, hand-mirrored `services/webhooks.ts` (12 functions + 2 DTOs + 5 type re-exports), `api.d.ts` regenerated | PR #138 |
| `WebhooksView.vue` + sidebar nav + i18n en/ru + `Webhook` lucide icon + one-time secret display widget | PR #139 |
| Cosign sign + verify for our Docker images (panel + agent) — fixes the post-`v0.4.0` supply-chain gap | PR #129 + #130 |
| `latest` tag on tag-push for non-prerelease versions (post-`v0.5.0` follow-up) | PR #127 |
| JSON logs in production via `AEGIS_ENV=production` (post-`v0.5.0` follow-up) | PR #128 |

## Closed in v0.6.0

The `plans` table was in migration 0001 from the start (a
v0.3.0 stub); v0.6.0 promotes it to a real CRUD surface with
a typed Go package, an HTTP admin handler, an OpenAPI spec,
and a UI view. v0.6.0 is the second post-v0.4.0 milestone and
lands the operator-facing tariff catalog.

| Item | Closed by |
| --- | --- |
| `internal/plans` package — Plan + ResetPeriod closed enum (daily / weekly / monthly / never), Store interface + MemoryStore + PgStore + Service with input validation (Name 1..64 chars, Duration [1 minute, 10 years], non-negative numbers, ResetPeriod enum), 23 unit tests + 4 pg integration tests | PR #131 |
| `plans.AdminRouter` — `GET /` + `GET /{id}` + `POST /` + `PATCH /{id}` + `DELETE /{id}` behind `auth.RequireScope(ScopePlans)`, 11 e2e tests | PR #132 |
| OpenAPI spec — `/plans` paths + Plan schema + PlanCreateRequest + PlanUpdateRequest + PlanListResponse + PlanResetPeriod enum, `services/plans.ts` hand-mirror, `api.d.ts` regenerated | PR #133 |
| `PlansView.vue` + sidebar nav + i18n en/ru + zod form schema | PR #134 |

Deferred to v0.6.x (logged in `docs/ROADMAP.md`):

- `plan_pool` writes (the join table linking plans to host
  pools). v0.6.0 keeps the read-only view in
  `internal/subscription`.
- `plan_pool` UI (no HostPool picker in the plan dialog yet).
- Audit log writes from the mutating handler (the call-site
  wiring is a separate batch across all admin handlers).

## Closed in v0.5.0

v0.5.0 is the "operations-grade" feature set the panel needs to
be deployable for the soft launch. All eight items landed in
PRs 119 through 126. The detailed scope breakdown is in
`docs/ROADMAP.md` §"v0.5.0 — polish before v0.6.0+".

| Item | Closed by |
| --- | --- |
| sops+age secrets (`configure_secrets` Ansible role) | PR #119 |
| `internal/backups` package — `pg_dump` + sidecar SHA-256, per-node queue, 20s window, single-flight via `inflight sync.Mutex`, retention via age + max count, `pg_restore` gated by `AEGIS_BACKUPS_ALLOW_UI_RESTORE` | PR #120 |
| `BackupsView.vue` + i18n + sidebar nav + download | PR #121 |
| Pre-PR local gate (`tools/scripts/pre-pr.sh` + Makefile + pre-push hook) — gofmt, golangci-lint v2, vue-tsc, eslint, markdownlint-cli2, go test -short, npm run codegen:check | PR #122 |
| GitHub API SHA-256 fetch for sing-box (`install_singbox` role) — replaces the v0.4.0-c hardcoded digest | PR #123 |
| Container wiring for #119 secrets (`install_panel` role + `docker-compose.prod.yml.j2`) — bind-mount `/etc/aegis/secrets.env` read-only into the panel container; `aegis-agent.service` gains a secondary `EnvironmentFile=-/etc/aegis/secrets.env` | PR #124 |
| Operator-side backup CLI (`aegis-pg-backup` + `aegis-pg-restore`) — separate binaries; two-step id confirmation; `--dry-run` for `pg_restore --list` | PR #125 |
| `docs/operator-guide.md` (canonical install + daily-ops reference) + `docs/SECURITY.md` (threat model + disclosure flow + supply-chain trust) + `docs/guide/quickstart.md` (5-minute fresh-VPS flow) | PR #126 |

## Closed in v0.4.0

These items are kept here so a reader of
`ARCHITECTURE.md §21 / v0.4.0` can see what was actually
delivered, and so the diff between v0.3.0 and v0.4.0 is
auditable.

| Item | Closed by |
| --- | --- |
| `BatchedApplier` + real apply transport + `install_singbox` Ansible role (panel → aegis-agent → sing-box config write → reload, end-to-end) | PRs #92, #93, #94 (v0.4.0-mvp-batched) |
| `internal/users` data layer (d.1) — User + Status + MemoryStore + PgStore, 32-byte / 64-hex-char `sub_token` | PR #95 (d.1) |
| `users.User` wire-format compat with `subscription.User` (snake_case JSON, `[]uuid.UUID` for hosts) | PR #96 (d.r1) |
| Drop subscription-side user-CRUD (Store / MemoryStore / PgStore / Service-level thin wrappers) | PRs #97, #99 (d.r2, d.r3) |
| Move `admin_handler.go` to `internal/users`; drop the 4 Service thin wrappers | PR #99 (d.r3) |
| `DefaultSubTokenRotationGrace` as a public package constant; `docs/ROADMAP.md` published | PR #100 (d.r4) |
| Release workflow fixes (GHCR lowercase, `workflow_dispatch` push, UI image tag input, explicit panel semver tags) — no application code change | PRs #102, #103, #104, #111 (post-tag) |

## Closed in v0.3.0

| Item | Closed by |
| --- | --- |
| BYO-node bootstrap backend provisioner | v0.3.0-mvp-byo-node |
| "Add node" UI dialog (modal in `NodesView`, status badge, i18n) | v0.3.0-mvp-byo-node |
| Real `aegis-agent` Go binary + Ansible `install_agent` role (replaces the `sleep infinity` placeholder) | v0.3.0-mvp-byo-node |
| Per-node `AgentBearer` storage (`nodes.agent_bearer` column, migration 0013) | v0.3.0-mvp-byo-node |
| chi v5.2.4 → v5.3.1 + `ClientIPFrom*` IP extraction (closes `GHSA-3fxj-6jh8-hvhx`) | PR #75 |
| Trivy workflow `ignorefile:` → `trivyignores:` | PR #74 |
| Frontend `eslint --fix` (171 auto-fixable warnings → 0) | PR #76 |
| 11 reserved-package `doc.go` stubs (cabinet, caddy, cascades, decoy, events, mcp, notifications, plans, stats, subscriptions, webhooks) | PR #77 |
| vitest 3 → 4 | PR #82 |
| eslint 8 → 10 flat config | PR #83 |
| vue-router 4 → 5 + vite 6 → 7 + pinia 2 → 3 | PR #84 |
| `.gitattributes` + `npm ci` standardisation (Windows CRLF fix) | PR #87 |
| vite 7.3.0 → 7.3.6 (6 dependabot advisories, all `Development`-only) | PR #89 |
| `brace-expansion@2 → 5` + `js-yaml@3 → 4` (3 HIGH-severity OSV findings) | PR #90 |
| Custom Caddy binary (drops upstream Caddy `grpc-go` CVE by patching to `v1.82.1`) | PR #91 |

## Closed in v0.2.0

These items are kept here so a reader of
`ARCHITECTURE.md §21 / MVP-0.2` can see what was actually
delivered, and so the diff between v0.1.0 and v0.2.0 is
auditable.

| Item | Closed by |
| --- | --- |
| Per-node inbounds editor | PR #62 (PR-I) |
| Host create / edit dialogs | PR #61 (PR-H) |
| User CRUD | PR #60 (PR-G) |
| Settings UI (panelcfg HTTP) | PR #59 (PR-F) |
| OpenAPI codegen for the TS types | PR #65 (PR-L) |
| Real subscription rate-limiting | PR #64 (PR-K) |
| Argon2id for the admin password (operational gap closed by `aegis admin` CLI; production seed guard) | PR #63 (PR-J) |
| Audit log + operator profile (read surface) | PR #66 (PR-M) |
| Sub-token rotation + URL-prefix rotation | PR #47 |

## What's NOT a limitation

These are sometimes mistaken for gaps; they are intentional.

- The default admin password is documented in
  `deploy/ansible/group_vars/all.yml` — not a backdoor, just
  an operator onboarding aid. v0.5.0+ sops+age flow makes the
  rotation path documented in
  `docs/operator-guide.md` §"Secrets rotation".
- The default dark theme is intentional (dev-tool aesthetic
  per `ADR-0004`). Light theme is a token swap away; the
  light-theme polish is on the v1.5+ roadmap.
- Subscriptions render the sing-box format by default; Clash /
  base64 / HTML are available via the `?format=` query
  parameter and the `/subscription` view.
- The project is single-tenant by design. See
  `ARCHITECTURE.md` §27 and the relevant ADR (multi-tenant was
  explicitly rejected in v9).
- 9 packages remain `doc.go`-only placeholders (cabinet,
  caddy, cascades, decoy, events, mcp, notifications, stats,
  subscriptions-plural). Of these, `plans` and `webhooks` are
  done (v0.6.0, v0.7.0); the rest are post-v1.0. They are
  listed in `docs/ROADMAP.md` §"Open gaps (post-v0.4.0 audit)".
