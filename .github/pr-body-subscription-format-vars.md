## Summary

Adds format-variable substitution and a wildcard `*` salt to the subscription renderers (ARCHITECTURE.md §10.1.1 and §10.1.2). The `remark` / `address` fields on a Host and the `SNI` / `Host` arrays on each Endpoint may now contain `{VARIABLE}` placeholders or a literal `*`; the renderer substitutes them at fetch time.

This is PR #44, the natural next step after the Clash renderer (PR #43). The base64 / sing-box / Clash renderers are all updated to call the new enricher before per-endpoint rendering; the per-endpoint URI / JSON / YAML builders themselves are unchanged.

## What's in the box

- `internal/subscription/render_vars.go` — the substitution engine. `RenderContext{Salt, Vars}`, `BuildRenderContext(u, h, now)`, `computeSalt(hostID, userID, now)` (sha256 minute-bucket, 8 hex chars), `buildUserVars(u)` (10 closed-set variables), `formatBytes` / `formatDaysLeft` / `formatUsagePercent` / `statusEmoji` helpers, `applyFormatVariables` (string-replace, unknown placeholders left intact), `applyWildcardSalt`, `enrichEndpoint` (per-host salt + per-endpoint `PROTOCOL` / `SERVER_IP` injection), `applyWildcardToStringSlice` / `applyFormatVariablesOnSlice`.
- `internal/subscription/render.go` — `Service.newRenderContext(u)` (nil-safe). `RenderBase64` enriches the endpoint slice before sort + render.
- `internal/subscription/render_singbox.go` — `RenderSingbox` enriches before per-endpoint builder.
- `internal/subscription/render_clash.go` — `RenderClash` enriches before per-endpoint builder.
- 12 new unit tests in `render_vars_test.go` covering the salt function, the per-user variable map, the helpers, `enrichEndpoint`, the wildcard-on-slice path, and a render-time round-trip through each of the three renderers.

## Format variables

| Placeholder         | Substituted with                                          |
| ------------------- | --------------------------------------------------------- |
| `{USERNAME}`        | `user.Username`                                           |
| `{DATA_USAGE}`      | `formatBytes(user.TrafficUsedBytes)` — "12.34 GB"         |
| `{DATA_LIMIT}`      | `formatBytes(user.TrafficLimitBytes)` — "∞" if zero      |
| `{DATA_LEFT}`       | `formatBytes(limit - used)` — "∞" if limit is zero        |
| `{DAYS_LEFT}`       | days until `expire_at` — "∞" if no expire                 |
| `{EXPIRE_DATE}`     | Gregorian date string (e.g. "2026-08-15")                 |
| `{STATUS_EMOJI}`    | ✅ (active) / ⌛ (grace) / 🪫 (exhausted) / ❌ (disabled) / 🔌 (disconnected) |
| `{USAGE_PERCENTAGE}` | integer percent of limit used (capped at 100)             |
| `{PROTOCOL}`        | the inbound's `protocol` (vless / hysteria2 / etc.)      |
| `{SERVER_IP}`       | the node's `address` (first segment)                     |

Unknown placeholders are left intact (`{XYZ}` stays as `{XYZ}`) so the operator can see what they typed in the subscription rather than a confusing empty string. This matches the convention used by the popular Russian panel projects (Marzban, 3X-UI).

The `{PROTOCOL}` and `{SERVER_IP}` variables are **per-endpoint**, not per-user — they are substituted inside `enrichEndpoint`, where the inbound's protocol and the node's address are in scope. The remaining 8 variables are per-user and live in `RenderContext.Vars`.

## Wildcard salt

The `SNI`, `Host`, and `address` fields may contain a literal `*`. On each fetch, the `*` is replaced with an 8-character hex salt derived from

```
sha256(host_id || user_id || fetch_minute)  →  first 8 hex chars
```

The minute-bucket makes the salt stable for one minute — clients that re-fetch within the same minute get the same SNI, which avoids breaking in-flight connections; clients that wait longer get a fresh salt. 8 hex characters is 32 bits of entropy, enough to defeat DPI heuristic-fingerprinting on a per-fetch basis (DPI sees the union of all clients in a 60s window, not a single one).

The salt is **per-host**: a user on two different hosts gets two different salts. This is intentional — if both salts were per-user, the salt would only rotate on user identity, not on host, and DPI could correlate the same SNI across hosts. Computing the salt inside `enrichEndpoint` (where the host id is in scope) keeps the per-host semantics without forcing a per-user global salt that the renderer would then have to override.

## Design notes

- **String-replace, not text/template.** The substitution is a single-pass `strings.ReplaceAll` for each known variable. Using `text/template` would mean a `*template.Template` per fetch, a `map[string]any` per execution, and a goroutine-safe parse-cache. The variable set is closed (10 entries), the input strings are short (host remark is a single line), and the substitution is per-fetch — the simpler implementation is the right one.
- **Unknown placeholders are left intact.** A `strings.NewReplacer(pairs...).Replace(input)` would silently erase typos. `applyFormatVariables` checks `vars[placeholder]` and only replaces when the key exists; the input string is otherwise untouched.
- **The `*` is replaced AFTER `{VARIABLE}` substitution.** A `{VARIABLE}` value that happens to contain a literal `*` is the operator's problem; the salt engine does not run a second pass.
- **The HTML renderer is not updated.** The HTML page renders the user-facing welcome string, not a per-endpoint connection string; the variable substitution is for connection-string fields only. The `renderHTML` path is left unchanged.
- **The renderers are unchanged.** `enrichEndpoint` produces a new `ResolvedEndpoint` whose `Host.Remark` / `Host.Address` / `Endpoint.Address` / `Endpoint.SNI` / `Endpoint.Host` already carry the substituted values. The base64 / sing-box / Clash builders call `displayName(host)` and `effectiveAddress(ep)` and see the post-substitution string — no changes to those functions.
- **No new env vars, no new migrations, no new HTTP endpoints.** The renderer is the only thing that changes; the handler / router / store layer is untouched.

## Test strategy

12 unit tests in `render_vars_test.go` (no `//go:build integration` tag):

- `TestComputeSalt_StableWithinMinute` — same inputs at `T+0s` and `T+30s` produce the same salt; `T+90s` produces a different salt.
- `TestComputeSalt_DifferentInputsDifferentOutput` — different `(hostID, userID)` tuples produce different salts.
- `TestBuildUserVars_AllFieldsPopulated` — every variable in the closed set is present in the map; the values are the expected formatted strings.
- `TestBuildUserVars_NoExpire` — `DAYS_LEFT` and `EXPIRE_DATE` are "∞" / empty when `ExpireAt` is zero.
- `TestStatusEmoji` — one assertion per `UserStatus` value.
- `TestFormatBytes` / `TestFormatDaysLeft` / `TestFormatUsagePercent` — boundary cases (0, exactly-1, 1 GiB, exhausted 100%, over 100% cap).
- `TestEnrichEndpoint_AppliesWildcardAndPerEndpointVars` — `*` in `Host.Address` and `{PROTOCOL}` / `{SERVER_IP}` in `Host.Remark` are both substituted; `{USERNAME}` (a per-user var) is also substituted; unknown `{XYZ}` is left intact.
- `TestApplyWildcardToStringSlice` — `[]string{"a.*", "b.*"}` becomes `[]string{"a.deadbeef", "b.deadbeef"}` with the same salt.
- `TestRenderVars_RoundTrip_Base64` / `TestRenderVars_RoundTrip_Singbox` / `TestRenderVars_RoundTrip_Clash` — the renderers accept a pre-enriched endpoint slice and produce the expected output. These are the cross-renderer regression nets: any future refactor that bypasses `enrichEndpoint` trips one of them.

The integration test on the future `SubscriptionPgStore` will exercise the same paths against a real Postgres; the substitution engine is pure (no DB access) so the unit tests cover the full behaviour today.

## Compatibility

- Boot path: no changes. `cmd/aegis/main.go` already constructs `subscriptionSvc`; the substitution engine lives inside the existing `Service` and is enabled automatically.
- Wire format: the rendered base64 / sing-box / Clash outputs are byte-identical to PR #40 / #41 / #42 / #43 when the operator's host and endpoint fields contain no `{VARIABLE}` or `*`. The substitution is a no-op for the empty-template case.
- The standard subscription headers on the response are unchanged. The `Profile-Title` / `Profile-Update-Interval` / `Subscription-Userinfo` headers stay byte-stable.

## Follow-up

Natural next PRs in dependency order:

1. **Multi-port inbound** — random per-fetch selection from a comma-separated port list on the inbound.
2. **XHTTP `download_settings`** — sing-box-only field referencing another host. Requires a refactor: the resolver now needs to know about *other* hosts, not just the user's entitled ones.
3. **QR code in HTML sub-page** — `go-qrcode` or pure-Go; the `?target=html` page is already wired, this PR upgrades the body.
4. **Sub-token rotation** + the `/s3cr3t-sub-<hex>` URL prefix.
5. **`SubscriptionPgStore`** — mirrors the inbounds / hosts / nodes pattern (PRs 24 / 36 / 37 / 38).
6. **Frontend** — Phase 0 placeholder; the real Vue 3 admin UI is the next big visible-to-user milestone.

## Checklist

- [x] `go vet -tags=integration ./...` clean
- [x] `go vet -tags=integration -vettool=inline ./...` clean
- [x] `gofmt -l` clean
- [x] `go build -tags=integration ./...` clean
- [x] `go test ./...` passes (12 new tests; 0 broken)
- [x] No new migrations
- [x] No new env vars
- [x] No new top-level dependencies
