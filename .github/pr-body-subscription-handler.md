## Summary

Adds the HTTP surface for the subscription package: `GET /api/v1/sub/{token}` returns the user's subscription in the requested format. The base64 format (the universal fallback) is implemented end-to-end; a minimal HTML landing page is also served; sing-box and Clash return 501 with a clear message (their renderers land in subsequent PRs).

This is PR #41, the natural next step after PR #40 (data model + base64 renderer). The router change is small: one new mount under `/api/v1/sub` and a one-argument addition to `router.Build`. The `cmd/aegis/main.go` boot path gains one parameter; no env vars, no migrations, no new dependencies.

## What's in the box

- `internal/subscription/handler.go` — `Handler` struct, `Router(svc)`, `handleRender`. The single endpoint:
  - parses the sub_token from the URL,
  - resolves the user via `Service.GetUserBySubToken` (404 on miss),
  - calls `Service.ResolveEndpointsForUser` (403 on `UserNotLiveError`),
  - dispatches by `Format` (501 for sing-box / Clash, full render for base64 / html).
- `Format` enum extended with `FormatHTML`; the renderer signature is unchanged.
- Format auto-detect: `?target=base64|singbox|clash|html` wins, then `Accept: application/yaml|application/json`, then `User-Agent` substring match against `clash|mihomo` (clash) and `sing-box|hiddify|nekobox|karing|streisand|v2box` (sing-box), then default `base64`.
- Standard subscription headers: `Profile-Title`, `Profile-Update-Interval` (24h), `Subscription-Userinfo` (built from the user's `TrafficUsedBytes` / `TrafficLimitBytes` / `ExpireAt`).
- Per-format `Content-Type` and `Content-Disposition` (the file-extension hint helps the operator spot the right format on disk: `aegis-sub.txt` for base64, `.json` for sing-box, `.yaml` for Clash).
- Minimal HTML landing page in `target=html`: lists the user, the host-line count, and the subscription URL with `target=base64` forced. No QR code, no per-client copy buttons — those land with the Phase 1 sub-page work.
- `internal/router/router.go` — mounts `subscription.Router(subscriptionSvc)` at `/api/v1/sub`. The `Build` signature gains one parameter.
- `cmd/aegis/main.go` — passes `subscriptionSvc` to `router.Build` (the `_ = subscriptionSvc` discard from PR #40 is gone).
- `internal/subscription/handler_test.go` — 12 unit tests, no integration build tag. Cover the happy path (base64 default + explicit `?target=base64`), 404, 403 (`UserNotLiveError`), 415 (`?target=garbage`), 501 (sing-box / Clash), 200 (html), the auto-detect matrix, and the two header builders.

## Design notes

- **The sub_token IS the credential.** No auth middleware on `/sub/{token}` — by design, the URL itself is the bearer. A future PR will add rate limiting and a sub-token rotation path; for now an unknown token returns 404 and a non-live user returns 403, which is the standard subscription-service contract (v2rayNG, Shadowrocket, Hiddify all behave this way).
- **Unknown `?target=` returns 415, not 501.** The auto-detect path always returns a known `Format` value; an explicit unknown value is a client bug, not a "format not yet implemented" case. The 415 body says so explicitly. Sing-box / Clash are 501 because we know the format and we have not implemented it yet.
- **`Content-Disposition` includes a file extension hint.** Some clients save the subscription to disk before parsing; a `filename="aegis-sub.txt"` lets the operator (and the user) spot the right format at a glance. Not a security feature, just a usability nudge.
- **HTML page is intentionally inline-styled and framework-free.** Same reason: it is the landing target for a phone camera in a future QR-code PR, so it must work without JavaScript and render in <100 ms. The page carries a `target=base64` URL so a user on a desktop browser can still copy the subscription string.
- **`Subscription-Userinfo` is emitted as `upload=N; download=N; total=N; expire=UNIX`.** We do not have a separate upload / download split yet (the stats pipeline is Phase 2), so we use the same `TrafficUsedBytes` value for both upload and download. The header is present and well-formed; the upload/download split is wired in when the stats pipeline is. Missing expire_at is `expire=0` rather than omitted — clients that parse the header do not have to special-case the missing-key branch.

## Test strategy

12 unit tests in `handler_test.go`, no `//go:build integration` tag. They mount the router on a local `chi.Mux` (mirroring the production layout under `/sub`) and exercise the full HTTP path: status code, headers, body shape, and the auto-detect matrix. The cross-service fixture from PR #40 is reused via `newFixture`; `newHandlerFixture` adds a one-line `chi.Mux.Mount` wrapper.

The integration suite (future `SubscriptionPgStore` PR) will exercise the same paths against a real Postgres.

## Compatibility

- Boot path: one extra parameter on `router.Build`. No env var changes. The `AEGIS_SUBSCRIPTION_BACKEND` env switch lands with the `SubscriptionPgStore` PR.
- Wire format: the base64 output is byte-identical to PR #40's `Service.RenderBase64`. The standard subscription headers are added on top; no client breaks because clients that do not understand the new headers ignore them.
- The `panel_path_config.sub_path` rotation (e.g. `/s3cr3t-sub-<hex>/<token>`) is intentionally not wired here — it depends on the `panel_path_config` table from migration 0001 and a separate `subscriptions` router mount in `main.go`. That lands in the panel-path-rotation PR.

## Follow-up

Natural next PRs in dependency order:

1. `SubscriptionPgStore` — mirrors the inbounds / hosts / nodes pattern (PRs 24 / 36 / 37 / 38).
2. `FormatSingbox` renderer (one new file in `render.go`, the per-endpoint structures in `ResolvedEndpoint` already carry everything the renderer needs).
3. `FormatClash` renderer (same shape, different wire format).
4. Sub-page `?target=html` upgrade — QR code (using `go-qrcode` or a pure-Go implementation), per-client copy buttons, "scan with phone" instructions.
5. Sub-token rotation and the `/s3cr3t-sub-<hex>` URL prefix (depends on the `panel_path_config` table).
6. Format variables (`{USERNAME}`, `{DATA_LEFT}`, `{DAYS_LEFT}`, …), wildcard `*` with random salt, multi-port random selection, XHTTP `download_settings`.

## Checklist

- [x] `go vet -tags=integration ./...` clean
- [x] `go vet -tags=integration -vettool=inline ./...` clean
- [x] `gofmt -l` clean
- [x] `go build -tags=integration ./...` clean
- [x] `go test ./...` passes (12 new tests; 0 broken)
- [x] No new migrations
- [x] No new env vars
- [x] Existing handler tests in `inbounds/`, `hosts/`, `nodes/` still pass
