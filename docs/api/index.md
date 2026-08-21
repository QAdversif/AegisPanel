---
title: API reference
---

# API reference

The canonical API contract is the OpenAPI spec at
[`docs/openapi.yaml`](https://github.com/QAdversif/AegisPanel/blob/main/docs/openapi.yaml)
in the repo root.

**v0.8.28** adds `GET /api/v1/backups/schedule`, the
read-only schedule surface for the admin UI. The
endpoint returns the current `AEGIS_BACKUPS_CRON`
expression, the `AEGIS_BACKUPS_RETENTION_DAYS` +
`AEGIS_BACKUPS_MAX_COUNT` retention knobs, and a
`scheduleActive` boolean (`true` if the scheduler
goroutine is running, `false` for manual-only mode
— i.e. `AEGIS_BACKUPS_CRON` is empty or the
expression failed to parse at boot). The response
shape is:

```json
{
  "cron": "0 2 * * *",
  "retentionDays": 30,
  "maxCount": 0,
  "scheduleActive": true
}
```

The endpoint is admin-scoped (requires the
`backups` scope) and is paired with the new
`Backups → Schedule` section in `BackupsView.vue`
so the operator can audit the active cron +
retention at a glance. The `Service.ReloadCron(ctx, expr)`
method is also in place (in-process hot-reload);
a matching `POST /api/v1/backups/schedule`
endpoint is planned for v0.9.1. PR #275. The
OpenAPI spec was bumped to `0.8.28` and the
generated `frontend/src/types/api.d.ts`
regenerated. The hand-mirrored
`frontend/src/api/services/backups.ts` is also
updated.

**v0.8.28** also adds `GET /api/v1/inbounds`, the
top-level batch endpoint for the inbound
catalog. The response is `InboundListResponse`
with an `inbounds[]` array where every entry
carries the owning `nodeId`; the per-row
`GET /api/v1/nodes/{nodeId}/inbounds/{id}` shape
is unchanged. The endpoint replaces the per-row
`GetByNode` fan-out that the v0.8.x frontend
(HostsView, NodesView) had to make on each view
open — under heavy node counts the fan-out was
the dominant open-view latency contributor. The
OpenAPI spec was bumped to `0.8.28` and the
generated `frontend/src/types/api.d.ts`
regenerated without manual mirror work (the
hand-mirrored `frontend/src/api/services/
inbounds.ts` is also updated). PR #264
(perf-only — no auth / scope / schema change
beyond the new `nodeId` field on the response
entry).

**v0.8.8** wires the v0.8.7 `RefreshAgentBearer`
recovery loop into the sing-box `Apply` path so a
401 from `POST /v1/apply` triggers a refresh +
retry without operator intervention. The
`singbox.NodeResolver` interface gains
`RefreshBearer(ctx, id) (string, error)`; the
`main.go` `singboxNodeResolver` adapter
implements it via `nodes.Service.RefreshAgentBearer`.
One retry only — no loop. 500/404 do NOT
trigger refresh (server-side, not stale-bearer).
The audit row's `ActorID` is empty for the
auto-refresh path (no `auth.Claims` in the
BatchedApplier context) vs non-empty for the
v0.8.7 operator-initiated path — distinguishes
"the panel did this" from "the operator did
this" in the audit UI. Race is benign (two
goroutines refreshing the same node both read the
same `agent.env` value, DB write is idempotent).
6 new Apply-level tests + updated
`flushfn_smoke_test` `stubResolver`. PR #189
(backend-only — no OpenAPI shape change).

**v0.8.7** adds the refresh-agent-bearer recovery
path. `nodes.Service.RefreshAgentBearer` decrypts
the stored panel SSH key, SSHes into the node via
`bootstrap.NewClient` (TofuPolicy=`Reject`), reads
`/etc/aegis/agent.env`, parses
`AEGIS_AGENT_BEARER`, and updates
`nodes.agent_bearer` — the recovery path for
"agent regenerated its bearer out-of-band".
Exposed as `POST
/api/v1/nodes/{id}/refresh-agent-bearer` (200
with the new bearer; 4xx with the SSH-fail /
parse-fail / no-stored-key error mapping). The
NodesView gets a "Refresh agent bearer" dropdown
entry (visible for `online` / `offline` /
`draining` / `disabled`; hidden for `new`). The
action is recorded in the audit log as
`node.agent-bearer.refresh`. New
`backend/internal/nodes/refresh_bearer.go`
(Service) + `handler_refresh_bearer.go` (HTTP) plus
30 + 11 unit tests. `App.go` wires
`WithSSHClientFactory(bootstrap.NewClient)
.WithKnownHosts(cfg.AgentKnownHosts)
.WithSSHUser(cfg.AgentSSHUser)`. The
BatchedApplier integration (401 → auto-refresh)
is the v0.8.8 follow-up. PR #188.

**v0.8.5** adds `GET /api/v1/nodes/{id}/stored-key`,
the read-side mirror of the v0.8.1 persistent panel SSH
key feature. The panel decrypts
`nodes.ssh_private_key_ciphertext` via the operator's
age envelope, derives the public-key line + SHA-256
fingerprint, and returns the public surface. The private
key never leaves the panel process. The 200 body carries
`{ has_stored_key, public_key_line, fingerprint,
algorithm, key_updated_at }`; `has_stored_key: false` for
`new` nodes (or legacy v0.3.0..v0.7.x nodes that have not
been back-filled with the v0.8.3 CLI). The NodesView's
per-row dropdown gets a "Show stored key" entry (visible
for any state). The read is recorded in the audit log
as `node.stored-key.read` (the "who looked at this row
at time T" trail).

**v0.8.4** adds `POST /api/v1/nodes/{id}/rotate-panel-key`,
the HTTP mirror of the v0.8.3
`aegis admin node rotate-panel-key` CLI (PR #184). The
endpoint generates a fresh ed25519 keypair on the panel,
encrypts the private half with the operator's age
envelope, persists the ciphertext on the
`nodes.ssh_private_key_ciphertext` column, and appends
the public half to the node's `~/.ssh/authorized_keys`.
The 200 body carries the new public key line and SHA256
fingerprint so the operator can verify what is now in
the node's authorized_keys. After the call returns, the
next re-provision on the node (via
`POST /api/v1/nodes/{id}/provision` with the
`authMethod: 'stored'` path) decrypts and reuses the new
key — the v0.8.x "auto-deploy" experience becomes
available retroactively on v0.3.0..v0.7.x nodes. The
NodesView's per-row dropdown gets a "Rotate panel key"
entry (visible for `online` / `offline` / `draining` /
`disabled`; hidden for `new` since the panel cannot SSH
into a never-installed node).

**v0.8.2** adds the credentials admin surface
(`/api/v1/credentials/` + the per-user and
per-inbound cross-cut reads) backed by the data
model that landed in v0.8.0. The data layer was
already in place; v0.8.2 is the HTTP wrapper. The
`/credentials/{id}` routes follow the same shape
as the v0.6.0 plans CRUD and the v0.7.0 webhooks
CRUD: `ScopeCredentials` is granted to every role
(admin / operator / viewer) because every operator-
facing surface that lists users (the UsersView's
"View credentials" dropdown, the future multi-user
sing-box renderer's lookup, the "Who can use this
inbound?" panel on an inbound row) reads the
credentials table.

**v0.8.1** (`ssh_password` field added to the
`NodeProvisionRequest` schema; the panel now accepts
an SSH login password for first-time node install in
addition to the pre-existing `ssh_private_key` field).
The two fields are mutually exclusive at the HTTP
layer (XOR enforced in
`backend/internal/bootstrap/handler.go`). The
endpoints list is unchanged from v0.8.0. The
front-end uses `openapi-typescript` to generate a
typed client from it. This page is a short overview
— for per-endpoint details, see the spec.

v0.8.0 ships the same admin surface as v0.7.0 (auth,
nodes, inbounds, hosts, plans, users, panelcfg,
audits, backups, webhooks). v0.8.0 is purely
internal: a new migration table and the data layer
for Phase 2 multi-user. The release closes the
Phase 2 multi-user sing-box render milestone
end-to-end (data model in `internal/credentials` with
migration 0019, multi-user renderer signature, builder
wiring and `BatchedApplier` fan-out narrow, per-user
subscription render) plus the audit-log call-site
wiring into every mutating service.

## Conventions

- **Base URL:** `https://<panel-domain>/api/v1`
- **Format:** JSON, `camelCase` keys (the `camelizeKeys`
  response interceptor in `frontend/src/api/client.ts`
  bridges the camelCase spec to the snake_case Go
  JSON tags; a full wire-format normalization is a
  separate work item).
- **Auth:** `Authorization: Bearer <token>` for admin
  endpoints; HMAC-SHA256 webhooks for inbound events.
- **Idempotency:** `Idempotency-Key` header on `POST` /
  `PUT` / `PATCH` / `DELETE`.
- **Rate limit:** 100 req / minute per token; `429` with
  `Retry-After`.
- **Versioning:** breaking changes bump the URL;
  non-breaking changes bump the
  `X-Api-Minor-Version` header.

## Endpoints shipped in v0.8.0

The full list is in `docs/openapi.yaml`. v0.8.0 is the
same surface as v0.7.0 (the spec is still at
`0.7.0`; no endpoints were added in v0.7.1, v0.7.2,
or v0.8.0 — all three releases were internal / data
layer / docs changes). The headline groups:

| Group         | Endpoints                                                                                                   |
| ------------- | ----------------------------------------------------------------------------------------------------------- |
| `auth`        | `POST /auth/login`, `POST /auth/refresh`, `GET /auth/me`, `POST /auth/change-password`                    |
| `nodes`       | CRUD on `/nodes/{id}` + `POST /nodes/{id}/provision` (the BYO install flow) + `POST /nodes/{id}/rotate-panel-key` (v0.8.4; the panel-key rotation surface, UI mirror of the v0.8.3 CLI) + `GET /nodes/{id}/stored-key` (v0.8.5; the read-side debug surface for the v0.8.1 persistent key — panel decrypts the stored ciphertext via the age envelope and returns the public-key line + SHA-256 fingerprint) + `POST /nodes/{id}/refresh-agent-bearer` (v0.8.7; the recovery path for "agent regenerated its bearer out-of-band" — panel SSHes in via the stored panel key, reads `/etc/aegis/agent.env`, updates `nodes.agent_bearer`; BatchedApplier wires the 401→auto-refresh loop in v0.8.8) |
| `inbounds`    | CRUD on `/nodes/{nodeId}/inbounds/{id}` + `GET /inbounds` (v0.8.28; top-level batch endpoint for the inbound catalog — returns `InboundListResponse` with `inbounds[]` each carrying `nodeId`; replaces the per-row `GetByNode` fan-out that the v0.8.x frontend had to make on each view open) |
| `hosts`       | CRUD on `/hosts/{id}`                                                                                       |
| `plans`       | CRUD on `/plans/{id}` (v0.6.0) вЂ” the operator-facing tariff catalog                                          |
| `users`       | CRUD on `/users/{id}` + `POST /users/{id}/rotate-token`                                                     |
| `panelcfg`    | `GET /panelcfg`, `POST /panelcfg/rotate`, `POST /panelcfg/rotate-to`, `POST /panelcfg/reset`              |
| `sub`         | `GET /sub/{token}` (the user-facing subscription render; auto-detects client format)                       |
| `audits`      | `GET /audits` + `GET /audits/{id}` (read-only)                                                              |
| `backups`     | CRUD on `/backups/{id}` + `POST /backups/{id}/restore` (v0.5.0) + `GET /backups/schedule` (v0.8.28; read-only schedule surface for the admin UI — returns `{cron, retentionDays, maxCount, scheduleActive}`; admin-scoped, `backups` scope; paired with the `Backups → Schedule` section in `BackupsView.vue`)                                            |
| `webhooks`    | CRUD on `/webhooks/{id}` + `/webhooks/{id}/deliveries` + `/webhooks/{id}/test` + `/webhooks/dlq[/...]` (v0.7.0 surface; v0.7.1 wires the dispatcher into every mutating handler so the events actually fan out to subscribers) |
| `credentials` | CRUD on `/credentials/{id}` + `/credentials/by-user/{userId}` + `/credentials/by-inbound/{ibId}` (v0.8.2 surface; the data model was already in `internal/credentials` + migration 0019 from v0.8.0) |
| `meta`        | `GET /health` (anonymous, liveness) + `GET /cores` (provider catalog)                                       |

The detailed per-endpoint schemas (request / response
shapes, error codes, examples) live in
`docs/openapi.yaml`. The Vue UI consumes the
generated types at `frontend/src/types/api.d.ts`
(regenerated by `npm run codegen`); the
hand-mirrored service functions live at
`frontend/src/api/services/<entity>.ts`.

## Endpoints shipped in a later version

The following endpoints are documented in
`ARCHITECTURE.md` В§13 but not yet wired:

- `POST /api/v1/webhooks/payment` (the inbound
  payment confirmation webhook from the Cabinet)
- The `/cabinet/*` surface (the end-user cabinet
  UI; lands with `internal/cabinet` in v1.2+)
- The `internal/notifications` outbound side
  (Telegram + generic webhook via n8n; lands in
  v0.8.x)
- The `aegis admin node rotate-panel-key` CLI
  subcommand (v0.8.3; takes an existing node and
  rotates the stored panel SSH key). v0.8.4 ships
  the HTTP mirror
  (`POST /api/v1/nodes/{id}/rotate-panel-key`); the
  CLI is now the operator-side fallback, the UI
  button is the primary path. v0.8.5 ships
  the read-side mirror
  (`GET /api/v1/nodes/{id}/stored-key`) so the
  operator can audit the stored ciphertext without
  rotating it.

## See also

- `docs/openapi.yaml` вЂ” the full OpenAPI 3.0 spec
- `docs/ARCHITECTURE.md` вЂ” the data model and the
  request / response design rationale
- `docs/operator-guide.md` вЂ” the operator-side
  install + daily-operations guide
- `docs/SECURITY.md` вЂ” the threat model and the
  auth flow
