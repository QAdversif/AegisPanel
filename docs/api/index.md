---
title: API reference
---

# API reference

The canonical API contract is the OpenAPI spec at
[`docs/openapi.yaml`](https://github.com/QAdversif/AegisPanel/blob/main/docs/openapi.yaml)
in the repo root.

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
| `nodes`       | CRUD on `/nodes/{id}` + `POST /nodes/{id}/provision` (the BYO install flow) + `POST /nodes/{id}/rotate-panel-key` (v0.8.4; the panel-key rotation surface, UI mirror of the v0.8.3 CLI) |
| `inbounds`    | CRUD on `/nodes/{nodeId}/inbounds/{id}`                                                                     |
| `hosts`       | CRUD on `/hosts/{id}`                                                                                       |
| `plans`       | CRUD on `/plans/{id}` (v0.6.0) вЂ” the operator-facing tariff catalog                                          |
| `users`       | CRUD on `/users/{id}` + `POST /users/{id}/rotate-token`                                                     |
| `panelcfg`    | `GET /panelcfg`, `POST /panelcfg/rotate`, `POST /panelcfg/rotate-to`, `POST /panelcfg/reset`              |
| `sub`         | `GET /sub/{token}` (the user-facing subscription render; auto-detects client format)                       |
| `audits`      | `GET /audits` + `GET /audits/{id}` (read-only)                                                              |
| `backups`     | CRUD on `/backups/{id}` + `POST /backups/{id}/restore` (v0.5.0)                                            |
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
  button is the primary path.

## See also

- `docs/openapi.yaml` вЂ” the full OpenAPI 3.0 spec
- `docs/ARCHITECTURE.md` вЂ” the data model and the
  request / response design rationale
- `docs/operator-guide.md` вЂ” the operator-side
  install + daily-operations guide
- `docs/SECURITY.md` вЂ” the threat model and the
  auth flow
