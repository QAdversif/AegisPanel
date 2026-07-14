# feat(backend): per-node Inbound model + CRUD

Implements the panel-side CRUD for the `inbounds` table
introduced in migration 0003. An Inbound is a single
protocol listener on a specific node (VLESS-Reality,
Hysteria 2, Shadowsocks, …) — the v3 model realisation
of ARCHITECTURE.md §10.0.

## What it does

- **Inbound** is the per-node listener: name, protocol,
  listen address, listen port, enabled flag, free-form
  tags, and a `params` blob that the sing-box provider's
  RenderConfig already understands.
- **Cross-entity validation** via `*nodes.Service`: every
  inbound's `node_id` must resolve, and an inbound
  belongs to exactly one node for its lifetime (NodeID
  is immutable in the model).
- **Two unique constraints** enforced at the storage
  layer:
  - `UNIQUE (node_id, name)` — operator label collision
  - `UNIQUE (node_id, listen_port)` — port collision
    (two protocols on the same port would collide at
    the OS level)
- **Protocol allow-list** via DB CHECK constraint
  (`'vless' | 'hysteria2' | 'shadowsocks' | 'trojan'`)
  and Go-side `isAllowedProtocol`. Mirrors the
  per-protocol renderers in the sing-box provider so a
  typo cannot reach the agent.
- **HTTP**: `/api/v1/nodes/{nodeId}/inbounds[/...]`,
  ScopeNodes-guarded. The URL path makes the node scope
  mandatory — every handler reads `{nodeId}` and the
  service rejects mismatches on Update.

## v3 model realisation

ARCHITECTURE §10.0 calls for a Host's Endpoint to
reference an Inbound by `inbound_id`. PR #33 implemented
the host model with a temporary `Endpoint.Protocol`
string + Service-side allow-list. **This PR sets up the
inbound side of the join.** The actual migration —
`Endpoint.Protocol` → `Endpoint.InboundID` — is a
straight rename + a FK addition in the next PR (#35),
which will also need a small migration on the (future)
`hosts` table.

Until that lands, the inbounds CRUD is fully usable
from the admin UI / API. The Service here enforces the
same protocol allow-list the host manager enforces, so
the eventual join is a straight rename + a FK addition.

## Migration

`backend/migrations/0003_node_inbounds.sql` creates the
`inbounds` table with three indexes:

- `inbounds_node_id_idx` — `ListByNode`
- `inbounds_node_id_enabled` — admin UI's
  "show only enabled" filter
- `inbounds_protocol_idx` — `ListByProtocol` (e.g. "all
  VLESS inbounds across the fleet")

The `Down` body drops the table and indexes in
reverse order. The existing `inbound_sets` /
`inbound_revisions` tables from migration 0001 are
**not touched** — they model a different concept
(reusable templates, Phase 4+ cascade work) and will
either be deprecated or wired in a future PR.

## Files

| File | Purpose |
|---|---|
| `backend/migrations/0003_node_inbounds.sql` | new table + indexes + CHECK + UNIQUE constraints |
| `internal/inbounds/inbound.go` | `Inbound` model + `Protocol` enum |
| `internal/inbounds/store.go` | `Store` interface + `MemoryStore` (concurrency-safe, copy-on-write) |
| `internal/inbounds/service.go` | `Service` with validation, ID/timestamp generation, cross-entity checks |
| `internal/inbounds/validate.go` | Validation helpers (name, protocol, port, listen, tags) |
| `internal/inbounds/handler.go` | HTTP `Router` + handlers (scoped under `{nodeId}`) |
| `internal/inbounds/inbound_test.go` | Model `IsValid` + protocol-allow-list |
| `internal/inbounds/store_test.go` | `MemoryStore` CRUD + unique-constraint tests + concurrency |
| `internal/inbounds/service_test.go` | All validation paths + partial updates + cross-entity |
| `internal/inbounds/handler_test.go` | HTTP round-trip + scope enforcement + URL scoping |

Also touches:

- `cmd/aegis/main.go` — wire `inbounds.NewService`,
  pass to `router.Build`
- `internal/router/router.go` — mount
  `/api/v1/nodes/{nodeId}/inbounds`

## Out of scope (next PRs)

- **`InboundPgStore`** — the migration is in place;
  the pgx-backed Store lands with the broader Phase 1
  pg migration (mirroring `auth.PgStore` from PR #24
  and the planned `nodes.PgStore`).
- **`Endpoint.Protocol` → `Endpoint.InboundID`**
  migration on the (future) `hosts` table + Host
  model update. The comment in `internal/hosts/host.go`
  documents the planned migration; this PR is the
  pre-requisite.
- **Agent gRPC transport** for `apply_config` against
  the inbound set of a node.
- **Inbound defaults** (the "Inbound value" layer in
  the §10.1 override chain) — Phase 2 subscription
  service work.

## Testing

```
go test ./internal/inbounds/...
# ok  github.com/QAdversif/AegisPanel/internal/inbounds
```

Lint: `golangci-lint` clean on `internal/inbounds/...`.
All existing tests across `internal/{auth,cores,hosts,
migrations,nodes}` continue to pass.
