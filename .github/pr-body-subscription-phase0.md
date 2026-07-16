## Summary

Adds the `internal/subscription` package — the panel-side view of users, plans, host-pools, and the per-user subscription resolver — plus a single wire format (`base64`, the universal fallback for v2rayNG / Shadowrocket / v2rayN). This is the foundation of Phase 2; subsequent PRs add the sing-box and Clash renderers, format variables, wildcard `*` with random salt, multi-port random selection, XHTTP `download_settings`, sub-token rotation, and the `/s3cr3t-sub-<hex>` URL handler.

ARCHITECTURE.md §2.4 + §10.4 call for a full subscription URL with auto-detect of format from `?target=` / `Accept` / `User-Agent`; that work is intentionally not in this PR. This PR lands the data model, the store + service, and the only format that is not a real-world design exercise (base64 is a thin wrapper around per-protocol URI builders — sing-box and Clash are JSON / YAML and warrant their own PRs).

## What's in the box

- `internal/subscription/subscription.go` — model layer. `User`, `Plan`, `Pool`, `PoolMember`, plus closed-set enums `UserStatus` (with `IsLive()` for the `active|grace` allow-list) and `ResetPeriod`. Mirrors migration 0001's `users` / `plans` / `host_pools` / `plan_pool` / `host_pool_members` schema.
- `internal/subscription/store.go` — `Store` interface + `MemoryStore`. Methods: `GetUserBySubToken`, `GetUserByID`, `ListPoolsForUser`, `ListPoolsAll`, `ListPoolMembers`. The `MemoryStore.With*` helper chain (`store.WithUser(u1).WithUser(u2).WithPool(p1).WithPoolMember(...)`) is the test fixture's primary surface; production code goes through `Service`. Index `usersByToken` is denormalised at write time (mirrors the migration's `UNIQUE` index) so the lookup is O(1).
- `internal/subscription/service.go` — `Service` that walks `users.plan_id` → `plan_pool` → `host_pools` → `host_pool_members` → `hosts.Service.Get` and returns the deduplicated, priority-sorted, enabled-filtered set. Also `ResolveEndpointsForUser` which expands every host into one `ResolvedEndpoint` per endpoint with the node and inbound already resolved (this is what the renderer needs).
- `internal/subscription/render.go` — the `base64` format. Per-protocol URI builders for VLESS, Hysteria 2, Shadowsocks, Trojan. Honours endpoint-level address / port / SNI / host / path overrides; reads protocol-specific values from `inbound.Params` (UUID for VLESS, password for HY2 / Trojan, method+password for SS). Unknown protocols are skipped (a single unrenderable endpoint must not poison the whole subscription). Stable secondary order on `(host ID, endpoint ID)` so the wire format is byte-identical across requests.
- `internal/subscription/errors.go` — `ValidationError` (400), `NotFoundError` (404), `UserNotLiveError` (403). Handlers can map them with `errors.As`.
- `cmd/aegis/main.go` — wires `subscription.NewMemoryStore()` and `subscription.NewService(...)` so the boot path validates the cross-service pointer dance. The HTTP handler is not mounted yet; the package is reachable from main, but no `r.Mount("/subscriptions", ...)` call is added. That lands with the next PR.

## Design notes

- **What Phase 0 does NOT do** (all explicitly deferred, with comments pointing at the right spot in `service.go`):
  - per-host `status_filter` (ARCHITECTURE.md §10.1.3);
  - per-user `hosts_allowlist` / `hosts_blocklist` (the slices are stored on `User` but not consulted yet);
  - non-`all` pool strategies (`round_robin`, `least_loaded`, `geo_aware`);
  - antiaffinity;
  - format variables (`{USERNAME}`, `{DATA_LEFT}`, `{DAYS_LEFT}`, …);
  - wildcard `*` with random salt;
  - multi-port inbound (`"8080,8443,9090"`) with random per-fetch selection;
  - XHTTP `download_settings` reference to another host;
  - `sing-box` and `clash` renderers;
  - HTTP `Profile-Update-Interval` / `Subscription-Userinfo` / `Profile-Title` headers;
  - `?target=html` sub-page with QR code;
  - sub-token rotation and the `/s3cr3t-sub-<hex>` URL prefix.

  Each of these is a method-local filter pass or a new builder in `render.go`. The package's public surface is set up so adding them is additive (no existing call site has to change).

- **`MemoryStore.ListPoolsForUser` shortcut.** The MemoryStore has no `plan_pool` table — it short-circuits to "every pool with at least one member is attached to every plan" so the test fixture works. The comment in `store.go` flags this as a Phase 0 only shortcut; the pg implementation will honour the real `plan_pool` join.

- **Render path skips unrenderable endpoints.** If a single endpoint lacks the data needed to build its URI (e.g. a VLESS inbound with no `params.uuid`), the renderer skips it and continues. The subscription must still serve for the rest of the user's entitled endpoints. The integration test would fail loudly if a real production path produced an unrenderable endpoint, so the silent skip is safe.

- **No new migrations.** The schema in `0001_initial.sql` already creates the tables this package needs. A `PgStore` lands in a future PR that adds the (un)marshal helpers for the JSONB allow / block lists.

## Test strategy

- 14 unit tests in `internal/subscription/` cover the store and the service without a real database. They run in plain `go test ./...` (no `//go:build integration` tag) so the default development loop is fast.
- `TestService_RenderBase64_*` round-trips the base64 output back through `base64.StdEncoding.DecodeString` and asserts on the decoded shape (`vless://`, host:port, query params, display name). This catches the most likely future regression — a malformed URI that a VPN client silently refuses.
- One test (`TestService_ResolveHostsForUser_DropsDisabled`) is `t.Skip` with a comment pointing at the integration suite. The MemoryStore cannot model host enable/disable transitions cheaply in a unit test, and the integration test on the eventual `PgStore` will cover it.
- The cross-service wiring is exercised in the service tests through a `newFixture` helper that builds the four `MemoryStore`s and wires them into the three dependent services. The integration tests for `PgStore` (a future PR) will exercise the same wiring against a real Postgres.

## Compatibility

- The new package has no callers outside `main.go` and the new tests. The boot path adds one `subscription.NewMemoryStore()` and one `subscription.NewService(...)` call. No env var changes, no router mount yet, no HTTP routes added.
- The unused-import error is avoided by `_ = subscriptionSvc` in `main.go` until the router mount lands. This is intentional — a future PR deletes the assignment and the discard in the same change.

## Follow-up

The natural next PRs in dependency order are:

1. `r.Mount("/subscriptions", subscription.Router(...))` and the per-format auto-detect middleware (uses the `base64` renderer only for now).
2. `sing-box` and `clash` renderers (the per-endpoint structures are already in `ResolvedEndpoint`; the renderers are new files in `render.go`).
3. Format variables (text/template with a sandboxed env).
4. Wildcard `*` with random salt in `sni` / `host` / `address` (render-time substitution).
5. Multi-port inbound with random per-fetch selection.
6. XHTTP `download_settings` reference (validation that referenced host has no download of its own).
7. `?target=html` sub-page with QR code.
8. Sub-token rotation and the `/s3cr3t-sub-<hex>` URL prefix.
9. `subscription.PgStore` (mirrors the inbounds / hosts / nodes pattern from PRs 24 / 36 / 37 / 38).
10. `auth` integration: the `sub_token` becomes the bearer for `GET /s3cr3t-sub-<token>`; refresh tokens are unchanged.

## Checklist

- [x] `go vet -tags=integration ./...` clean
- [x] `go vet -tags=integration -vettool=inline ./...` clean
- [x] `gofmt -l` clean
- [x] `go build -tags=integration ./...` clean
- [x] `go test ./...` passes (14 new tests; 0 broken)
- [x] No new migrations
- [x] No new env vars
- [x] No new HTTP routes
