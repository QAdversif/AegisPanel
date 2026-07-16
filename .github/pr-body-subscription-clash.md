## Summary

Adds the Clash (Clash Meta / mihomo) renderer to the subscription package. The handler now returns a `proxies: [ ... ]` YAML document for `?target=clash` (or auto-detected Clash / Clash Meta / mihomo clients via User-Agent). All four supported protocols — VLESS, Hysteria 2, Shadowsocks, Trojan — produce valid Clash proxy entries with the per-protocol fields the Clash schema expects.

This is PR #43, the natural next step after the sing-box renderer (PR #42). The renderer's design matches the Clash Meta / mihomo YAML schema; older Clash clients ignore unknown fields.

## What's in the box

- `internal/subscription/render_clash.go` — the renderer. `Service.RenderClash(ctx, user, eps)` returns a marshaled `proxies: [ ... ]` YAML document via `gopkg.in/yaml.v3`. Per-protocol builders `buildClashVLESS`, `buildClashHysteria2`, `buildClashShadowsocks`, `buildClashTrojan`.
- `internal/subscription/handler.go` — the `FormatClash` arm of the format switch now calls `h.renderClash(w, ctx, user)`. The `writeNotImplemented` 501 path is removed for `FormatClash`; the only "not implemented" left in the switch is the catch-all 415 for unknown `?target=` values.
- `internal/subscription/handler_test.go` — `TestHandler_Render_ClashNotImplemented` renamed to `TestHandler_Render_Clash_Renders` and updated to assert 200 + `text/yaml`.
- `internal/subscription/render_clash_test.go` — 6 unit tests for the renderer in isolation: the VLESS happy path (full TLS field set: sni / alpn / fingerprint / client-fingerprint / reality-opts), the empty case, the endpoint-SNI-overrides-params case, the all-four-protocols case, the `tls: true` for VLESS case, the no-`tls:`-field for Shadowsocks case. 0 broken existing tests.

## Design notes

- **Wire format is the Clash proxy list, not a full Clash config.** The top-level is `proxies: [ ... ]` — clients (Clash Verge, Clash Meta for Android, mihomo) accept this as a "remote subscription" and fill in `proxy-groups` / `rules` from their own template. A full Clash config export is not in scope; the format is the minimal shape that delivers a working remote subscription.
- **`proxy-groups` and `rules` are intentionally NOT emitted.** Per ARCHITECTURE.md §10.2, those are a per-pool / per-client policy concern, not a per-subscription one. Clients merge the `proxies` list into their own template and apply the user-defined groups / rules there. Adding them here would force the operator's subscription to impose a single policy on every client.
- **The Clash `tls: true` flag is the simple on/off switch.** Unlike sing-box (which nests everything under `tls: { ... }`), Clash inlines the most common TLS fields (`sni:`, `alpn:`, `fingerprint:`, `client-fingerprint:`, `reality-opts: { ... }`) at the proxy root. The `tls: true` flag is set whenever `sni:` is set, so the client knows to dial TLS.
- **The Clash VLESS field set is slightly different from sing-box.** The Clash schema uses `client-fingerprint` (a Clash Meta addition) where sing-box uses `utls.fingerprint`. We emit both for compatibility: `fingerprint:` (older Clash) and `client-fingerprint:` (Clash Meta). The same `params.fingerprint` key feeds both.
- **Trojan always sets `tls: true`.** Trojan is TLS-only by protocol design. The operator can opt out via `params.skip_cert_verify` (also wired), but the Phase 0 default is the safe one.
- **Shadowsocks has no `tls:` field.** The protocol does not negotiate TLS at the outbound layer (TLS, if any, is provided by an outer transport such as h2 / grpc). The SS builder never emits `tls:`. The `TestRenderClash_NoTLSKeyForShadowsocks` test guards this.
- **Per-endpoint fail-soft.** Same contract as the base64 and sing-box renderers: an endpoint whose inbound lacks the data needed for a valid proxy entry is silently skipped. The subscription must still serve for the rest of the entitled endpoints.

## Test strategy

6 unit tests in `render_clash_test.go`, no `//go:build integration` tag. They `yaml.Unmarshal` the output back into `struct{ Proxies []map[string]any }{ yaml: "proxies" }` and assert on per-field values; this catches the most likely future regression — a typo in a YAML field name that the Clash client silently refuses to parse.

The cross-service fixture from PR #40 / #42 is reused: the per-protocol `newClashFixture` is a thin alias for `newSingboxFixture` because the two renderers take the same `ResolvedEndpoint` input. The all-four-protocols test seeds four inbounds and four hosts in a loop.

The integration test on the future `SubscriptionPgStore` will exercise the same paths against a real Postgres; the renderer is pure (no DB access) so the unit tests cover the full behaviour today.

## Compatibility

- Boot path: no changes. `cmd/aegis/main.go` already constructs `subscriptionSvc` and passes it to `router.Build`; the Clash format lands on the same Service.
- Wire format: the Clash proxy list YAML is what clients expect; no breaking change to clients that already auto-detect `application/yaml` or `text/yaml`.
- The base64 and sing-box renderers' output is byte-identical to PR #40 / #41 / #42; the standard subscription headers on the response are unchanged.
- The `go.mod` is updated to include `gopkg.in/yaml.v3` (it was already a transitive dep through `k8s.io/apimachinery` and similar; `go mod tidy` made it direct). No new top-level dependency beyond what was already pulled in.

## Follow-up

Natural next PRs in dependency order:

1. **Format variables + wildcard `*` with random salt** — render-time substitution; the renderers stay byte-stable per request, with a per-request salt cache.
2. **Multi-port inbound** — random per-fetch selection from a comma-separated port list on the inbound.
3. **XHTTP `download_settings`** — sing-box-only field referencing another host. Requires a refactor: the resolver now needs to know about *other* hosts, not just the user's entitled ones.
4. **Transport (ws / grpc / h2)** for both sing-box and Clash renderers. Both formats share the same per-endpoint transport block; the inbound's `params.transport` field is not yet wired.
5. **QR code in HTML sub-page** — uses `go-qrcode` or a pure-Go implementation; the page target=html is already wired, this PR upgrades the body.
6. **Sub-token rotation** + the `/s3cr3t-sub-<hex>` URL prefix.
7. **`SubscriptionPgStore`** — mirrors the inbounds / hosts / nodes pattern (PRs 24 / 36 / 37 / 38).
8. **Frontend** — Phase 0 placeholder; the real Vue 3 admin UI is the next big visible-to-user milestone.

## Checklist

- [x] `go vet -tags=integration ./...` clean
- [x] `go vet -tags=integration -vettool=inline ./...` clean
- [x] `gofmt -l` clean
- [x] `go build -tags=integration ./...` clean
- [x] `go test ./...` passes (6 new tests; 0 broken)
- [x] No new migrations
- [x] No new env vars
- [x] `go mod tidy` ran (no new top-level deps; `gopkg.in/yaml.v3` was already indirect)
- [x] Existing handler test updated (the 501 → 200 rename)
