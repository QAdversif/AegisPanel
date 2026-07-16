## Summary

Adds the sing-box renderer to the subscription package. The handler now returns a `{"outbounds": [...]}` JSON document for `?target=singbox` (or auto-detected sing-box / Hiddify / NekoBox / Karing / Streisand / V2Box clients via User-Agent). All four supported protocols — VLESS, Hysteria 2, Shadowsocks, Trojan — produce valid sing-box outbounds with the per-protocol fields clients expect.

This is PR #42, the natural next step after the HTTP handler (PR #41). The renderer's design matches the sing-box 1.8 / 1.9 wire format; clients on older versions ignore unknown fields.

## What's in the box

- `internal/subscription/render_singbox.go` — the renderer. `Service.RenderSingbox(ctx, user, eps)` returns a marshaled `{"outbounds": [...]}` document. The per-protocol builders are `buildSingboxVLESS`, `buildSingboxHysteria2`, `buildSingboxShadowsocks`, `buildSingboxTrojan`. A shared `buildSingboxTLS` helper assembles the optional `tls` block from endpoint SNI / params (`sni`, `alpn`, `fingerprint`, `reality`).
- `internal/subscription/handler.go` — the `FormatSingbox` arm of the format switch now calls `h.renderSingbox(w, ctx, user)`. The `writeNotImplemented` 501 path remains for `FormatClash` (PR #43).
- `internal/subscription/handler_test.go` — `TestHandler_Render_SingboxNotImplemented` renamed to `TestHandler_Render_Singbox_Renders` and updated to assert 200 + `application/json`. The other handler tests are unchanged.
- `internal/subscription/render_singbox_test.go` — 5 unit tests for the renderer in isolation: the VLESS happy path (full TLS block: server_name, alpn, utls, reality), the empty case, the endpoint-SNI-overrides-params-sni case, the all-four-protocols case, and the no-TLS-block case (Shadowsocks). 0 broken existing tests.

## Design notes

- **Wire format is the sing-box outbounds document**, not a full sing-box config. The top-level is `{"outbounds": [...]}` — clients (Hiddify, Streisand, NekoBox, Karing, V2Box, sing-box CLI) accept this as a "remote subscription" and fill in inbounds / route / dns from their own template. A full sing-box config export is not in scope; the format is the minimal shape that delivers a working remote subscription.
- **Empty subscription is a valid subscription.** No entitled endpoints returns `{"outbounds": []}` and a 200 status, never an error. The client sees an empty list and lets the operator know the user has nothing to connect to.
- **Per-endpoint fail-soft.** Endpoints whose inbound lacks the data needed for a valid outbound (e.g. a VLESS without a UUID, a HY2 without a password) are silently skipped, mirroring the base64 renderer's contract. The subscription must still serve for the rest of the entitled endpoints. The current builder strategy is "produce whatever you can, let the client validate" — this avoids a single bad inbound poisoning the whole subscription. A future PR may surface per-endpoint validation errors through the request log.
- **TLS block is omitted when empty.** If none of `server_name` / `alpn` / `fingerprint` / `reality` is set, `buildSingboxTLS` returns `nil` and the caller skips the `tls` key on the parent outbound. sing-box treats "no tls" as "no TLS layer" — the operator's intent for a plaintext endpoint is preserved.
- **Endpoint SNI wins over params SNI.** The endpoint's `SNI[0]` override (set by the host manager for a specific endpoint) is the closest layer; `params.sni` (set on the inbound, the shared default) is the fallback. This mirrors the priority order from ARCHITECTURE.md §10.1 ("Endpoint value → Host value → Inbound value → System default").
- **Shadowsocks has no TLS block.** The protocol does not negotiate TLS at the outbound layer (TLS, if any, is provided by an outer transport such as h2 / grpc). The SS builder never calls `buildSingboxTLS`. The `TestRenderSingbox_TLSBlockOmittedWhenNoFields` test guards this.
- **Transport (ws / grpc / h2) lands in a later PR.** The sing-box outbound shape includes a `transport` block for those, but the inbound's `params.transport` field is not yet wired. PR #43 (Clash) is the right place to add it because both renderers need the same per-endpoint transport block.

## Test strategy

5 unit tests in `render_singbox_test.go`, no `//go:build integration` tag. They unmarshal the output back into `{"outbounds": []map[string]any}` and assert on the per-field values; this catches the most likely future regression — a typo in a JSON field name that the sing-box client silently refuses to parse.

The cross-service fixture from PR #40 is reused: the per-protocol `newSingboxFixture` seeds one node, one inbound, one host. The all-four-protocols test seeds four inbounds and four hosts in a loop. The test suite runs in plain `go test ./...` (no integration tag) so the default development loop is fast.

The integration test on the future `SubscriptionPgStore` will exercise the same paths against a real Postgres; the renderer is pure (no DB access) so the unit tests cover the full behaviour today.

## Compatibility

- Boot path: no changes. `cmd/aegis/main.go` already constructs `subscriptionSvc` and passes it to `router.Build`; the sing-box format lands on the same Service.
- Wire format: the sing-box outbounds document is what clients expect; no breaking change to clients that already auto-detect `application/json`.
- The base64 renderer's output is byte-identical to PR #40 / PR #41; the standard subscription headers on the response are unchanged.
- The `FormatClash` arm of the handler is still `writeNotImplemented` — that lands with PR #43.

## Follow-up

Natural next PRs in dependency order:

1. **Clash renderer** (`FormatClash`) — same per-endpoint structures, different wire format (YAML proxies array). The `buildSingboxTLS` helper is a starting point; the `buildSingboxReality` extraction is a candidate for shared.
2. **Transport block (ws / grpc / h2)** for both renderers. sing-box and Clash both need it.
3. **XHTTP `download_settings`** — sing-box-only field referencing another host. Requires a refactor: the resolver now needs to know about *other* hosts, not just the user's entitled ones.
4. **Format variables + wildcard `*` with random salt** — render-time substitution; the renderers stay byte-stable per request, with a per-request salt cache.
5. **Multi-port inbound** — random per-fetch selection from a comma-separated port list on the inbound.
6. **`SubscriptionPgStore`** — mirrors the inbounds / hosts / nodes pattern (PRs 24 / 36 / 37 / 38).
7. **Sub-token rotation** + the `/s3cr3t-sub-<hex>` URL prefix.

## Checklist

- [x] `go vet -tags=integration ./...` clean
- [x] `go vet -tags=integration -vettool=inline ./...` clean
- [x] `gofmt -l` clean
- [x] `go build -tags=integration ./...` clean
- [x] `go test ./...` passes (5 new tests; 0 broken)
- [x] No new migrations
- [x] No new env vars
- [x] Existing handler tests updated (the 501 → 200 rename)
