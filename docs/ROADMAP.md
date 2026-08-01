# AegisPanel — Roadmap

> **Living document.** Updated each milestone as the d-refactor / v0.x / v1.0
> ladder progresses. The next unreleased tag is at the top; completed tags
> are listed in `CHANGELOG.md` (per-release log) and `docs/adr/`
> (architecturally significant decisions).

## Status (2026-07-31)

| Tag                  | Scope                                                                                                  | Status               |
| ---                  | ---                                                                                                    | ---                  |
| `v0.1.0-mvp-render`  | Public-facing subscription endpoint, render orchestrator, base64 + singbox + Clash + HTML            | ✅ shipped (#50–58)  |
| `v0.2.0-mvp-agent`   | `aegis-agent` (Go binary) on every node, real `apply` writes, reload                                       | ✅ shipped (#59–66)  |
| `v0.3.0-mvp-byo-node` | BYO-node bootstrap (SSH + TOFU + provisioning)                                                          | ✅ shipped (#67, #74–77, #82–84, #87) |
| `v0.4.0-mvp-batched` | `BatchedApplier` + real apply transport + `install_singbox` Ansible role                                  | ✅ shipped (#92, #93, #94) |
| `v0.4.0-d.1`         | `internal/users` data layer (post-d.0 split; precedes the Path C consolidation)                          | ✅ shipped (#95)      |
| `v0.4.0-d.r1`        | `users.User` wire-format compat with `subscription.User`                                                  | ✅ shipped (#96)      |
| `v0.4.0-d.r2`        | Drop subscription-side Store / MemoryStore / PgStore user-CRUD (Path C step 2)                          | ✅ shipped (#97)      |
| `v0.4.0-d.r3`        | Move `admin_handler.go` to `users`; drop Service-level thin wrappers (Path C step 3)                   | ✅ shipped (#99)      |
| `v0.4.0-d.r4`        | Cleanup pass + roadmap — Path C step 4                                                                  | ✅ shipped (#100)     |
| `v0.4.0-post`        | Release workflow fixes: GHCR image tag lowercase, `workflow_dispatch` push, UI image tag input, explicit semver tags — no application code change | ✅ shipped (#102, #103, #104, #111) |
| `v0.4.0`             | Tag for the d-r-series; aggregate of #95–#100, on commit `3beff0f` → `db151f2` (rewritten post history-edit) | ✅ shipped |
| `v0.5.0`             | sops+age secrets, backup/restore (pkg + UI + CLI), pre-PR gate, GitHub-API sing-box SHA-256, container wiring for secrets, operator guide + SECURITY + quickstart | ✅ shipped (#119, #120, #121, #122, #123, #124, #125, #126) |
| `v0.6.0`             | `internal/plans` (table already exists in migration 0001)                                                | ✅ shipped (#131, #132, #133, #134) |
| `v0.7.0`             | `internal/webhooks` (table already exists in migration 0001)                                             | ✅ shipped (#136, #137, #138, #139) |
| `v0.7.1`             | Webhook call-site wiring, sops+age envelope on `webhook_endpoints.secret`, background worker for retry, shared zod schema at `frontend/src/schemas/webhook.ts`, events multi-select in the WebhooksView; plus the post-v0.7.0 Go+frontend dependency batch (#141, #142, #143, #144) and the docs sync (#145) | ✅ shipped (#146, #147, #148, #149, #150) |
| `v0.8.0`             | `internal/notifications` (Telegram + generic webhook via n8n)                                            | ⏳                   |
| `v0.9.0`             | Smoke test on fresh VM in CI (terraform + ansible + boot log artifact)                                  | ⏳                   |
| `v1.0.0-mvp-soft-launch` | GA tag — minimum surface for the public release                                                       | ⏳                   |

## Path C: v0.4.0-d consolidation

The d.1 PR (#95) shipped a `users` package that duplicated
`subscription`'s user-CRUD. Path C is the consolidation:

1. **d.1 (#95)**: `internal/users` data layer — landed.
2. **d.r1 (#96)**: `users.User` wire-format compat with
   `subscription.User` (snake_case JSON, `[]uuid.UUID` for
   hosts allow/block lists). Made the move possible without
   render-code churn. — landed.
3. **d.r2 (#97)**: Drop subscription-side Store / MemoryStore /
   PgStore user-CRUD. Type-alias `User = users.User` makes
   the render code (`render.go` / `render_singbox.go` /
   `render_clash.go` / `render_vars.go`) a no-op compile
   change. Service keeps 4 thin wrappers for the admin +
   render paths. — landed.
4. **d.r3 (#99)**: Move `admin_handler.go` to
   `internal/users/admin_handler.go`. Drop the 4 thin
   wrappers from `subscription.Service`. Render handler
   consults `*users.Service` directly for the
   `sub_token`-→-user lookup. — landed.
5. **d.r4 (#100)**: Cleanup pass + this roadmap.
   `DefaultSubTokenRotationGrace` is now a public constant
   on `users.Service` (was a test-internal re-export).
   Subscription package doc trimmed of the d.r2
   "AEGIS_USERS_BACKEND" reference. — landed.

**Net Path C diff:** `internal/subscription` shed ~600 lines
(Store / MemoryStore / PgStore user-CRUD + Service thin
wrappers + admin handler); `internal/users` gained ~900
lines (d.1 + d.r1 + d.r2 + d.r3 + d.r4 in this PR). The
subscription package is now a pure render orchestrator.

## v0.4.0 release workflow contract

The `release.yml` workflow supports two event
paths that now produce **identical** GHCR tag
lists for both images:

- **Tag-push** (`git push origin vX.Y.Z`) —
  `github.event_name = 'push'`,
  `github.ref_name = 'vX.Y.Z'`. Login + push +
  Create GitHub release all run. Panel
  `metadata-action` emits `[X.Y.Z, X.Y, latest]`;
  UI tagged `ghcr.io/qadversif/aegispanel-ui:vX.Y.Z`.
- **workflow_dispatch re-run**
  (`gh workflow run release -f tag=vX.Y.Z`) —
  `github.event_name = 'workflow_dispatch'`,
  `github.ref_name = 'main'`. Login + push run;
  Create GitHub release is SKIPPED. Panel
  `[X.Y.Z, X.Y, latest]` (via the
  `Compute release version` step + raw tags);
  UI `:vX.Y.Z` (via `env.release_tag`).

This contract was fixed across four PRs
(#102, #103, #104, #111) that landed on
`main` *after* the `v0.4.0` git tag (which
points to `39d4d9e`). The fixes are
infrastructure-only (no application code
change) and are documented under `[Unreleased]`
in `CHANGELOG.md` to be picked up by the next
`v0.4.1` / `v0.5.0` release. The previous
behaviour — push silently disabled on
`workflow_dispatch`, UI image tagged with the
branch name, panel `X.Y.Z` / `X.Y` tags left
on the original tag-push digest — is gone.

The `latest` tag follows correctly in both
cases: skipped for prerelease (`-rc` / `-beta`
/ `-alpha`) on tag-push via the
`flavor: latest=auto`; emitted on
`workflow_dispatch` from the default branch
via the raw `enable={{is_default_branch}}`.

## v0.5.0 — polish before v0.6.0+

Scope is the "operations-grade" feature set the panel
needs to be deployable for the soft launch. **All eight
items landed in #119–#126.**

- **sops+age secrets (`configure_secrets` role)** — #119.
  The panel host decrypts `secrets.yml.enc` to
  `secrets.env` (mode 0600, owner `aegis-deploy`).
  The plaintext never leaves the host; CI never decrypts.
- **`internal/backups` package** — #120. `pg_dump -Fc | gzip`
  with SHA-256 sidecar; per-node queue; 20s window;
  `inflight sync.Mutex` for single-flight. HTTP
  endpoints at `/api/v1/backups/` gated by `ScopeBackups`
  plus `AEGIS_BACKUPS_ALLOW_UI_RESTORE` (the latter is a
  sanity check, not a security boundary — the DSN is).
- **Backups UI (`BackupsView.vue`)** — #121. List, trigger,
  download, delete. Wire format: `Backup{ID, CreatedAt,
  SizeBytes, Trigger, Status, Error, SchemaVersion,
  NodeCount, UserCount, HostCount, ChecksumSHA256, Path}`.
  Trigger is `manual | scheduled`. Status is
  `running | ok | failed`.
- **Pre-PR local gate (`tools/scripts/pre-pr.sh`)** — #122.
  gofmt + golangci-lint v2 + vue-tsc + eslint +
  markdownlint-cli2 + go test -short + npm run codegen:check
  locally. Scope flags: `--backend`, `--frontend`, `--docs`,
  `--quick`. Makefile targets: `pre-pr`, `pre-pr-install`.
  Pre-push hook installer: `tools/scripts/install-pre-push.sh`.
- **GitHub API SHA-256 fetch for sing-box
  (`install_singbox` role)** — #123. Replaces the
  v0.4.0-c hardcoded digest with a runtime fetch from
  `https://api.github.com/repos/SagerNet/sing-box/releases/tags/v{{ version }}`.
  **GPG-verify was the original scope; dropped** —
  SagerNet does not publish detached GPG / minisign
  signatures or a `SHA256SUMS` file. The trust model
  is therefore the GitHub API response (TLS + GitHub's
  signing keys), not a stronger guarantee than
  "trust GitHub". Cosign sign + verify for **our** Docker
  images is the v0.5.x equivalent for the panel/agent
  supply chain and is a separate, future PR.
- **Container wiring for #119 secrets (`install_panel` role
  plus `docker-compose.prod.yml.j2`)** — #124. The
  `secrets.env` file is bind-mounted read-only into the
  panel container via `env_file:` (with `required: true`).
  Loopback-only port 8080. Data services (Postgres /
  Redis / NATS) are operator-managed; the role refuses
  to start the panel without the secrets file present.
  The `aegis-agent.service` unit gains a secondary
  `EnvironmentFile=-/etc/aegis/secrets.env` (the
  systemd `-` prefix tells systemd to silently skip
  the file if missing; the per-node `agent.env` still
  wins on key collision).
- **Operator-side backup CLI (`aegis-pg-backup` +
  `aegis-pg-restore`)** — #125. Two separate binaries:
  `aegis-pg-backup` is the safe default (list / get /
  create / delete / download), `aegis-pg-restore` is the
  intentional destructive path. The split enforces the
  safety boundary at the process level: an operator who
  types `aegis-pg-backup restore <id>` gets an
  `unknown subcommand` error, not a silent data wipe.
  Both binaries are JSON-to-stdout cron-friendly; the
  restore CLI does a two-step id confirmation before
  the destructive op and supports `--dry-run` for
  eyeball checks via `pg_restore --list`.
- **Operator guide + security policy + quickstart docs**
  — #126 (this PR). `docs/operator-guide.md` (the
  canonical install + daily-ops reference),
  `docs/SECURITY.md` (the threat model + disclosure
  flow + supply-chain trust), `docs/guide/quickstart.md`
  (the 5-minute "fresh VPS to panel running" path).
  The `deploy/secrets/README.md` is the field-by-field
  sops+age workflow; the operator guide links to it.

**Deferred from the original v0.5.0 scope (the
"ничего резать не будем" decision does NOT apply to
items that were not in the original scope):**

- **JSON logs** — the zerolog ConsoleWriter / JSON
  switch was on the v0.5.0 list. The implementation
  is a one-line `AEGIS_ENV=production` toggle in
  `cmd/aegis/main.go` and a test in
  `internal/config/config_test.go`; the
  `internal/log` package's `New(env string)` already
  returns the right writer. **A v0.5.x follow-up;**
  the work is small but the CI round-trip on a one-line
  code change was not worth the cycle time. Operators
  who need JSON logs today can run `docker logs aegis-panel
  | jq` on the ConsoleWriter output (the format is
  already `key=value` with timestamps, not pure freeform).
- **Cosign sign + verify for our Docker images** — the
  v0.5.x follow-up. The release workflow has the
  `metadata-action` step, which is the natural
  integration point for `cosign sign` post-push. Until
  then, the trust model is the same as the OCI
  registry's authentication (TLS + GitHub's OIDC
  token).
- **Smoke test on fresh VM in CI** — out of v0.5.0
  scope. The `bootstrap_node + configure_secrets +
  install_panel` playbooks are tested in
  ansible-lint + the role defaults dry-run; a full
  VM bootstrap test is a v0.5.x follow-up.
- **GPG-verify sing-box** — the v0.5.0 plan called for
  a `gpg --verify` step on the sing-box tarball. SagerNet
  does not publish detached signatures, so the work
  was de-scoped in #123. The GitHub API digest is the
  trust model.

## v0.6.0 — `internal/plans` ✅ shipped

The `plans` table is in migration 0001; the package
exists as a `doc.go` stub (#77). v0.6.0 shipped the
full CRUD surface across the Go backend, the
HTTP layer, the OpenAPI spec, and the admin UI.

Closed by: PR #131 (Go package: Plan model +
ResetPeriod closed enum + Store interface +
MemoryStore + PgStore + Service with input
validation + 23 unit tests + 4 pg integration
tests), PR #132 (admin HTTP handler + ScopePlans
auth + AEGIS_PLANS_BACKEND config + router/main
wiring + 11 e2e handler tests), PR #133 (OpenAPI
spec + hand-mirrored API client + regenerated
types), PR #134 (PlansView.vue + sidebar nav +
i18n en/ru + zod form schema). Tag `v0.6.0`
after the docs PR lands.

What landed:

- `plans.Store` interface (MemoryStore + PgStore)
  backed by the `plans` table from migration 0001.
- `plans.Service` with input validation
  (Name 1..64 chars, Duration [1 minute, 10 years],
  ResetPeriod enum, non-negative numbers) and the
  ID / timestamp generation on Create.
- Admin handler at `plans.AdminRouter(plansSvc, auth)`
  behind `auth.RequireScope(auth.ScopePlans)`.
- Route mount: `r.Mount("/plans", plans.AdminRouter(...))`.
- Wire format: `plans.Plan` JSON DTO. Duration is
  int64 nanoseconds on the wire (the Go side
  stores it as a Postgres INTERVAL via
  `pgtype.Interval` with `Valid: true`; the UI
  formats ns back to a "30d" / "1h" string at the
  rendering layer).
- Frontend: `PlansView.vue` with the list +
  create + edit + delete dialogs + global search.
- Frontend: `plans.*` i18n namespace in en/ru,
  sidebar nav entry, OpenAPI codegen.
- 23 unit tests + 4 pg integration tests in the
  Go package; 11 e2e tests in the handler.

Deferred to v0.6.x:

- `plan_pool` writes (the join table linking
  plans to host pools). v0.6.0 keeps the
  read-only view in `internal/subscription`.
- `plan_pool` UI (no HostPool picker in the plan
  dialog yet).
- Audit log writes from the mutating handler
  (the call-site wiring is a separate batch
  across all admin handlers).
- Zod schema at `frontend/src/schemas/plan.ts`
  (the v0.6.0 view uses inline zod via
  `useZodForm`; a shared schema file lands when
  the UI matures).

## v0.7.0 — `internal/webhooks`

Same shape as plans: the `webhooks` table is in
migration 0001, the `doc.go` stub is in #77. The
v0.7.0 work:

- `webhooks.Store` (MemoryStore + PgStore).
- `webhooks.Service` with delivery (HTTP POST to the
  configured URL) + retry (exponential backoff,
  max 5 retries).
- Admin handler at `webhooks.AdminRouter(webhooksSvc, auth)`.
- Event-emission hook: when an event package
  (post-v0.6.0 / v0.8.0 `events` package) fires, the
  webhooks service delivers to all enabled subscriptions
  matching the event type.
- HMAC signature on the payload (operator-configured
  secret on the webhook row; SHA-256 in the
  `X-Aegis-Signature` header) so the receiver can
  verify the source.

## v1.0.0-mvp-soft-launch

The minimum surface for the public release:

- The v0.4.0-mvp-batched end-to-end flow (panel →
  aegis-agent → sing-box config write → reload) on at
  least one node with at least one user.
- The v0.4.0-d Path C consolidation (this PR plus
  #99, #97, #96, and #95).
- A `docs/operator-guide.md` for the soft launch
  operators.
- A `SECURITY.md` with the disclosure policy and the
  GPG-verify path for sing-box.
- The four empty packages that are NOT in the v1.0
  cut: `cabinet`, `caddy`, `cascades`, `decoy`,
  `events`, `mcp`, `notifications`, `plans` (v0.6.0),
  `stats`, `subscriptions-plural`, `webhooks` (v0.7.0).
  v1.0 ships without them; they land post-v1.0 in
  named v1.x releases.

## Open gaps (post-v0.4.0 audit)

11 packages are `doc.go`-only and un-wired (per the
v0.4.0-d audit). Of these, `plans` and `webhooks` are
on the v0.6.0 / v0.7.0 path; the remaining 9 are
post-v1.0:

- `cabinet` — end-user self-service UI (sub-token
  rotation, traffic stats, plan view). v1.2+ target.
- `caddy` — Caddy reverse-proxy admin API integration.
  Not a v1.0 dependency (Caddy runs out-of-band).
- `cascades` — the existing `BatchedApplier` already
  has cancel/replace semantics; cascades is the
  multi-user delta. v1.1+ target.
- `decoy` — decoy site content storage. v1.0 ships with
  the decoy-site static content as a single tarball
  (no admin UI); the v1.1+ work is a per-decoy CRUD
  surface.
- `events` — internal event bus. v0.7.0 (webhooks)
  has the minimum event-emission hook; the full
  events package lands v1.1+ with the audit log
  shape.
- `mcp` — Model Context Protocol server. v1.2+ target.
  Out of scope for the soft launch.
- `notifications` — outbound notification channels
  (Telegram, email, Discord). v1.2+ target.
- `stats` — per-user traffic stats (the
  `traffic_used_bytes` column is updated by the
  agent on every Apply). v1.1+ target.
- `subscriptions-plural` — the external Squads UI
  (the multi-tenant surface). v1.3+ target.

## Tagging policy

- v0.x.y tags land in `git tag -a v0.x.y -m "..."`
  on `main` after the milestone PR merges.
- The tag message includes the per-PR list (e.g.
  for v0.4.0-d: #95, #96, #97, #99, this PR).
- `CHANGELOG.md` is updated with the per-PR summary
  in the same merge as the tag.
- v1.0.0-mvp-soft-launch is the only tag in the v1.0
  range; v1.0.x patch releases are bugfixes only,
  no new features.
