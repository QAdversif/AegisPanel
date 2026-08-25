# Known Limitations — AegisPanel v0.8.28

This document tracks the gaps between what the latest shipped
milestone delivers and the full design in `ARCHITECTURE.md` §21.
Every open entry points to the milestone that closes it.
**Closed** items are kept for context — the PR that closed each
one is named so future readers can find the diff.

The current state of the project is **v0.8.28** (the
Tier 3 dialog-extraction closeout + Tier 1 #3
backup-cron closeout; PRs #254-#275; release cut at
`4a3c31a`). v0.8.28 ships two release tracks in one
tag. **Tier 3 (PRs #254-#270)**: 5 dialog-extraction
PRs split HostsView and NodesView into 8 self-contained
dialog components under `frontend/src/views/dialogs/`
(HostCreateDialog + HostEditDialog #265, NodeCreateDialog
and NodeEditDialog #266, NodeProvisionDialog #267,
NodeRotateDialog #268, NodeRefreshDialog and
NodeInspectDialog #269); adjacent refactors
`ChangePasswordRequest` dedup (#254), `window.confirm`
→ `ConfirmDialog` migration (#256), typed
`as Parameters<...>` casts replacing `as never` (#263);
two perf wins (`camelizeKeys` memoization #255, new
`GET /api/v1/inbounds` batch endpoint #264); 52 new
vitest tests (39 → 91 total, #270). **Tier 1 #3
(PRs #273-#275)**: backup-cron hardening. PR #273
extends `parseCronField` to the full Vixie construct
set (`N-M`, `N-M/S`, `*/S`, `N,M,K`). PR #274 adds
33 scheduler goroutine tests across 4 new test
functions (`IdempotentWithinMinute`,
`AdvancesLastEvenOnNonMatch`,
`RespectsCancelledContext`,
`TriggersAtScheduledTime`). PR #275 ships the
admin-UI surface — `Service.ReloadCron` + the
read-only `GET /api/v1/backups/schedule` endpoint +
`Backups → Schedule` section in `BackupsView.vue` +
10 i18n keys + OpenAPI schema bump to `0.8.28` +
auto-regenerated `frontend/src/types/api.d.ts`. **Anti-
leak infra hardening (PR #272)**: a `ghp_` /
`github_pat_` regex is added to `BANNED_PATTERNS` in
`tools/scripts/check-sensitive.sh` (and the AGENTS.md
mirror), closing the 2026-08-20 3-PAT incident loop.
**v0.8.27** (the anti-leak infrastructure + `release.yml`
hard-gate smoke + `docs/RUNBOOKS/oncall.md` + recreated
`docs/gap-analysis-v0.8.24.md` batch; PRs #241 / #246 /
\#247 / #249 / #250 / PR #251 / PR #252; release cut at
`6b48879`) is the first release where the anti-leak
infrastructure gates a merge end-to-end (pre-commit +
CI + the agent's banned-list). v0.8.25 still closes the
**9-bug silent-production chain** that started in
v0.8.15 (pg_dump missing in image) and ran through
v0.8.16..v0.8.24 (symlink wrapper, DSN-stripped dump
call, postgres 15 vs 16 mismatch, known_hosts TOFU
unreachable, wire-vs-line fingerprint, ed25519
HostKeyAlgorithms pin, `SHA256:` prefix-strip, state-
write `UpdateInput{}` empty struct, ETXTBSY on direct
binary overwrite). All nine bugs were caught by
post-deploy live smoke tests, never by the
`release.yml` workflow — the v0.9.0 `release.yml`
hard-gate smoke test (PR #247) is the first release
where the smoke is a CI gate, not a manual post-deploy
check. The full gap-vs-roadmap analysis is in
[`docs/gap-analysis-v0.8.24.md`](./docs/gap-analysis-v0.8.24.md);
the TL;DR is: the v0.8.x code is **richer** than the
v9.5 roadmap expected at this point (per-user credentials,
inbound templates, cookie-only auth, Caddy CSP, webhooks
with HMAC + DLQ all shipped ahead of the Phase 1 plan),
but the **operational confidence** (restore-drill on a
fresh VM, backup cron + retention, 24h soak) is still
on the MVP-1.0 checklist, not Phase 2. The single
missing piece for the v1.0.0-mvp-soft-launch tag is
**v0.9.0 — restore-drill on a fresh VM + `release.yml`
hard-gate smoke** (download backup → restore → first-boot
→ panel reachable; new release fails the gate if `pg_dump
--version` doesn't return a real binary).

## v0.8.1 — closed (historical: the v0.8.1 + earlier open items)

The v0.8.1 lead-in was the last release that opened new
items. Every entry in this section is now closed; the
"Closed in this PR" / "shipped in vX.Y.Z" notes in each
subsection name the PR. v0.8.2 through v0.8.14 each
closed a batch of these and added the next milestone's
items (which are also closed by v0.8.14). The only
remaining GA-blocker is the **admin password rotation**
(see "v0.8.14 — currently open" at the bottom of this
file).

### Operations

#### Server-side `/me` fix (auth.Store.GetByID) — v0.8.2

The v0.8.0 release introduced a regression in
`auth.Service.Me()`: the function walks the in-memory
admin map (`lookupByID`) which only works on the
`MemoryStore`. On the `pg` backend, the call falls
through to "lookupByID only supported for MemoryStore
in Phase 0" and returns 500. The UI had a
defensive fallback (#172) that hides the bug
(`canWrite ?? true` when `auth.me === null`), but the
topbar's "Logged in as X" still renders empty, and
the user detail page fails to render the user's
scopes. The fix is a `GetByID(ctx, id)` method on
the `auth.Store` interface, implemented in both
`MemoryStore` (walk the map) and `PgStore`
(`SELECT FROM admins WHERE id = $1`), with
`Service.lookupByID` rewired to use the interface
method. v0.8.2.

#### HTTP admin surface for `user_inbound_credentials` — v0.8.2

The data layer for Phase 2 multi-user is in place
(PR #167 — `internal/credentials` Service + Store +
24 unit tests, `AEGIS_CREDENTIALS_BACKEND` env var,
`a.Credentials` field on `App`). The HTTP admin
handler (`/api/v1/credentials/` mount behind
`auth.RequireScope(ScopeCredentials)`) and the
OpenAPI spec are not yet wired. The admin UI
(Credentials tab in the user detail page) lands
with the HTTP layer. This is a focused PR
(2-file change for the AdminRouter + OpenAPI, a
third file for the UI tab); the rest of Phase 2
multi-user is end-to-end.

### BatchedApplier and re-provision follow-ups (deferred from v0.8.1)

#### BatchedApplier decrypt-and-use path for the stored
panel key — v0.8.3

The Builder fetches the operator's credential from
`inbound.Params` (Phase 1 path) and now optionally
overrides with `user_inbound_credentials` (Phase 2
path, PR #169). Neither path uses the v0.8.1
panel-generated SSH key for the sing-box
`Apply` transport. The applier still uses the
`AgentBearer` from `nodes.agent_bearer` to POST
`/v1/apply` to the agent. The next step is the
BatchedApplier reading `nodes.ssh_private_key_ciphertext`,
decrypting via the envelope, and using the key for
the transport. v0.8.3.

#### Re-provision path for v0.3.0..v0.7.x nodes (CLI "force
rotation") — v0.8.3

A node that was provisioned before v0.8.1 has an
empty `nodes.ssh_private_key_ciphertext` and no
panel key on the agent. Re-provisioning such a node
on v0.8.1 takes the "operator-supplied key" path
(the operator pastes their existing PEM). A future
CLI command (`aegis admin node rotate-panel-key <id>`)
and matching admin UI button would generate a fresh
panel key for an existing node (uses the operator's
current auth to bootstrap, then rotates to the new
key). v0.8.3.

### UX follow-ups (deferred from v0.8.1)

#### Host → node mapping in the Builder-side filter — closed in this PR

Closed in this PR (the host→node mapping v0.8.x
work). The lookup lives in `internal/hosts.Service`:

- `HostsForInbound(ctx, nodeID, inboundID)
    (*uuid.UUID, error)` — the (node, inbound)
    pair's owning host id (or nil if no host
    references the pair). Used by
    `internal/cores/builder.BuildCoreConfigForNode`
    to populate `InboundSpec.HostID` (previously
    always `""`, see `builder.go:32-41`).
- `NodesForHost(ctx, hostID) ([]uuid.UUID,
    error)` — every distinct node id the host
    references. Used by
    `internal/users.Service.expandHostsToNodes` to
    fan `User.HostsAllowlist` / `HostsBlocklist`
    (host IDs, per the architecture) into node IDs
    the BatchedApplier fan-out matches.

The user-level filter on the BatchedApplier side
was, in v0.7.x, a **misimplementation**: the field
was named `HostsAllowlist` but the fan-out code
treated the UUIDs as node IDs. v0.8.x fixes the
semantic: the field stores host IDs (per the
architecture; see `docs/comparison/remnawave.md:
118-119` and the original TODO at `builder.go:
32-41`); the fan-out expands them via
`NodesForHost`. A nil `s.hosts` with a non-empty
field is **fail-closed** (no fan-out + warning
log) — the alternative (fail-open to "all nodes")
would silently grant access on a misconfigured
v0.8.x install.

The per-credential Builder-side filter (the
"Builder does not filter credentials by
`user.HostsAllowlist`" half of the original TODO)
is a follow-up — it requires a per-user context
in the FlushFn, which the BatchedApplier does not
carry today. The v0.8.x work here is the
prerequisite lookup; the consumer-side filter is
a separate PR. Outbound group rendering
(`docs/comparison/remnawave.md:319`) is gated on
user demand and is a separate PR.

#### Per-user credential filter in the Builder — closed in this PR

Closed in this PR (v0.8.10+). The per-user
context the v0.7.x TODO said the FlushFn did not
carry turned out to be unnecessary: the Builder
already has the `nodeID` (it's the FlushFn's
per-node key), so the read direction is "which
user IDs the host-allow/block filter admits for
this node", not "which node IDs the user is on".
The new method is the reverse-direction read of
the v0.8.x `enqueueUserDelta` fan-out, sharing
the same `expandHostsToNodes` helper.

- `internal/users.Service.AllowedUsersForNode(ctx,
  nodeID) ([]uuid.UUID, error)` — the new
  method. Uses `StatusActive` as the user-status
  filter, blocklist-wins-over-allowlist, fail-closed
  on a nil `s.hosts` with a non-empty allow/block
  field (matching `enqueueUserDelta`).
- `internal/cores/builder.ListUsersAllowedForNode`
  interface + `BuildCoreConfigForNode` per-inbound
  filter — one DB round-trip per build (per node
  flush), not per inbound. A nil source / nil
  result / lookup error keeps the v0.8.0-v0.8.9
  default-allow contract (every credential
  passes). A non-empty result is the allow-set.
  An empty result (the lookup succeeded with [])
  drops every credential for that node — the
  fail-closed "no users allowed" semantic.
- `internal/cores/builder.NewFlushFn` — new
  `usersSrc` argument between `credSrc` and
  `renderer`. Wired in `cmd/aegis/main.go` to
  pass `a.Users`.

**Migration note for operators**: no schema
migration, no new env vars, no new
`AEGIS_*_BACKEND` config. The per-user filter
activates automatically on the next BatchedApplier
flush once `a.Users` is wired. Operators who
have populated `User.HostsAllowlist` /
`HostsBlocklist` with host IDs see the filter
take effect immediately; operators who have not
populated the fields see no behavioral change
(the v0.8.0-v0.8.9 default-allow contract is
preserved when every user has empty allow/block
lists).

**Migration note for operators**: the
`User.HostsAllowlist` / `HostsBlocklist` UUIDs
are now host IDs, not node IDs. A panel
upgrading from v0.7.x to v0.8.x where these
fields held node IDs will see an empty fan-out
for affected users until the values are
re-populated with host IDs. The `expandHostsToNodes`
helper's warning log (`user fan-out is empty
(fail-closed)`) is the operational signal.

#### Inbound-templates work — partially shipped in v0.8.13+ (PR #205 foundation)

The v0.8.x-bucket "inbound templates" feature —
named, reusable `Params` defaults that any number
of `inbounds` rows can reference via the new
nullable FK `inbounds.template_id` — is now
partially shipped.

**Foundation (PR #205, post-v0.8.12) shipped:**

- Migration `0021_inbound_templates.sql` adds
  the `inbound_templates` table +
  `inbounds.template_id` nullable FK +
  `inbound_templates_protocol_idx` +
  `inbounds_template_id_idx`. Backwards
  compatible (every existing inbound has
  `template_id = NULL` and continues to use its
  inline `params`).
- New `internal/inboundtemplates/` package
  (model + validate + store + pg_store and service
  and handler, with 8 unit and 4 pg integration
  tests). The handler is mounted at
  `/api/v1/inbound-templates` with 5 paths.
- 3 new webhook event types
  (`inbound_template.{created, updated,
  deleted}`) added to `AllowedEventTypes`.
- Inbounds model + `CreateInput` + `UpdateInput`
  and the JSON request shapes gain
  `TemplateID *uuid.UUID` (stored verbatim, no
  validation yet).
- `app.go` + `router.go` wire the new service
  into the App + the
  `/api/v1/inbound-templates` mount + the
  `ScopeNodes` guard. No new env vars.

**Follow-up PRs pending:**

- The sing-box renderer's
  `BuildCoreConfigForNode` reading
  `template.params` when `inbound.template_id`
  is set — the actual feature. Until this lands,
  the new `template_id` column is stored but
  the rendered config still uses the inline
  `inbound.params` (the v0.8.0-v0.8.12 path).
  This is a `internal/cores/builder` change
  that follows the v0.8.10 per-user credential
  filter pattern (one DB round-trip per flush,
  not per inbound).
- The inbounds service validation that the
  template's protocol must match the
  inbound's. The FK is nullable + the DB CHECK
  on `protocol` is per-row; the cross-table
  invariant must be enforced at the
  `inbounds.Service` boundary.
- The frontend UI: a new `InboundTemplatesView`
  page (list + create + edit + delete) + a
  "Template" dropdown in `InboundsView`'s
  create/edit form. The openapi.yaml + codegen
  refresh lands in the same PR.

**Design rationale (kept for the follow-up PRs):**

The `Params` JSONB column on `inbounds` is the
sing-box provider's per-listener configuration
(Reality keys, UUIDs, passwords, …). The
templates layer factors that out so the
operator does not paste the same JSON into
every inbound. The per-user credential from
`user_inbound_credentials` is still layered on
top in the multi-user render — the template
is the "shared protocol config", the per-user
row is the "per-user auth credential". The
renderer's look-up replaces the template's
`uuid` / `password` keys with the per-user
value; the rest of the params flow through
unchanged.

#### "Show me the stored public key" debug surface — v0.8.x

The "Stored panel key" radio option in the
provision form is opaque — the operator clicks
submit, the panel re-uses its own key. There is
no "what is the panel's key on this node right now"
debug view. A small SHA-256 fingerprint display
on the node row (the public key is safe to show;
the private key never leaves the panel) would help
operators verify that the node has the right key
after a manual `ssh` rotation. v0.8.x.

#### Merged "Add node + Provision" dialog — closed in this PR

Closed in this PR (v0.8.12). The Create dialog
now carries a "Provision this node after
registering" checkbox (default on) and reveals
the auth-method radio + key / password /
ssh_user / ssh_port / tofu_policy /
expected_fingerprint fields when checked. The
submit handler calls `createNode` then
optionally `provisionNode` in sequence. The
per-row "Provision" dropdown entry stays for
re-provisioning offline nodes (it keeps the
three-way radio including the "Stored panel key"
option, which is disabled for first-time
installs because the panel has no key on file
yet for a `new` node). The two existing API
endpoints (`POST /api/v1/nodes` and
`POST /api/v1/nodes/{id}/provision`) are
unchanged; the merged dialog is a UX-layer
composition. Operators who preferred the
v0.8.11 two-step flow can uncheck the
"Provision after registering" checkbox.

#### shadcn-vue RadioGroup primitive — closed in v0.8.12+

Closed in v0.8.12+ (PR #202). The two hand-rolled
radio groups in `NodesView.vue` (the three-way
auth-method picker in the per-row provision dialog,
plus the two-way picker in the merged "Add node +
Provision" dialog) are now rendered through new
shadcn-vue primitives: `components/ui/RadioGroup.vue`
(thin wrapper over `radix-vue`'s `RadioGroupRoot`,
forwards `modelValue` + `defaultValue` + `disabled` +
`required` + `orientation` + `dir` + `loop` + `name` +
`class`, emits `update:modelValue`) and
`components/ui/RadioGroupItem.vue` (wrapper over
`RadioGroupItem` and `RadioGroupIndicator`; renders
a `<button role="radio">` with a 16x16 circular border
plus a 10x10 inner dot; default slot is the label).
The new primitives inherit arrow-key + Space keyboard
navigation, ARIA `role="radiogroup"` / `role="radio"` /
`aria-checked` / `aria-disabled` semantics, and
`data-[state=checked]` + `data-[disabled]` styling hooks
for free. The previously hand-rolled
`.nodes__auth-radios*` CSS block (~46 lines) is
deleted; visual parity preserved via the standard
`bg-muted` + `border-ring` data-attribute pattern.

### Operations polish (deferred from v0.5.0 / v0.7.0)

#### Pre-existing `vue/max-attributes-per-line` template
warnings — chore

The v0.7.0 view templates (WebhooksView, PlansView, a
handful of dialogs) carry pre-existing eslint warnings for
`vue/max-attributes-per-line` and
`vue/singleline-html-element-content-newline`. They're
auto-fixable with `eslint . --fix`; not in scope for any
release PR. v0.7.0-legacy chore.

#### Cosign re-signing on every release — closed in v0.8.9

Closed in v0.8.9. After the existing single sign
step, `release.yml` waits 30s (let GHCR settle)
and then re-signs each image + runs `cosign verify`
with the same OIDC flags a consumer would use
(`--certificate-identity-regexp "https://github.com/QAdversif/AegisPanel/.*"`,
`--certificate-oidc-issuer https://token.actions.githubusercontent.com`).
Three concrete failure modes covered (v0.8.8
evidence, PR #189): (1) tag-mutation drift on
`latest` between sign and pull; (2) sign-step
OIDC flake recovery without full workflow_dispatch
- rebuild; (3) explicit `cosign verify` audit
trail in workflow log so a successful `sign` exit
0 is no longer a "we hope this works" claim.

#### `docs/RUNBOOKS/deploy.md` §6 sops+age workflow was
misleading — closed in this PR

The v0.8.6+ hard guard on memory backends makes
sops+age a precondition for production deploys,
but the runbook had three concrete gaps that bit
the v0.8.0 → v0.8.9 production deploy on
2026-08-09 (90-min recovery, JWT secret
regenerated out-of-band):

1. **Decrypt-on-container claim was wrong.** §6.3
   ended with "the panel binary reads sops-decrypted
   env at boot" — no `cmd/aegis/main.go` code path
   does this in v0.8.x. The actual workflow is
   decrypt-on-operator (operator runs `sops -d`
   locally, parses the env, builds `docker run
   -e KEY=VALUE` flags, ships them over SSH). The
   runbook now spells this out explicitly with
   the `SOPS_AGE_KEY_FILE=… sops --config … -d`
   command + a worked `python` env-flag builder.
2. **No distroless UID ownership gotcha.** The
   panel container runs as the distroless `nonroot`
   user (UID **65532**). The age key on the host
   was originally 0600 root, which 65532 cannot
   read. The panel boot-looped on:
   > fatal: webhooks: failed to build age secret
   > cipher: envelope: read identity file
   > "/etc/aegis/age.key": open
   > /etc/aegis/age.key: permission denied
   The fix: `sudo chown 65532:65532
   /etc/aegis/age.key && sudo chmod 0640
   /etc/aegis/age.key`. The runbook now requires
   this step in §6.2.
3. **No canonical env file shape.** §6.3 had YAML
   examples with `AEGIS_WEBHOOKS_SECRET_KEY_FILE`
   and `AEGIS_WEBHOOKS_CREDENTIALS_*` env vars
   that don't exist in `internal/config/config.go`.
   The actual shape is the dotenv-style
   `AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE` +
   `AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS`
   (the only envelope surface; shared with
   `nodes.stored-key`, bootstrap, and the
   `admin_node rotate-panel-key` CLI). The
   runbook now lists all 11 `AEGIS_*_BACKEND`
   vars + the 2 envelope vars + the canonical
   non-secret env shape.

A future v0.8.x PR could plumb sops-decrypt
into the panel binary's `cmd/aegis/main.go`
so the server-side `docker run` only needs
`-e AEGIS_ENV_FILE=… -v …:/etc/aegis/aegis-env.enc.env:ro`
- the age key mount, with no plaintext-env
on the operator. The runbook §6.6 documents
this as the future direction.

#### Subscription URL display in UsersView — closed in this PR

Closed in this PR. The v0.8.x operator UX gap
("the admin has no way to get a user's
subscription URL out of the panel UI without
manually concatenating
`https://<host>/<sub_path>/sub/<token>`") was
an oversight from the v0.1.0 SubscriptionView
work (the diagnostic page only accepts a raw
sub_token and never exposes the constructed
URL). A dead `fetchSubscriptionForUser` helper
in `frontend/src/api/services/subscription.ts`
called a non-existent
`GET /api/v1/users/{id}/sub` endpoint; the
helper has been removed.

The fix:

- **New DropdownMenu item in `UsersView.vue`**
  ("Show subscription URL" / "Показать ссылку
  подписки") on each user row. Opens a dialog
  with the full URL (read-only textarea), a
  **Copy URL** button, an **Open** button (new
  tab, `noopener`), and a format selector +
  **Preview** button that renders the
  sing-box / clash / base64 / HTML payload
  via the existing `GET /api/v1/sub/{token}`
  endpoint.
- **URL construction** is pure-frontend:
  `${window.location.origin}${sub_path_prefix}/sub/${user.subToken}`.
  The active `sub_path` is read from
  `GET /api/v1/panelcfg/` on every dialog open
  (so a recent rotation is picked up without a
  page reload) and a **Refresh** button
  re-runs the lookup without closing the
  dialog.
- **Existing post-create / post-rotate modal
  extended**: the dialog now also shows the
  full URL (read-only textarea) + a new
  **Copy URL** button alongside the existing
  **Copy** (raw token) button. The raw token
  stays primary because of the v0.1.0
  "shown only once" contract.
- **No backend change**. No `openapi.yaml`
  bump. The two endpoints touched
  (`/api/v1/sub/{token}`, `/api/v1/panelcfg/`)
  are both pre-existing and stable.

#### Smoke test on fresh VM in CI — v0.9.0

`tools/scripts/smoke-local.sh` (PR #152) covers the
local docker-compose path; a terraform + ansible +
boot-log CI job is a separate work unit. v0.9.0.

### Out of scope (post-v1.0)

These items are tracked in `docs/ROADMAP.md` and
`docs/README.md` for context. None block v0.8.0 or
the v1.0.0-mvp-soft-launch.

- **JSON logs in production** — closed in v0.8.6
  (config-level guard for the `AEGIS_ENV=development`
  with a pg backend, the silent-misconfig shape; see
  `Config.validate()` and `usesAnyPgBackend()` in
  `backend/internal/config/config.go` and the
  `config_test.go` 8-function / 18-subtest suite).
  The obs-package wiring itself has been in place
  since v0.5.0-era; the v0.8.6 PR is the guard
  that converts the silent-misconfig failure mode
  into a loud boot-time error.
- **Cosign re-signing on every release** — v0.7.0
  closed the initial sign + verify pair; the
  post-v0.7.0 workflow contract (PRs 102/103/104/111)
  does not yet include cosign re-signing on every
  release. v0.8.x.
- **Smoke test on fresh VM in CI** — v0.9.0
  candidate. `tools/scripts/smoke-local.sh` (PR #152)
  covers the local docker-compose path; a
  terraform + ansible + boot-log CI job is a
  separate work unit. v0.9.0.
- **`internal/cabinet` end-user surface** —
  doc.go-only. The per-user sub URL is the
  per-user cabinet for v0.8.0. A separate
  end-user-facing cabinet (login UI, sub URL fetch,
  traffic stats, plan change) is v1.2+.

## v0.8.0 — closed in v0.8.0

| Item | Closed by |
| --- | --- |
| Audit log call-site wiring. Every mutation in the admin handler stack now writes an `audit_log` row. `audits.RecordFromContext(ctx, svc, e)` Service-layer mirror of the existing `RecordFromRequest`; pulls actor from `auth.ClaimsFromContext`; IP/UA blank. Six services: `users`, `plans`, `nodes`, `hosts`, `inbounds`, `backups`. Pre-fetch for audit `Before` on `users.Service.Delete` + `plans.Service.Delete` (extra round-trip; same trade-off as the credentials pre-fetch). 6 new test files (~20 tests). | PR #166 |
| Phase 2 multi-user sing-box render — data model. `user_inbound_credentials` table (migration 0019): `id UUID PK, user_id FK→users ON DELETE CASCADE, inbound_id FK→inbounds ON DELETE CASCADE, credential_value TEXT NOT NULL, created_at, updated_at, UNIQUE (user_id, inbound_id)` + 2 indexes. `internal/credentials` package: `Credential` struct, `Store` interface, `MemoryStore` (Phase 0), `PgStore` (SQLSTATE 23505 → `ErrDuplicate`), `Service` with `Create/Get/ListByUser/ListByInbound/Rotate/Delete` + `WithAudits` setter, all mutating methods call `audits.RecordFromContext` with `credential.create` / `credential.rotate` / `credential.delete` actions. Wired into `internal/app` (`a.Credentials` field, `AEGIS_CREDENTIALS_BACKEND` env). 24 unit tests. | PR #167 |
| Phase 2 multi-user sing-box render — renderer. `renderVLESS` / `renderHY2` / `renderTrojan` take a per-(user, inbound) credential list. When non-empty, the renderer emits a `users: [{name, uuid or password}, ...]` array of length N. When empty, the renderer falls back to `params["uuid"]` / `["password"]` and emits a length-1 array. `renderShadowsocks` unchanged (single-password protocol by design). New `ExperimentalInboundCredentialsKey` constant + `extractCredentialsByTag` helper (defensive: missing key, wrong-typed value, wrong-typed per-tag entry all fall through to the Phase 1 path). 5 new tests + 28 existing tests unchanged. | PR #168 |
| Phase 2 multi-user sing-box render — builder wiring + BatchedApplier narrow. The Builder's `ListCredentialsByInbound` source interface + `BuildCoreConfigForNode` populates `cfg.Experimental["inbound_credentials"]` for every enabled inbound. Per-inbound query failures are fail-soft (log + Phase 1 fallback). `users.Service.enqueueUserDelta(d, user)` filters the BatchedApplier map by `user.HostsAllowlist` and `user.HostsBlocklist`. Blocklist wins over allowlist. Empty allowlist + empty blocklist = default allow (v0.5.0 behaviour). 4 call sites updated. New `BatchedApplier.QueueLen()` method (enqueue-pressure metric, also used by the new tests). 4 new builder tests + 5 new fan-out tests. | PR #169 |
| Phase 2 multi-user sing-box render — subscription. The per-user sub URL is the per-user cabinet. `subscription.Service` gains `creds *credentials.Service` + per-render `userCreds map[inboundID]credentials.Credential` cache. `WithCreds(svc)` setter (nil-safe). `precomputeUserCreds(ctx, u)` does ONE `ListByUser` call per render (not one per inbound). `RenderSingbox` and `RenderClash` thread the per-endpoint `userCred` into the per-protocol builders. Each builder uses `userCred` when non-empty, falls back to `params` when empty. 4 new tests including the auth-boundary `TestRenderSingbox_Phase2_OtherUserCredNotLeaked`. | PR #170 |
| Frontend dependency batch — TS / CSS / axios / vue-tsconfig / postcss. `@types/node` 22.12.0 → 26.1.2; `@vue/tsconfig` 0.7.0 → 0.9.1; `typescript` 5.6.3 → 5.8.3; `prettier` 3.4.1 → 3.9.6; `globals` 17.7.0 → 17.8.0; `autoprefixer` 10.4.27 → 10.5.4; `postcss` 8.5.19 → 8.5.25; `sass` 1.101.0 → 1.102.0; `axios` 1.18.1 → 1.19.0 (CVE-2026 GHSA-hmw2-7cc7-3qxx). 2 latent type errors in `PlansView.vue` fixed (the `noUncheckedIndexedAccess` strictness tightened by the `@vue/tsconfig` 0.8.x bump). | PR #159, #161, #163, #165 |

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
| JSON logs in production via `AEGIS_ENV=production` (post-`v0.5.0` follow-up) | PR #128 / closed in v0.8.6 (#187) |

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

## v0.8.14 — closed in v0.8.14+ (PR follow-ups)

### Admin password rotation — closed 2026-08-10 22:38 MSK

The v0.8.x fixture-admin-password (see deploy.local.md) rotation was
performed on the prod panel (the live server.click). The new
password is a 28-char `secrets.choice`-generated
value over a 70-char alphabet (letters + digits +
`!@#$%^&*-_+=`); recorded ONLY in
`~/.aegis/deploy.local.md` (outside the repo, per
the operator's privacy rule). Verified via the
public API: `POST /api/v1/auth/login` returns 200
with the new password and 401 with the old
fixture. The rotation script is reusable
(`C:\Users\adversif\.aegis\scripts\admin-passwd-rotate.py`)
and uses the `aegis admin passwd admin` subcommand
with the bufio.Reader 4096B-drain workaround
(`subprocess.Popen` + `time.sleep(1.0)` between
writes). The `age` envelope on the panel env file
is unaffected (the JWT signing key is independent
of the admin password). Existing bearer tokens
keep working until the 15-min TTL expires;
existing refresh tokens keep working until the
30-day idle TTL expires (or a chain-revocation
event). **v1.0.0 GA is unblocked.**

### Dialog content overflow on content-heavy dialogs — closed in this PR

The merged "Add node + Provision" dialog (PR #201)
and the Inbounds / Hosts / Webhooks dialogs (which
override the base `max-w-lg` to `max-w-2xl` /
`max-w-3xl` / `max-w-4xl`) carry a content stack
that exceeds the viewport height on a 1280×720
laptop. The base `DialogContent.vue` (shadcn-vue
wrapper over `radix-vue`) is fixed at
`max-w-lg translate-x-[-50%] translate-y-[-50%]`
with NO `max-h-` and NO `overflow-y-` — so the
content clips below the viewport and the operator
has no way to reach the submit button. The fix:
add `max-h-[90vh] overflow-y-auto` to the base
class. `overflow-y-auto` (not `scroll`) only
shows the scrollbar when content actually
overflows, so small dialogs (ProfileView confirm,
credential delete, etc.) keep the default
borderless look. The X close button stays
anchored in the top-right because it is
`absolute` relative to the same panel; the
scroll viewport is the panel itself, not the
consumer's `<slot />`. No consumer override was
broken (the per-dialog `max-w-2xl` /
`max-w-3xl` / `max-w-4xl` overrides are
additive on the same Tailwind class — last-wins
semantics for the width, additive for the new
max-h / overflow).

## v0.8.16..v0.8.25 — the silent-bug chain (closed)

The v0.8.16..v0.8.25 release cycle was dominated by a
chain of **9 silent production bugs** in the panel's
backup + provision code path. Every one was caught by
a post-deploy live-smoke test on the live server,
**never by the `release.yml` workflow**. The single
most-important v0.9.0 follow-up is a `release.yml`
hard-gate smoke test (a `pg_dump --version` + a
tiny backup against the freshly-built image) that
would have caught every one of them before publish.

### v0.8.15 — multi-stage Dockerfile for `pg_dump` + `aegis-agent` (PR #222)

Fixed in v0.8.15 (PR #222). The v0.8.14 panel
image was built on
`gcr.io/distroless/static-debian12:nonroot`
which has no dynamic linker; `pg_dump` exec
failed with `ENOENT` on the loader
(`/lib64/ld-linux-x86-64.so.2`). v0.8.15 adds
a `debian:12-slim` tooling stage that runs
`apt-get install postgresql-client`, switches
the runtime base to `distroless/base` (which
ships glibc + the linker + busybox), and copies
`pg_dump` + the whole `/usr/lib/x86_64-linux-gnu`
tree into the runtime stage. Image size grows
by ~50 MB. `POST /api/v1/backups/` now returns
a row with `status="running"` (transitions to
`"ok"` once the dump completes). The same PR
adds a second `go build` for `./cmd/aegis-agent`
in the same build stage, copies the resulting
binary to `/app/bin/aegis-agent`, and sets
`AEGIS_AGENT_BINARY=/app/bin/aegis-agent` as
the runtime default (the v0.8.14 default
`./bin/aegis-agent` 502'd the first install step).
The same PR adds a `log.Error().Int("status",...).Str("error",...).Msg(...)`
line to `internal/bootstrap/handler.go` so every
4xx/5xx from this package lands in the structured
log stream with the `(status, msg)` pair.

### v0.8.16 — `postgresql-client-15` + `joinHostPort` (PR #224)

Fixed in v0.8.16 (PR #224). v0.8.15's
`apt-get install postgresql-client` in the
tooling stage still produced a `/usr/bin/pg_dump`
that was a **symlink to `pg_wrapper`**, a
shell script. The runtime base
`distroless/base-debian12:nonroot` has **no
shell** (`/bin/sh` is not in the image), so
`pg_dump` exited 1 silently on every call.
v0.8.16 installs the real `postgresql-client-15`
package in the runtime stage (not just the
tooling stage) and fixes `joinHostPort` to
handle the `host:22:22` (double-colon) parse
path that bit the early Live demo debug.

### v0.8.17 — replace symlink with real binary (PR #226)

Fixed in v0.8.17 (PR #226). v0.8.16 still
left a symlink at `/usr/bin/pg_dump → pg_wrapper`
in the runtime image; v0.8.17 explicitly
removes the symlink and copies the real
`/usr/lib/postgresql/15/bin/pg_dump` to
`/usr/bin/pg_dump` in the tooling stage. The
resulting image has a working `pg_dump` that
exits 0 on `--version` and produces a real SQL
dump on a server connection. Combined with
v0.8.18 (the next fix) this is when
`POST /api/v1/backups/` first returned a row
with `status="ok"` and a non-empty `size_bytes`.

### v0.8.18 — `Dumper` / `Restorer` interfaces (PR #228)

Fixed in v0.8.18 (PR #228). Architectural
refactor: the single `dumpFn func(ctx) (io.ReadCloser, error)`
callback is split into consumer-side `Dumper`
and `Restorer` interfaces; the service holds
the full DSN in `Config` and delegates extract
to injected `Dumper` / `Restorer`. Production
wiring installs `pgBinaries`. The PR also fixes
3 silent bugs:

- **Full DSN was stripped to bare db name** by
  the v0.8.17 DSN helper (`dsnDatabase(dbName string)
  string`). The panel was connecting to
  `postgres://localhost:5432/<dbname>` (no
  user, no password) which the postgres server
  rejected with `FATAL: no PostgreSQL user name
  specified in startup packet`. The user-facing
  symptom was 23-byte empty-dump files
  masquerading as "successful" backups.
- **pg_dump subprocess exit code was discarded**
  by `closeQuiet(src io.ReadCloser) error` — a
  best-effort `Close()` whose return value was
  always ignored. A failed pg_dump (any non-zero
  exit) was indistinguishable from a successful
  one at the row-write level.
- **`pgDumpReader.Close()` now returns the
  subprocess exit code** (not best-effort close).
  A failed pg_dump now surfaces as `status="failed"`
  in the API and the partial file is removed by
  named-return deferred cleanup.

`pgDumpArgs(dsn string) (args []string, pgpw string, err error)`
is a pure function (table-tested) that builds
the argv + extracts the password to a `PGPASSWORD`
env var. **PGPASSWORD env, NEVER argv** (defends
against `/proc/<pid>/cmdline` leak).

### v0.8.19 — pg_dump 15 → 16 via PGDG (PR #229)

Fixed in v0.8.19 (PR #229). v0.8.18 fixed the
silent-fail mode but the binary was still pg_dump
15 against a postgres-16 server, which fails
with `pg_dump: error: server version: 16.x;
pg_dump version: 15.x` (server-major-version
mismatch). v0.8.19 adds the PGDG GPG key +
`apt.postgresql.org` repo in the tooling stage +
`apt-get install postgresql-client-16` + copies
the real `/usr/lib/postgresql/16/bin/pg_dump`
into the runtime image. The PGDG trust anchor
(apt list + GPG key) is removed at the end of
the `RUN` (no third-party repos in the runtime
image long-term). Live smoke on the first v0.8.19
deploy: backup row `status="ok"`, `size_bytes=21982`
(real 21KB dump, not 23 bytes).

### v0.8.20 — TOFU policy reachable (PR #230)

Fixed in v0.8.20 (PR #230). Pre-PR the
`bootstrap.hostKeyCallback` early-returned the
strict `knownhosts.New` whenever the
`known_hosts` file existed (even an empty one),
which short-circuited the TOFU policy entirely.
The result was that on a fresh install — where
`/var/lib/aegis/known_hosts` is created as a 0-byte
file by the volume mount — the very first
provision call hit `knownhosts: key is unknown`
with no fallback. PR #230 lifts the TOFU logic
to be the single source of truth: the
`known_hosts` file is consulted **INSIDE** the
`TofuAcceptAndAppend` callback (and inside
`TofuReject`), never as an early exit. 3
regression tests: empty-file TOFU accepts,
known-key accepts silently, mismatch rejects
with `ErrHostKeyMismatch`.

### v0.8.21 — fingerprint from binary wire format (PR #231)

Fixed in v0.8.21 (PR #231). Pre-PR the panel
used Go's `ssh.FingerprintSHA256` which (in
modern Go) actually hashes the **authorized_keys
line format** (`"ssh-ed25519 AAAA…\n"`), not
the binary wire format. The operator's
OpenSSH-generated fingerprint pin (computed
by `ssh-keygen -lf` on the wire format) therefore
mismatched the actual key. v0.8.21 adds a new
`sshFingerprintWire(key) string` helper that
SHA-256s `key.Marshal()` + strips trailing `=`
from the base64, matching `ssh-keygen -lf`
byte-for-byte. The test fixture uses a real
Demo-нода public-key blob.

### v0.8.22 — pin `HostKeyAlgorithms` to ed25519 (PR #232)

Fixed in v0.8.22 (PR #232). Pre-PR the panel
accepted any of `{rsa, ecdsa, ed25519}` during
SSH kex; the server's `kexinit` preferred
ECDSA, the operator's ed25519 pin was rejected
as "mismatch". v0.8.22 pins the algorithm to
`[]string{ssh.KeyAlgoED25519}` in
`ssh.ClientConfig.HostKeyAlgorithms`. v0.9.0
candidate: parse the algorithm from the expected
fingerprint (32-byte ed25519 vs 64-byte P-256
vs 256-byte RSA-2048) and pin the algorithm list
accordingly. Until then the operator is expected
to pin an ed25519 fingerprint.

### v0.8.23 — `stripFingerprintPrefix` (PR #233)

Fixed in v0.8.23 (PR #233). Pre-PR the compare
was literal: `pCnGi…` ≠ `SHA256:pCnGi…` (the
actual key matched the un-prefixed side, the
operator's pin had the `SHA256:` prefix).
v0.8.23 adds a `stripFingerprintPrefix(fp string) string`
helper (case-insensitive via `strings.ToUpper` +
`HasPrefix`) and routes `fingerprintEqual(a, b)`
through it. 5 table-driven test cases:
case-insensitive, different base64, mixed
prefix, MD5 prefix, unknown prefix (passes
through → surfaces as real mismatch).

### v0.8.24 — `BootstrapNodeProvider.Update` propagates State (PR #234)

Fixed in v0.8.24 (PR #234). Pre-PR the method
mutated `current.State` locally then called
`a.Svc.Update(ctx, current.ID, UpdateInput{})`
with an empty struct. `UpdateInput` is a
pointer-field struct where nil pointers mean
"leave alone", so the underlying service
update wrote nothing — and the operator's UI /
state machine silently disagreed with the
provision response. v0.8.24 passes the new
state via `UpdateInput{State: &newState}`.
One real-line code change + a comment block
documenting the pre-PR-#234 bug.

### v0.8.25 — `Client.UploadAndSwap` for ETXTBSY-safe binary replacement (PR #235)

Fixed in v0.8.25 (PR #235). Pre-PR the SFTP
step did a direct overwrite of
`/usr/local/bin/aegis-agent`. On a **re-provision
of an already-running node**, Linux returned
`ETXTBSY` (text-file-busy) — the kernel refuses
to let one process unlink or truncate a binary
currently mmap'd for execution by another
process. The sftp-server (running as the SSH
user) gets the error and surfaces it as
`SSH_FX_FAILURE`, which the panel mapped to
HTTP 502 with the error `sftp: "Failure"
(SH5_FX_FAILURE)`. v0.8.25 splits the upload
into SFTP-to-temp (`.aegis-agent.swap.<8-hex>`
in the same directory, 4 random bytes from
`crypto/rand` for the suffix, 32 bits of
entropy) + `mv -f` over the target via SSH.
`rename(2)` is always permitted even when the
target is being executed — the running process
keeps the unlinked inode alive until it exits,
and a new process (or the systemd
`Restart=always` loop) picks up the new binary
at the same path. Mock seam in
`installer_test.go` records `uploadSwapPaths`
separately from `uploadPaths`; the
`TestInstaller_SuccessPath` assertion
specifically checks that the agent-binary
path uses `UploadAndSwap` (regression guard
against a future patch that swaps back to
`Upload`). End-to-end verified on the live
server: v0.8.25 deployed, Demo-нода
re-provisioned without the `systemctl stop`
workaround that v0.8.24 needed.

### `scopesForRole("super-admin")` was missing `ScopeBackups` — closed in PR #221

Fixed in PR #221. The admin user now has the
`backups` scope (12 scopes total: admin, read,
write, nodes, users, subscriptions, hosts, plans,
webhooks, audits, credentials, **backups**). The
operator must re-login once to get a new access
token with the `backups` scope.

### `SelectItem value=""` in InboundsView — closed in PR #221

Fixed in PR #221. radix-vue's `SelectItem` does
not allow an empty-string `value` prop (the
runtime throws `A <SelectItem /> must have a value
prop that is not an empty string` at setup). The
"no template" option in the inbound Create + Edit
dialogs now uses the form-level sentinel `"none"`
end-to-end (zod default, createForm initialValues,
editForm blankEditValues, inboundToRow prefill).
The submit guards
(`if (values.templateId !== 'none')`) keep the
wire format clean — the `templateId` field is
still omitted from the payload when "no template"
is selected.

## v0.8.25 — currently open

### 401 on `POST /api/v1/nodes/{id}/provision` from stale in-memory session (under investigation)

The user-reported symptom (a 401 in the browser
console after clicking "Установить агента" in the
merged Add+Provision dialog) is reproducible only
from a stale in-memory Pinia session. The
backend's auth chain is verified clean:

- `POST /api/v1/auth/login` with a fresh login
  returns 200 + a valid `aegis_rt` cookie.
- `GET /api/v1/nodes/` with the access token
  returns 200 (the nodes list, including the
  newly-created Demo node).
- `POST /api/v1/nodes/{id}/provision` with the
  same access token returns 200 (install
  attempted) or 400 (body validation, e.g. "exactly
  one of ssh_private_key or ssh_password is
  required") or 502 ("install failed at stage
  `input`"), depending on the SSH handshake —
  NEVER 401. `ScopeNodes` is correctly granted
  to the admin role.
- `POST /api/v1/nodes/{id}/provision` with a
  malformed token returns 401 "invalid token".
- `POST /api/v1/nodes/{id}/provision` with no
  Authorization header returns 401 "missing
  bearer token".

The 401 in the browser is therefore the
"missing bearer token" branch — the access token
is empty in the request interceptor at the
moment the user clicks submit. The most likely
root cause is an expired access token (15-min
TTL) on a tab the user kept open, with a
refresh-cookie round-trip that did not complete
in time (the `useAuthStore().clear()` fallback
in the response interceptor fires when
`refreshTokens()` returns `null`). The user
mitigation is: re-log-in once, the cookie round-
trip restarts, subsequent requests succeed.
The follow-up PR will harden the response
interceptor to surface a "session expired,
re-login" toast instead of a generic 401, and
will add a v0.8.x integration test that
reproduces the stale-session 401 + auto-recovers
via a single re-login. No backend change is
required (the auth chain is correct).

### `known_hosts` temp-file creation in the nonroot container — workaround, v0.9.0 candidate

The `bootstrap.hostKeyCallback` (post-handshake
TOFU-append path) tries to write a temp file
in `/var/lib/aegis/known_hosts.<pid>` to
atomically rename it onto the final
`known_hosts` path. The panel container runs as
the distroless `nonroot` user (UID 65532), and
`/var/lib/aegis/` on the host is owned by the
`aegis-deploy:1000` user with mode 0755. UID
65532 cannot create new files in the directory
even when the existing `known_hosts` file is
mode 0666 (the chmod-666 workaround we use for
the `known_hosts` file itself doesn't extend to
new files in the parent dir). The current
runtime workaround is `chmod 0666
/var/lib/aegis/known_hosts` (and accept that
the temp-file append fails — the warning
`bootstrap: temp known_hosts: open
/var/lib/aegis/.known_hosts.<pid>: permission denied`
in the panel logs is the signal). The first
TOFU connection on a fresh install still
succeeds because the panel tries the file
append best-effort (the operator-pinned
fingerprint is the primary safety net). The
proper fix is a v0.9.0 mount ownership change:
either `chown 65532:65532 /var/lib/aegis/`
in the deploy script, or have the deploy
script `chmod 1777 /var/lib/aegis/` (sticky +
world-writable so the nonroot container can
create temp files in the same dir). Tracked
under "ETXTBSY + service-restart race + temp-file
ownership" in `KNOWN_LIMITATIONS.md`.

### State-machine "invalid state transition" warning — cosmetic, v0.9.0 candidate

The provisioner logs a `bootstrap: invalid state
transition: offline -> online (should not
happen)` warning when a node transitions from
`offline` to `online` via re-provision. The
underlying state machine considers the
`offline → online` transition a "no change"
(idempotent install) and flags it as invalid,
but the SQL UPDATE still goes through. The
warning is purely cosmetic — operators see a
spurious "should not happen" log line on every
successful re-provision. v0.9.0 candidate: pin
`offline → online` as a valid transition in the
state machine's "provisionable" set (the
existing code in `internal/bootstrap/provisioner.go`
already special-cases this with a comment but
the log is still emitted). Tracked under
"state-machine validation gap" in
`KNOWN_LIMITATIONS.md`.

### v0.9.0 — Restore-drill on fresh VM (the GA blocker)

The `release.yml` hard-gate smoke test
(terraform + ansible + boot-log artifact in
CI) + a clean-VM restore-drill (download
backup → restore → first-boot → panel reachable)
is the single missing piece for the
`v1.0.0-mvp-soft-launch` tag. The Tier 1 plan
in `docs/gap-analysis-v0.8.24.md` §6 sequences
this as the dominant work unit (~3 days of
focused effort). The pre-existing
`tools/scripts/restore.sh` covers the operator
side; a CI job that runs it against a
fresh-provisioned VM is the missing piece.

## v0.8.28 — closed in v0.8.28 (Tier 3 + Tier 1 #3 + anti-leak)

The v0.8.28 release cut at `4a3c31a` closes two
release tracks in one tag plus one anti-leak
infrastructure hardening.

### Tier 3 — dialog extraction (PRs #254-#270)

Five dialog-extraction PRs split HostsView and
NodesView into 8 self-contained Vue components
under `frontend/src/views/dialogs/`. The view files
keep only the trigger refs + per-row pointers; the
dialogs own the form state, the wire-payload
builder, and the success card surface.

| Item | Closed by |
| --- | --- |
| `HostCreateDialog` + `HostEditDialog` extracted from `HostsView.vue` — `HostsView.vue` 1417→302 lines | PR #265 |
| `NodeCreateDialog` + `NodeEditDialog` extracted from `NodesView.vue` — `NodesView.vue` 2003→1463 lines | PR #266 |
| `NodeProvisionDialog` extracted from `NodesView.vue` — auth-method radio (key / password / stored) + conditional key/password fields, single-step form — `NodesView.vue` 1463→1162 lines | PR #267 |
| `NodeRotateDialog` extracted from `NodesView.vue` — `NodesView.vue` 1162→1035 lines | PR #268 |
| `NodeRefreshDialog` + `NodeInspectDialog` extracted from `NodesView.vue` — `NodesView.vue` 1035→639 lines. Fixed a pre-existing missing `.nodes__refresh-*` CSS block in the same PR | PR #269 |
| `ChangePasswordRequest` type dedup — the type was redefined in 3 places (UsersView, Settings, ProfileView); now a single shared type in `frontend/src/types/forms.ts` | PR #254 |
| `window.confirm` → `ConfirmDialog` component migration (HostsView + InboundsView) — typed wrapper over shadcn-vue's `AlertDialog`, replaces the native browser confirm that v0.7.0-era code used | PR #256 |
| `as never` → typed `as Parameters<typeof fn>[0]` casts in HostsView + NodesView (15 + 2 occurrences; the 2 in `provisionNode` use a different cast and are also fixed) | PR #263 |
| `camelizeKeys` memoization for large response bodies — 30%+ latency win on HostsView / NodesView open with > 50 nodes | PR #255 |
| `GET /api/v1/inbounds` batch endpoint — replaces the per-row `GetByNode` fan-out that the v0.8.x frontend (HostsView, NodesView) had to make on each view open. OpenAPI spec bumped to `0.8.27`; auto-regenerated `frontend/src/types/api.d.ts`; hand-mirrored `frontend/src/api/services/inbounds.ts` updated | PR #264 |
| 52 new vitest tests across 8 dialog test files (39 → 91 total) | PR #270 |

### Tier 1 #3 — backup-cron hardening (PRs #273-#275)

The backup-cron subsystem gets a 3-PR hardening
pass: parser extension, scheduler test coverage,
and admin-UI surface.

| Item | Closed by |
| --- | --- |
| `parseCronField` extended to the full Vixie construct set — `*` (wildcard), `N` (specific value), `N-M` (range, inclusive), `N-M/S` (range with step), `*/S` (every S-th value), `N,M,K` (list of values, sorted + deduplicated). Parser stays wall-clock only (5 fields required, no `@`-syntax, no sub-minute granularity, no timezones). `gocritic` `unnamedResult` compliance (named returns + 2 body `:=` → `=`). 38 new tests | PR #273 |
| Scheduler goroutine test coverage — 33 tests across 4 new test functions in `backend/internal/backups/scheduler_test.go`: `IdempotentWithinMinute` (two ticks in the same minute collapse to one fire), `AdvancesLastEvenOnNonMatch` (the `lastFire` field advances even when the expression does not match), `RespectsCancelledContext` (a cancelled `ctx` stops the loop cleanly), `TriggersAtScheduledTime` (the goroutine actually fires `nextFire` — uses a fake clock to avoid real time) | PR #274 |
| Admin-UI surface — `Service.ReloadCron(ctx, expr)` method on `backups.Service`; read-only `GET /api/v1/backups/schedule` endpoint (admin-scoped, `backups` scope; response is `{cron, retentionDays, maxCount, scheduleActive}`); `Backups → Schedule` section in `BackupsView.vue` (renders the active cron + retention + `scheduleActive` flag); 10 i18n keys (`backups.schedule.*` in en.json + ru.json); OpenAPI schema bump to `0.8.28`; auto-regenerated `frontend/src/types/api.d.ts` | PR #275 |

### Anti-leak infra hardening (PR #272)

| Item | Closed by |
| --- | --- |
| `BANNED_PATTERNS` in `tools/scripts/check-sensitive.sh` (and the AGENTS.md mirror) extended with a `ghp_[A-Za-z0-9]{36,}` / `gho_[A-Za-z0-9]{36,}` / `ghu_[A-Za-z0-9]{36,}` / `ghs_[A-Za-z0-9]{36,}` / `ghr_[A-Za-z0-9]{36,}` / `github_pat_[A-Za-z0-9_]{82,}` regex set. Catches classic `ghp_` / fine-grained `github_pat_` / OAuth `gho_` / `ghu_` / `ghs_` / `ghr_` tokens. Closes the 2026-08-20 3-PAT incident loop (the three leaked tokens have all been rotated by the operator; the regex form is the durable preventive). Pre-commit + CI gate + agent banned-list now all three catch a `ghp_` leak end-to-end | PR #272 |

## v0.8.28 — deployed to prod at 2026-08-21 MSK

The v0.8.28 release was deployed to the prod
panel (the live server, the `<sub-path>` decoy
prefix) at 2026-08-21 11:18 MSK by the orchestrator
(Mavis) via plink + sops-decrypt-on-operator
workflow. The deploy was a clean drop-in
replacement for the v0.8.27 prod (4 days old):
no schema changes, no OpenAPI breaking changes,
no new env vars. The PR #275 `GET
/api/v1/backups/schedule` endpoint is the only
new surface. v0.8.27 containers are preserved
as `aegis-panel-prev3` / `aegis-ui-prev3` for
rollback.

- panel image: `ghcr.io/qadversif/aegispanel:0.8.28`
  (sha256:`6a2627d5...c8ee85`)
- ui image: `ghcr.io/qadversif/aegispanel-ui:v0.8.28`
  (sha256:`8d3166fc...5b045db7b`)
- migrations: 20 applied (unchanged from v0.8.27)
- pre-deploy backup: not taken (v0.8.28 is a
  drop-in replacement; the v0.9.0 restore-drill
  will validate the backup -> restore path against
  a fresh VM, not the prod upgrade path)
- smoke: health=200, public `GET /inbounds`=401
  with `missing bearer token` (correct auth gate
  on the v0.8.28 batch endpoint), public UI
  shell=200 (Caddy -> decoy sub-path -> UI:8081)
- post-deploy logs: 11/11 backends on `PgStore`
  (auth, hosts, nodes, inbounds, subscription,
  users, plans, panelcfg, audits, webhooks,
  credentials) — see the "stale env" lesson below
  for the inbound_templates addition
- rollback tested: no (the previous `aegis-*-prev3`
  containers can be re-started if needed;
  the documented rollback is `docker rename
  aegis-panel aegis-panel-broken && docker
  rename aegis-panel-prev3 aegis-panel && docker
  start aegis-panel`)

### Lesson: stale env file (v0.8.9-era) — AEGIS_INBOUND_TEMPLATES_BACKEND was missing for 5+ releases

The `aegis-env.enc.env` shipped to the prod panel
on 2026-08-09 (the v0.8.9 fresh-install deploy)
had 11 `AEGIS_*_BACKEND=pg` env vars: auth, hosts,
nodes, inbounds, subscription, users, plans,
panelcfg, audits, webhooks, credentials. PR #205
(v0.8.13) added the 12th backend
`AEGIS_INBOUND_TEMPLATES_BACKEND` (inbound
templates — the per-tenant `Params` defaults
feature), but the prod env was never updated to
include it. The panel default for a missing
backend is `memory`, so from v0.8.13 (PR #205)
through v0.8.27, every inbound template created
in the prod UI was stored in-memory and lost on
every container restart (any panel bounce, any
deploy, any OOM-kill).

The v0.8.28 deploy caught this: the new
`backups.Service` boot log explicitly enumerates
every store, and the missing
`inbound_templates` line was visible in the
v0.8.27 logs as absent (compared to the v0.8.28
logs which show
`"store":"inbound_templates","message":"using pgx-backed store (PgStore)"`).
Added `AEGIS_INBOUND_TEMPLATES_BACKEND=pg` to the
deploy-time env, re-ran the v0.8.28 panel, all 12
backends now on `PgStore`. No data was migrated
from the memory backend (it was already gone).

**Action items** (for v0.9.0 or earlier):

1. **Add a v0.9.0 / v0.9.1 follow-up**: a
   `aegis admin` subcommand (or
   `tools/scripts/audit-env.sh` script) that
   reads the running panel's `AEGIS_*_BACKEND`
   set via the public API and diffs it against
   the canonical 12-backend set
   (11 + inbound_templates). Catches the
   "stale env from a prior deploy" class of
   silent misconfig.
2. **Document the canonical env template** at
   `docs/operator-guide.md` §"Environment
   variables" — a copy-paste-ready env file
   with all 12 backends pre-set to `pg` + the
   sops+age envelope vars + the non-secret
   canonical shape. The current runbook
   (§6.3) lists the vars but the operator has
   to assemble them by hand. A canonical
   template is the durable fix.
3. **The v0.8.13 inbound_templates data
   created on the prod between 2026-08-13
   and 2026-08-21 is gone** (memory backend,
   no persistence). This is the "operational
   cost" of the silent misconfig — captured
   here so future readers can find it.

## v0.8.31 — currently open (the mTLS+gRPC migration)

v0.8.29 introduced the gRPC control plane
alongside the v0.4.0-b HTTP+bearer surface
(dual-stack). v0.8.30 wired mTLS end-to-end
(self-signed root CA in the `agentca` table,
per-node server certs, panel-side client certs,
`RequireAndVerifyClientCert` on the agent, mTLS
dial on the panel). v0.8.31 ships the operator-
facing surface for the migration: the
`agent_transport` column, the
`aegis admin node rotate-transport` CLI, the
`GET /api/v1/nodes` deprecation-warning header,
and the CI grep gate that fails the build on a
new `http_transport.go` file outside the v0.8.29
archive path.

The v0.8.32 cut removes the HTTP+bearer path
entirely. The cut is gated on three conditions
(all must hold before v0.8.32 ships):

1. `GET /api/v1/nodes` shows 0% `transport=http`
   in prod for at least 1 release.
2. Telemetry confirms 0% HTTP at peak hour for
   7 days.
3. Operator sign-off.

The v0.8.31 release is the operator's window to
migrate. The runbook is in
`docs/operator-guide.md` §"mTLS+gRPC migration
(v0.8.31)" — a five-step flow that takes a
single-node install from the v0.8.30 dual-stack
default to a clean v0.8.32 cutover.

### `agent_transport=http` deprecation window — observability, v0.8.32 cut

The v0.8.30 dual-stack default is `http` (the
v0.4.0-b HTTP+bearer surface). The v0.8.31
release adds the `nodes.agent_transport` column
(migration 0024) + the `aegis admin node
rotate-transport` CLI to flip nodes from
`http` to `grpc`. The `GET /api/v1/nodes`
endpoint carries three headers when at least
one node is on `http`:

- `Deprecation: true` (RFC 8594).
- `X-Aegis-Deprecation-Notice: <text>` — the
  exact remediation command.
- `X-Aegis-Deprecation-Sunset: v0.8.32` — the
  release that removes the `http` value from
  the column's `CHECK` constraint.

**Operator action**: run
`aegis admin node rotate-transport --filter transport=http`
on the panel host. The CLI is idempotent (a
no-op rotation does not write the column or
emit an audit row) and is safe on cron. The
audit log records each actual rotation as
`node.transport.rotated`; the daily
`GET /api/v1/nodes` check is the operator's
"is the migration done?" signal.

**Why a per-node column** (vs a global
`AEGIS_AGENT_TRANSPORT` env): a global env
is process-wide and gives the operator no
per-node signal. The column is the per-node
source of truth that v0.8.32 will use to
drive the per-node transport pick (today, at
v0.8.31, the transport pick at apply time is
still process-wide via the env var; the column
is observability + audit only).

### Bootstrap `<-> nodes` import cycle — observed in v0.8.30 PR 1c, will be lifted in v0.8.31 follow-up

The v0.8.30 PR 1c commit notes a pre-existing
`bootstrap <-> nodes` import cycle that forced
the agentca wiring to live in `internal/app`
as a bridge. v0.8.31 surfaces the same cycle
in the `Service.Provision` integration (the
nodes-side caller of `agentca.EnsureNodeCerts`
is a bootstrap-package method that the
`bootstrap` package cannot import). v0.8.31
follow-up lifts the cycle by extracting the
shared cert material to a `pkg/middleware`
package that both sides import; the v0.8.32
cutover (which removes the HTTP transport
entirely) lands the cleaner topology. The
current bridge-in-app shape is correct but
not minimal.

### Per-node mTLS server key stored as plaintext DER — v0.8.31 follow-up

The v0.8.30 PR 1c migration
(`backend/migrations/0023_agentca.sql`) added
`nodes.mtls_server_key_ciphertext` as a
plaintext-DER column with a `_ciphertext`
suffix reserved for the v0.8.31 envelope pass.
The current release stores the private key
plaintext (the SQL column is BYTEA, not
encrypted at rest). The v0.8.31 follow-up
adds the envelope pass via the existing
`internal/crypto/envelope` API
(`AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS` +
`AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE`) so the
column carries the age-sealed ciphertext, not
the raw DER. The pre-v0.8.31 panel cannot
read the column either way (the column is
v0.8.30+) so the change is forward-only.

### Pre-v0.8.31 panel + v0.8.31 agent version skew — v0.8.30 documented, v0.8.31 still applies

A pre-v0.8.31 panel (e.g. the v0.8.30
production deploy) does not have the
`agent_transport` column. Per the v0.8.30
release notes (`docs/superpowers/plans/2026-08-25-mtls-grpc-agent.md`
§"Open risks" #3), the column miss falls back
to the v0.4.0-b HTTP+bearer default. After
the v0.8.31 release the panel ships the
column, so the skew resolves itself on the
next panel upgrade. The operator does not
need to take any action — the v0.8.30→v0.8.31
upgrade is the unblock.

## v0.9.1 — currently open (7 Tier 1 #3 follow-up items)

v0.8.28 closed the Tier 1 #3 batch but parked 7
follow-up items for v0.9.1. All 7 are pure Tier 1
\#3 (no schema, no OpenAPI, no breaking env
changes); the only surface that changes is the
optional `POST /api/v1/backups/schedule` payload
shape.

| # | Item | Notes |
| --- | --- | --- |
| 1 | Resolve the data race in `scheduler.maybeFire` | `loadSchedule` + `setNextFire` on the same `Scheduler` struct are concurrent (the manual-backup path writes while the scheduler reads). Currently benign (the read sees a stale `cron` for at most one tick) but `go test -race` flags it. Fix: a `sync.Mutex` around the two-field read+write pair, or a `sync/atomic.Pointer[ScheduleState]` |
| 2 | Add handler tests for `GET /api/v1/backups/schedule` | Cover: the `{cron, retentionDays, maxCount, scheduleActive}` response shape; the `backups`-scope guard (401 with no token, 403 with non-`backups` scope, 200 with admin token); the empty-`AEGIS_BACKUPS_CRON` case (`scheduleActive: false`); the case where the cron expression failed to parse at boot (`scheduleActive: false`, the raw `cron` field still reflects the input) |
| 3 | Document the `scheduleActive` semantic | `true` means the scheduler goroutine is running AND a `cron` expression is set AND it's parseable. `false` is manual-only mode (either `AEGIS_BACKUPS_CRON` is empty OR the expression failed to parse at boot). Add to the `GET /api/v1/backups/schedule` OpenAPI schema docstring + the operator-guide "Hot-reload" section |
| 4 | Wire a `POST /api/v1/backups/schedule` endpoint | Calls `Service.ReloadCron` and returns the refreshed schedule. The in-process `Service.ReloadCron(ctx, expr)` is already in place from PR #275; the UI surface for the POST is the missing piece. Add a matching form in the `Backups → Schedule` UI section (the current section is read-only) |
| 5 | Add a weekly cron that sweeps orphan on-disk dump files | Rows already deleted from the DB but the file still in `/var/lib/aegis/backups/`. Today these are left orphaned; `Cleanup` errors are non-fatal. A weekly sweep keeps the disk in sync with the DB without operator action |
| 6 | Field-naming consistency for `BackupsCron` | Pick a single convention (env var `AEGIS_BACKUPS_CRON` vs `BackupsCron` Go field vs `cron` OpenAPI schema vs `cron` UI form field) and propagate. Currently the env var is `AEGIS_BACKUPS_CRON`, the Go field is `Config.BackupsCron`, the OpenAPI schema is `cron`, and the UI form field is also `cron`. Two of the four are consistent; align the other two |
| 7 | Add syntax examples for the new Vixie constructs in `docs/operator-guide.md` §"Cron expression syntax" | Examples: `0 9-17/2 * * *` (every 2 hours from 09:00 to 17:00), `30 0 * * 1-5` (weekdays at 00:30), `0 0 1,15 * *` (1st and 15th of every month at midnight), `*/10 9-18 * * 1-5` (every 10 min during business hours on weekdays). The `*` / `N` / `N-M` examples are already there; the new constructs aren't |

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
