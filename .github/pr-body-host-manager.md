# feat(backend): Host manager — bundle-of-endpoints v3 model

Implements the panel-side CRUD for `hosts` per the v3
redesign in ARCHITECTURE.md §10. A Host is a *bundle* of
endpoints exposed to end users as a single product.

## What it does

- **Host** is the product-level entity. The operator
  sells "Latvia / VLESS+HY2" as one Host; the subscription
  URL has two entries (one per endpoint), but the admin
  UI / catalogue treats them as a single row.
- **Endpoint** is `(Node, Protocol, override)`. The
  override layer (Address, Port, SNI, Host, Path, Weight)
  lives on the endpoint so the operator can tweak a
  single (Node, Protocol) pair without touching the
  others.
- **Type** validates the v3 invariants from §10:
  - `direct` → endpoints.length == 1
  - `balancer` → endpoints.length >= 2 + Balancer.Strategy
  - `chain` → Phase 4+, explicitly rejected in Phase 1
- **StatusFilter** implements the §10.1.3 status-based
  visibility. Empty = all users.
- **HTTP**: `GET/POST /api/v1/hosts`,
  `GET/PUT/DELETE /api/v1/hosts/{id}`. All routes are
  protected by the new `auth.ScopeHosts` scope.

## v3 model realisation

Per ARCHITECTURE §10.0–10.1, the Go types are:

```go
type Host struct {
    ID, Remark, DisplayName   string
    Type                      HostType   // direct | balancer
    Enabled, Priority         bool/int
    StatusFilter              []UserStatus
    Country, City, Tags       string/[]string
    Endpoints                 []Endpoint // 1 (direct) or ≥2 (balancer)
    Balancer                  *Balancer  // required for type=balancer
    CreatedAt, UpdatedAt      time.Time
}

type Endpoint struct {
    ID                          uuid.UUID
    NodeID                      uuid.UUID  // FK -> nodes
    Protocol                    string     // vless | hysteria2 | shadowsocks | trojan
    Weight                      int        // default 1
    Address, SNI, Host          []string   // override layer
    Port                        *int
    Path                        string
}

type Balancer struct {
    Strategy                   BalancerStrategy // round_robin | least_loaded | random | least_ping | urltest
    HealthcheckURL             string
    HealthcheckIntervalSec     int
    FailoverEndpointIDs        []uuid.UUID     // must reference endpoints in this host
}
```

## Cross-entity validation

The `Service` consults `*nodes.Service` on every Create
and Update. An Endpoint whose `NodeID` does not resolve
returns `400 hosts: invalid endpoints[].node_id: node
<uuid> does not exist` — surfaced at the field level so
the admin UI can highlight the offending row.

## Inbound reference (planned migration)

`Endpoint.Protocol` is the protocol family string
("vless", "hysteria2", …) with a closed allow-list. The
v3 architecture calls for `Endpoint.InboundID` (UUID into
a future `inbounds` table). That table does not exist
yet — it lands in a later PR. The package comment in
`host.go` documents the planned migration; until then,
the Service enforces the same protocol allow-list that
the future Inbound model will enforce at registration
time, so a typo cannot leak into a subscription URL.

## Files

| File | Purpose |
|---|---|
| `host.go` | `Host`, `Endpoint`, `Balancer`, `HostType`, `BalancerStrategy`, `UserStatus` |
| `store.go` | `Store` interface + `MemoryStore` (concurrency-safe, copy-on-write) |
| `service.go` | `Service` with validation, ID/timestamp generation, cross-entity checks |
| `validate.go` | Validation helpers (remark, type, strategy, status, weight, URL) |
| `handler.go` | HTTP `Router` + handlers (`/api/v1/hosts`) |
| `host_test.go` | Model `IsValid` + clone-depth tests |
| `store_test.go` | `MemoryStore` CRUD + concurrency tests |
| `service_test.go` | All validation paths + partial-update semantics |
| `handler_test.go` | HTTP round-trip + scope enforcement |

Also touches:

- `cmd/aegis/main.go` — wire `hosts.NewService` and pass
  to `router.Build`
- `internal/router/router.go` — mount `/api/v1/hosts`
- `internal/auth/scopes.go` — add `ScopeHosts`
- `internal/auth/middleware.go` — add `WithClaims` test
  helper (unexported `ctxKeyClaims` was previously
  inaccessible from outside the package)

## Out of scope (next PRs)

- **`inbounds` model + InboundPgStore** — the FK that
  `Endpoint.InboundID` will replace `Endpoint.Protocol`
  with.
- **Inbound defaults layer** — the override chain
  Endpoint → Host → Inbound → System default is
  documented in §10.1 but the resolution code lives
  in the subscription service (Phase 2).
- **Subscription URL rendering** — sing-box provider
  renders JSON configs; the subscription service
  renders per-user URLs (`singbox`, `clash-meta`,
  `base64`). The Host model is now ready to feed it.
- **Format variables resolution** (`{USERNAME}`,
  `{DATA_LEFT}`, etc.) — §10.1.1.
- **Wildcard `*` salt** — §10.1.2.
- **Multi-port selection** — §10.1.4.
- **XHTTP download_settings** — §10.1.5.
- **Chain type** — Phase 4+ cascade topology.

## Testing

```
go test ./internal/hosts/...
# ok  github.com/QAdversif/AegisPanel/internal/hosts
```

Lint: `golangci-lint` clean on `internal/hosts/...`,
`internal/auth/...` (modulo the pre-existing CRLF
issues on Windows-only `core.autocrlf = true` working
trees — Linux CI sees LF and is happy).
