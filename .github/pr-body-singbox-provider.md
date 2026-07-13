# feat(backend): sing-box CoreProvider implementation

First concrete core wired into the panel. Implements all 8 methods of
`cores.CoreProvider` (added in #31) for the sing-box 1.8 config schema.

## What it does

- **RenderConfig** builds a sing-box 1.8 JSON config from the panel's
  normalised `CoreConfig` DTO. Supports four Phase 1 protocols:
  VLESS+Reality+Vision, Hysteria 2, Shadowsocks (2022-blake3 only),
  Trojan. Default outbounds (`direct`, `block`) and a minimal
  `geoip:private → direct` route rule are emitted for every render.
- **ValidateConfig** is a structural check (non-empty valid JSON with
  an `inbounds` array). Schema-level validation belongs to the
  agent, not the panel.
- **Diff** is a unified diff via `pmezard/go-difflib`. Empty when
  inputs are byte-equal.
- **Apply** is a stub that returns `ErrApplyNotImplemented` — the
  agent gRPC transport lands in a later PR.
- **ParseStatus / ParseStats** are stubs. The agent will populate
  them from sing-box's gRPC API.

## Render input contract

Protocol-specific parameters (port, TLS, Reality keys, password, …)
flow in through `CoreConfig.Experimental["inbound_params"]`, keyed
by inbound tag. The shape is `map[inbound_tag]map[string]any`; the
typed `cores.InboundSpec` only carries `Tag / Type / HostID`. This
keeps the `cores` package minimal while letting the sing-box provider
render real configs.

Example:

```go
cfg := cores.CoreConfig{
    Inbounds: []cores.InboundSpec{
        {Tag: "vless-in", Type: "vless", HostID: "h-lv"},
        {Tag: "hy2-in",   Type: "hysteria2", HostID: "h-lv"},
    },
    Experimental: map[string]any{
        "inbound_params": map[string]any{
            "vless-in": map[string]any{
                "port": 443, "uuid": "…", "flow": "xtls-rprx-vision",
                "tls": map[string]any{
                    "server_name": "cdn.example.com",
                    "reality": map[string]any{
                        "private_key": "…", "short_ids": []string{"01ab"},
                    },
                },
            },
            "hy2-in": map[string]any{
                "port": 443, "password": "…",
                "tls": map[string]any{"server_name": "cdn.example.com"},
            },
        },
    },
}
```

`RenderConfig` produces this JSON (excerpt):

```json
{
  "log": {"level": "info", "timestamp": true},
  "inbounds": [
    {"type":"vless","tag":"vless-in","listen":"::","listen_port":443,
     "users":[{"name":"vless-in","uuid":"…","flow":"xtls-rprx-vision"}],
     "tls":{"enabled":true,"server_name":"cdn.example.com",
            "reality":{"private_key":"…","short_ids":["01ab"]}}},
    {"type":"hysteria2","tag":"hy2-in","listen":"::","listen_port":443,
     "users":[{"name":"hy2-in","password":"…"}],
     "tls":{"enabled":true,"server_name":"cdn.example.com"}}
  ],
  "outbounds": [
    {"type":"direct","tag":"direct"},
    {"type":"block","tag":"block"}
  ],
  "route": {
    "rules": [{"ip_cidr":["geoip:private"],"outbound":"direct"}],
    "final": "direct"
  }
}
```

## Self-registration

The package has an `init()` that calls `cores.Register(New())` so
importing `internal/cores/singbox` is enough to wire it in. `cmd/aegis`
adds a blank import. In dev the noop provider is still registered
by `main.go`; in production only sing-box is in the registry.

## Capabilities

Phase 1 MVP per ARCHITECTURE.md §7:

- `VLESS`, `VLESS_REALITY`, `VLESS_XTLS_VISION`
- `HY2`
- `SHADOWSOCKS`
- `TROJAN`
- `BALANCER`, `ACL`, `DYNAMIC_USERS`, `MULTI_PORT`, `WILDCARD_RANDOM`,
  `XHTTP_DOWNLOAD`

`TUIC` and `WIREGUARD` are deliberately not advertised — TUIC is a
later-phase protocol and WireGuard is Phase 4+.

## Files

| File | Purpose |
|---|---|
| `singbox.go` | `Provider`, `init()`, `Name/Version/Capabilities` |
| `render.go` | `RenderConfig` + top-level `sbConfig` shell + parameter helpers |
| `protocols.go` | Per-protocol renderers (VLESS, HY2, SS, Trojan) |
| `validate.go` | `ValidateConfig` (structural check) |
| `diff.go` | `Diff` via go-difflib |
| `apply.go` | `Apply` stub + `ErrApplyNotImplemented` |
| `parse.go` | `ParseStatus` / `ParseStats` stubs |
| `singbox_test.go` | Name/Version/Capabilities, init registration, Diff, Apply, Parse |
| `render_test.go` | Per-protocol render + error cases + validate |

Also touches:

- `cmd/aegis/main.go` — blank import to fire the singbox `init()`
- `go.mod` / `go.sum` — `github.com/pmezard/go-difflib v1.0.0`

## Out of scope (next PRs)

- **Agent gRPC transport** — `Apply` will round-trip the rendered
  config to the named node's agent.
- **Stats / status parsing** — `ParseStatus` and `ParseStats` will
  read sing-box's gRPC `StatsService` and `queryStats` outputs.
- **Phase 2 protocols** (TUIC, WireGuard, dynamic user add/remove).
- **Balancer host** rendering — sing-box's `urltest` outbound
  comes with the host manager PR.
- **Cascade topology** (`reverse` / `forward` chain).

## Testing

```
go test ./internal/cores/...
# ok  github.com/QAdversif/AegisPanel/internal/cores
# ok  github.com/QAdversif/AegisPanel/internal/cores/noop
# ok  github.com/QAdversif/AegisPanel/internal/cores/singbox
```

`render_test.go` round-trips a four-protocol config through
`RenderConfig → ValidateConfig` and asserts the per-inbound
JSON structure. `singbox_test.go` covers the registry self-registration
and the stub contracts.
