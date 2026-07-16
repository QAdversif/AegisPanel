## Summary

Two rotation features land in this PR:

1. **Sub-token rotation** — operators can rotate a user's `sub_token`. The previous token stays valid for 24h (a configurable grace), so the user has time to re-import the new URL on every device. The `GetUserBySubToken` lookup chain tries the current token first, then the previous one (when present and not yet expired).
2. **URL prefix rotation** — a new `panel_path_config` table holds the active sub_path (a 16-char hex prefix, e.g. `s3cr3t-sub-aabbccdd1234eeff`). The router mounts the subscription handler at `https://panel/<sub_path>/sub/<token>` in addition to the default `/api/v1/sub/<token>`. A rotation makes the old URL stop working immediately (the 3X-UI convention) and the new URL take over.

This is PR #47, the natural next step after the QR-code HTML sub-page (#46).

## What's in the box

### Sub-token rotation

- Migration `0011_user_sub_token_prev.sql` — adds `users.sub_token_prev TEXT NULL` and `users.sub_token_prev_expires_at TIMESTAMPTZ NULL`. A partial UNIQUE index on `sub_token_prev` (WHERE sub_token_prev IS NOT NULL) keeps the lookup O(log n) for the rotated users and zero-cost for everyone else.
- `internal/subscription/subscription.go` — `User` model gains `SubTokenPrev` and `SubTokenPrevExpiresAt`.
- `internal/subscription/store.go`:
  - `usersByPrevToken` denormalised index, kept consistent with `usersByToken` by `WithUser` and `UpdateSubToken`.
  - `GetUserByPrevSubToken(token)` returns the user whose `sub_token_prev` matches.
  - `UpdateSubToken(userID, newToken, prevExpiresAt)` rotates atomically: drops the earlier primary + earlier prev from their indexes, moves the current primary into prev, installs the new primary, bumps `SubTokenRotatedAt`.
- `internal/subscription/service.go`:
  - `GetUserBySubToken` is now a two-step lookup chain. On primary miss it tries the prev token; the prev lookup is valid only when `SubTokenPrevExpiresAt` is set and in the future (the Service enforces the grace window; the Store is clock-agnostic).
  - `RotateSubToken(userID, grace)` generates a fresh 32-char hex token, sets the old token as prev with the supplied grace, and bumps `SubTokenRotatedAt`. Default grace is 24h (`DefaultSubTokenRotationGrace`), matching the 3X-UI convention.
- `internal/subscription/rotation_test.go` — 6 unit tests covering: fresh-token generation, current-token lookup, prev-token lookup during grace, prev-token rejection after grace, prev-token rejection with zero grace, and double-rotation consistency (the second rotation drops the original prev from the index).

### URL prefix rotation

- Migration `0010_panel_path_config.sql` — single-row table seeded with the empty `sub_path` (the "no rotation" default). The row id is fixed (`00000000-0000-0000-0000-000000000001`) so the Service can read the active row without a `List`.
- New `internal/panelcfg` package:
  - `config.go`: `SubPathConfig` model; `NewRandomSubPath` (16 hex chars, 8 random bytes); `ValidatePath` (`[a-z0-9-]+`, 4-64 chars).
  - `store.go`: `Store` interface (`GetActive`, `GetByID`, `SetActive`, `Reset`) and `MemoryStore` implementation with the "at most one active row" invariant enforced in `SetActive` / `Reset`.
  - `service.go`: `Service` with `GetActive`, `Rotate` (random path), `RotateTo` (operator-chosen path), `Reset` (back to default). The grace window is optional; the default is zero (immediate cutover, 3X-UI convention).
  - `panelcfg_test.go` — 9 unit tests covering path validation, random generation, the default row, rotation, grace windows, and Reset.
- `internal/router/router.go`:
  - The Build function takes a new `*panelcfg.Service` parameter.
  - The default mount at `/api/v1/sub/<token>` is unchanged.
  - When the active sub_path is set (non-empty), a second mount at `/<sub_path>/sub/<token>` is added at the top level (NOT under `/api/v1` — the sub_path IS the top-level prefix).
  - The sub_path is read once at Build time; Phase 1 will add a TTL cache so a rotation takes effect without a router restart.
- `internal/router/router_test.go` — 4 unit tests covering: default path always works, rotated path works, the two mounts coexist, and the empty default does NOT mount a stray `/sub/sub/<token>`.
- `cmd/aegis/main.go` — wires the panelcfg service (MemoryStore default; `AEGIS_PANELCFG_BACKEND=pg` lands with the future PgStore).

## Design notes

- **Sub-token rotation is atomic at the store level.** The `UpdateSubToken` method updates the indexes in the same critical section, so a reader that holds the read-lock at the same time either sees the old or the new state — never a half-applied rotation.
- **The grace window is enforced by the Service, not the Store.** The Store is clock-agnostic; the Service checks `SubTokenPrevExpiresAt.After(s.now())` after the Store returns a hit. Tests can pin a specific clock to assert on the exact behaviour.
- **The `panel_path_config` table is a single-row config.** Phase 2+ per-tenant paths land in a separate `panel_path_config_tenant` table that references this one.
- **The default mount stays live after a rotation.** Both `/api/v1/sub/<token>` and `/<sub_path>/sub/<token>` are mounted; the operator can choose to deprecate the default later by simply not running a rotation again. The 3X-UI panel does NOT deprecate the default — the rotated path is an additional mount, not a replacement.
- **The empty sub_path is the "no rotation" signal.** The router does NOT mount at `/sub/sub/<token>` (which would be a URL mistake); the mount is skipped when `active.SubPath == ""`. A `Reset` call clears the rotated row and re-activates the empty default.
- **The lookup chain is fail-soft on the surface.** Both 404s (no such user, no such token) surface as 404 to the caller — the user cannot tell whether they typed the wrong token or the operator has not yet created the account. This is the standard "don't leak existence" rule.
- **Sub-token rotation is per-user; URL prefix rotation is panel-wide.** They are independent: rotating the sub_path does NOT invalidate the users' sub_tokens (the path and the token are separate). Rotating a single user's sub_token does NOT rotate the panel's sub_path.

## Compatibility

- Boot path: no migration in `main.go` other than the new panelcfg service wiring. The default mount at `/api/v1/sub/<token>` is unchanged.
- Wire format: subscription bodies (base64 / singbox / Clash / html) are byte-identical to PRs #40-46. The new sub_path mount serves the same bodies; only the URL changes.
- Storage: two new migrations (0010, 0011) — both additive (no DROP TABLE, no schema-destructive ALTER).
- HTTP: the rotated mount is a fresh path, not a change to an existing one. Existing clients pointing at `/api/v1/sub/<token>` keep working.

## Follow-up

Natural next PRs in dependency order:

1. **Transport (ws / grpc / h2) for both sing-box and Clash renderers** — the sing-box renderer still emits no `transport` block; `params.transport` is read here for the XHTTP gate but not yet wired for the actual transport object.
2. **`SubscriptionPgStore`** — mirrors the inbounds / hosts / nodes pattern (PRs 24 / 36 / 37 / 38).
3. **`PanelCfgPgStore`** — mirrors the in-memory panelcfg store as a pgx-backed implementation. A `AEGIS_PANELCFG_BACKEND=pg` env switch is the same pattern as the other backends.
4. **TTL cache for the rotated sub_path mount** — the router currently reads the active sub_path at Build time. A `sync.Once`-style cache with a 60s TTL would let the operator rotate without restarting the panel.
5. **Admin endpoints for sub_path rotation** — `POST /api/v1/admin/panel/sub-path/rotate` (random) and `POST /api/v1/admin/panel/sub-path/rotate-to` (operator-chosen). Phase 2+ admin UI.
6. **Admin endpoints for sub_token rotation** — `POST /api/v1/admin/users/{id}/sub-token/rotate`. The user-facing version lands with the cabinet UI.
7. **Frontend** — Phase 0 placeholder; the real Vue 3 admin UI is the next big visible-to-user milestone.

## Checklist

- [x] `go vet -tags=integration ./...` clean
- [x] `gofmt -l` clean (LF form, which is what CI sees)
- [x] `goimports -l -local github.com/QAdversif/AegisPanel` clean
- [x] `go build -tags=integration ./...` clean
- [x] `go test ./...` passes (6 rotation tests + 9 panelcfg tests + 4 router tests; 0 broken)
- [x] `staticcheck ./...` clean
- [x] `gocritic` clean for new code
- [x] Two new migrations (0010, 0011), both additive
- [x] No new env vars
- [x] No new top-level dependencies
