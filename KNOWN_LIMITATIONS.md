# Known Limitations — AegisPanel v0.8.14

This document tracks the gaps between what the latest shipped
milestone delivers and the full design in `ARCHITECTURE.md` §21.
Every open entry points to the milestone that closes it.
**Closed** items are kept for context — the PR that closed each
one is named so future readers can find the diff.

The current state of the project is **v0.8.14** (the
audit-3.1 fix chain + body-field shim closure). v0.8.14
is a **consolidation + security tightening release** that
closes the v0.8.13 backwards-compat shim: the refresh
token is **only** in the `Set-Cookie: aegis_rt=...`
header. The audit-3.1 fix chain (HttpOnly refresh cookie
plus frontend in-memory only and Caddy CSP) is end-to-end
active. Migration `0021_inbound_templates.sql` is part of
v0.8.13+ — it auto-applies on the next panel image bounce
because the migrations dir is host-mounted and
`app.Build` runs the migrations as a side effect at
WORKDIR=`/app`. The v0.8.x bucket is fully shipped
(host→node mapping #192, subscription URL #193,
per-user credential filter #198, merged Add+Provision
dialog #201, eslint cleanup #200, shadcn-vue RadioGroup
PR #202, inbound-templates PRs #205/#209/#210/#211/#212,
audit-3.1 #214/#215/#216, body-field drop #217). The
next candidate is v0.9.0 (smoke test on fresh VM in CI)
followed by v1.0.0 GA (admin password rotation is the
remaining GA-blocker).

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

## v0.8.14 — currently open

### Admin password rotation is the only remaining GA-blocker

The `aegis-fixture-admin-password` is the v0.8.9-era
seed password that every fresh install of the
container is born with. It is documented in
`deploy/ansible/group_vars/all.yml` (the operator
onboarding aid) and is therefore **public knowledge**
— anyone who has read the v0.8.x deploy path can
reproduce it. Every prod install (the live server.click,
2026-08-09; the canonical reference deploy) is
running with this fixture password.

The rotation is a live op, not a code change:

1. The `aegis admin passwd` subcommand (Linux build
   at `/usr/local/bin/aegis` on the panel host) takes
   the current password + a new password on stdin.
   The `bufio.Reader` default 4096B buffer drains
   the entire pipe in one Read, so the standard
   "echo newpass" + "echo newpass" pattern can land
   both lines before the read starts. The workaround
   is Python `subprocess.Popen` with `time.sleep(1.0)`
   between the two writes; see
   `~/.aegis/deploy.local.md` "Deploy 2026-08-09" §2
   for the worked example.
2. After rotation, the new password is recorded in
   `~/.aegis/deploy.local.md` (outside the repo, per
   the operator's privacy rule). The `age` envelope
   on the panel env file is NOT affected (the JWT
   signing key is independent of the admin password).
3. No new admin sessions are required to be logged
   out (the change is a "set new value" not a
   "rotate to a new value + revoke old"). Existing
   bearer tokens keep working until the 15-min TTL
   expires; existing refresh tokens keep working
   until the 30-day idle TTL expires (or a
   chain-revocation event).

This is **Priority 9 from the deep-state analysis**
and the only remaining GA-blocker for v1.0.0.
v0.8.14 is fully shipped; the v1.0.0 GA tag is
unblocked the moment the admin password is rotated
on the prod panel.

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
