# feat(backend): Endpoint references Inbound by ID

Closes the v3 Host model realisation: an Endpoint is a
`(Node, Inbound)` pair, not a `(Node, Protocol)` pair.
The protocol family moves entirely onto the Inbound (PR
#34); the Endpoint carries only an `InboundID` and the
per-endpoint override layer.

## What it does

- **Migration 0004 (`0004_hosts_v3.sql`)** — drops the
  v2 `hosts` table (single-node, no endpoints; never
  wired to Go code) and creates the v3 schema:
  - `hosts` (id, remark, type, enabled, priority,
    status_filter, country, city, tags, balancer,
    created_at, updated_at) — same shape as the
    pre-existing Go `Host` model.
  - `host_endpoints` (id, host_id FK CASCADE,
    node_id FK RESTRICT, inbound_id FK RESTRICT,
    weight, address, port, sni, host, path,
    created_at, updated_at) — relational form of the
    embedded `Endpoints[]` array.
  - CHECK `type IN ('direct', 'balancer')` (chain is
    Phase 4+).
  - CHECK `weight > 0`, CHECK `port BETWEEN 1 AND 65535`.
  - The cross-entity invariant
    `host_endpoints.node_id = inbounds.node_id` is
    enforced by the Service layer (see below);
    PostgreSQL cannot express cross-table invariants
    in CHECK constraints, and a trigger would obscure
    the validation. The migration's `Down` body
    restores the v2 schema.
- **Go model** — `Endpoint.Protocol string` →
  `Endpoint.InboundID uuid.UUID`. The package comment
  in `host.go` documents the new contract: the
  protocol family is read off the referenced Inbound
  at render time.
- **Service** — adds `*inbounds.Service` as a
  dependency; replaces `validateEndpointProtocol` with
  `validateEndpointInbound` (FK resolves +
  `inbound.NodeID == endpoint.NodeID`). The
  protocol allow-list is gone from the hosts package
  — it lives on the inbounds package and on the DB
  CHECK constraint.
- **HTTP wire format** — `protocol` → `inbound_id`. The
  change is wire-only; the rest of the host API
  (remark, type, balancer, status_filter, …) is
  unchanged.
- **Tests** — updated to seed an inbound per node and
  reference it by ID. New test cases:
  `RejectsEndpointWithZeroInboundID`,
  `RejectsEndpointWithUnknownInbound`,
  `RejectsInboundOnWrongNode`. The old
  `RejectsEndpointWithUnknownProtocol` is gone (the
  protocol is no longer the Endpoint's concern).

## Cross-entity invariant

The v3 model has two cross-table references per
endpoint:

1. `host_endpoints.node_id → nodes.id` (FK)
2. `host_endpoints.inbound_id → inbounds.id` (FK)
3. `host_endpoints.node_id = inbounds.node_id` (the
   v3 invariant: the inbound must run on the same
   node the endpoint claims)

(1) and (2) are DB-enforced via FK constraints.
(3) is application-side, enforced in
`validateEndpointInbound` (internal/hosts/validate.go)
on every Create / Update. A failure returns a 400 with
a precise field reference:
`"inbound <id> belongs to node <X>, not <Y>"`.

The endpoint resolution is symmetric: every endpoint
must reference an inbound that exists AND that runs on
the same node. A typo or a misconfigured inbound never
leaks into a subscription URL.

## Files

| File | Purpose |
|---|---|
| `backend/migrations/0004_hosts_v3.sql` | drop v2 hosts, create v3 hosts + host_endpoints |
| `backend/internal/hosts/host.go` | `Endpoint.Protocol` → `Endpoint.InboundID` |
| `backend/internal/hosts/service.go` | take `*inbounds.Service`; new validation |
| `backend/internal/hosts/validate.go` | `validateEndpointInbound` replaces `validateEndpointProtocol` |
| `backend/internal/hosts/handler.go` | wire: `protocol` → `inbound_id` |
| `backend/internal/hosts/host_test.go` | model tests updated |
| `backend/internal/hosts/service_test.go` | rewritten: `testEnv` seeds nodes + inbounds; new cross-entity cases |
| `backend/internal/hosts/handler_test.go` | rewritten: uses the same `testEnv` helper |
| `backend/cmd/aegis/main.go` | wire `inboundsSvc` into `hosts.NewService` |

## Out of scope (next PRs)

- **`HostPgStore`** — the migration is in place; the
  pgx-backed Store lands with the broader Phase 1
  pg migration. The MemoryStore continues to embed
  the Endpoints array on the Host struct; the
  PgStore will use the relational form.
- **Format variables / wildcard / multi-port** — the
  subscription service work. The override chain
  (Endpoint → Host → Inbound → System default) is
  now possible: every layer carries the right keys.
- **Agent gRPC `apply_config`** — uses the resolved
  Endpoint → Inbound pointer to fetch the actual
  sing-box config the agent should apply.

## Testing

```
go test ./internal/hosts/...
# ok  github.com/QAdversif/AegisPanel/internal/hosts
```

Lint: `golangci-lint` clean on `internal/hosts/...`.
All existing tests across `internal/{auth,cores,
inbounds,migrations,nodes}` continue to pass.
