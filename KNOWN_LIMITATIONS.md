# Known Limitations — AegisPanel v0.7.0

This document tracks the gaps between what the latest shipped
milestone delivers and the full design in `ARCHITECTURE.md` §21.
Every open entry points to the milestone that closes it.
**Closed** items are kept for context — the PR that closed each
one is named so future readers can find the diff.

The current state of the project is **v0.7.0** (the outgoing
webhook surface) on top of the v0.5.0 operations-grade feature
set and the v0.6.0 plans CRUD. The v0.7.0 tag is the next
candidate release; v0.7.x is the planned follow-up batch (see
`docs/ROADMAP.md`).

## v0.7.0 — currently open

### Operations

#### Webhook call-site wiring — v0.7.x

`internal/webhooks.Service.Dispatch` is wired end-to-end on the
HTTP / `POST /webhooks/{id}/test` path (v0.7.0). The production
event flow — calling `Dispatch` from every mutating handler in
`internal/{auth,nodes,inbounds,hosts,users,plans,backups}` — is
NOT in v0.7.0; it lands in the v0.7.x follow-up batch. Until
then, the only way to verify a webhook end-to-end is the
`POST /webhooks/{id}/test` endpoint. The v0.7.x batch will add
the `internal/events` package as the in-process event bus that
the handlers emit into.

#### `sops` envelope on `webhook_endpoints.secret` — v0.7.x

`webhook_endpoints.secret` is stored in plaintext in the
`webhook_endpoints` table (per v0.7.0). The sops+age flow that
the `configure_secrets` Ansible role (#119) provides for the
panel's own boot secrets does not yet wrap this column. v0.7.x
moves the secret to sops+age at rest via a new migration (or a
transparent encrypt/decrypt layer on the `Service`); the
redaction logic on the read path (`***` on every read except the
verbatim-on-Create response) stays as-is. v0.7.x also extends
`docs/operator-guide.md` with a "rotating a webhook secret"
section.

#### Background worker for retry — v0.7.x

`webhooks.Service.RetryDelivery` exists (v0.7.0) and is called
from the synchronous `POST /webhooks/{id}/test` flow, but the
production retry loop (1s / 5s / 25s / 2m15s / 11m15s, max 6
attempts) is NOT in a background worker. v0.7.0 ships the
state-machine; v0.7.x lands the goroutine in `cmd/aegis/main.go`
that ticks the schedule. The infrastructure is ready
(`webhooks.NewScheduler(svc, tick)` is the v0.7.x API).

#### Webhook event types: "all" wildcard only — v0.7.x

v0.7.0 ships every endpoint subscribing to the wildcard (every
event the panel emits). v0.7.x replaces the wildcard with a
multi-select per endpoint (the UI ships the picker; the backend
validates the closed `EventType` enum on Create / Update).

#### Shared zod schema at `frontend/src/schemas/webhook.ts` — v0.7.x

The `WebhooksView` form uses inline zod via `useZodForm` (same
pattern as the v0.6.0 `PlansView`). v0.7.x moves the schema to
`frontend/src/schemas/webhook.ts` so the create / edit / test
dialogs share a single source of truth (matches the convention
that other views will adopt as the UI matures).

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
