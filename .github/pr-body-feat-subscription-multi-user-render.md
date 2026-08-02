feat(subscription): per-user credential render (Phase 2 step 4, multi-user sub URL)

Closes the v0.7.2 KNOWN_LIMITATIONS entry
"Phase 2 multi-user sing-box render — Phase 2"
step 4 (subs + cabinet). The `GET /sub/{token}`
handler now uses the per-(user, inbound)
credential from the `user_inbound_credentials`
table (PR #167) as the protocol auth material in
the rendered sing-box and Clash outbounds. The
"cabinet" surface is the same per-user sub URL
(separate auth model for a future end-user
facing cabinet is a v0.8+ project, not in
Phase 2 scope).

## What lands

- `backend/internal/subscription/service.go`:
  - `Service` struct gains `creds *credentials.Service`
    (Phase 2 multi-user render source) and
    `userCreds map[uuid.UUID]credentials.Credential`
    (per-render cache populated by
    `precomputeUserCreds`).
  - `WithCreds(svc *credentials.Service) *Service`
    setter — nil-safe, mirrors the `WithAudits`
    (PR #166) and `WithWebhooks` (PR #148) pattern.
  - `precomputeUserCreds(ctx, u)` — ONE call to
    `s.creds.ListByUser(ctx, u.ID)` per render,
    builds a per-inbound map for O(1) lookups in
    the per-endpoint builders. Per-render query
    failures are fail-soft (log + cache empty;
    the per-endpoint builders fall back to the
    v0.7.2 params-based path).
  - `userCredFor(inboundID)` — returns the
    per-(user, inbound) credential value, or ""
    when the user has no credential for this
    inbound. The "" return is the v0.7.2 fallback
    signal.
- `backend/internal/subscription/render_singbox.go`:
  - `RenderSingbox` now takes `ctx context.Context`
    (was `_`); pre-computes the per-render creds
    map; threads the per-endpoint userCred into
    `renderSingboxOutbound` and the per-protocol
    builders (`buildSingboxVLESS`, `buildSingboxHysteria2`,
    `buildSingboxTrojan`).
  - Each per-protocol builder uses the userCred
    when non-empty, falls back to
    `params["uuid"]` / `["password"]` when empty.
    Shadowsocks is unchanged (single-password
    protocol by design; the per-user cred is
    ignored on purpose, documented inline).
- `backend/internal/subscription/render_clash.go`:
  - Same change pattern as the sing-box renderer.
    `buildClashVLESS`, `buildClashHysteria2`,
    `buildClashTrojan` take a `userCred` parameter
    with the same Phase 1 / Phase 2 split.
- `backend/internal/subscription/render_phase2_test.go`
  (new, ~200 lines):
  - 4 tests cover the Phase 2 path end-to-end:
    `TestRenderSingbox_Phase2_UsesUserCredential`
    (user-specific UUID appears in the rendered
    outbound, operator's params[uuid] does NOT),
    `TestRenderSingbox_Phase2_FallsBackToParams`
    (a user with no per-inbound credential still
    gets a working sub URL via params — migration
    safety), `TestRenderClash_Phase2_UsesUserCredential`
    (mirror for Clash), and
    `TestRenderSingbox_Phase2_OtherUserCredNotLeaked`
    (auth boundary: user A's credential is not
    used when rendering user B's sub URL).
- `backend/internal/app/app.go`:
  - `a.Subs.WithCreds(a.Credentials)` after the
    subscription Service is constructed.

## Why this PR is the cabinet (for Phase 2)

The summary's "subs + cabinet" step covers the
per-user surface. Today the only per-user surface
is the public `GET /sub/{token}` endpoint — the
sub_token IS the credential, and the rendered
config is the "cabinet" the user sees. A separate
end-user-facing cabinet (with login UI, sub URL
fetch, traffic stats, plan change) is a v0.8+
project with its own auth model; it lives in the
`internal/cabinet` doc.go-only package. For Phase
2, the per-user sub URL is the cabinet.

## Design choices

- **Per-render query, not per-endpoint query** —
  ONE `ListByUser` call per render (not one per
  inbound). The per-endpoint builders do O(1)
  lookups in a pre-computed map. K inbound renders
  become 1 DB round-trip, not K.
- **Fail-soft on per-render query failure** —
  transient pg blips do not break every user's
  sub URL. The renderer logs the error (the
  `RenderSingbox` body has the per-render try
  site) and continues with an empty creds map;
  every per-endpoint builder falls back to
  `params["uuid"]` / `["password"]`. This matches
  the PR #169 Builder's per-inbound fail-soft
  policy.
- **Phase 1 / Phase 2 split at the builder level** —
  each per-endpoint builder has the same shape:
  "use the userCred if non-empty, else fall back
  to params". The builder does not need to know
  which path was taken; the dispatcher
  (`renderSingboxOutbound`) threads the value
  through.
- **Shadowsocks stays single-password** — the
  per-user cred is ignored on the dispatch path
  (the comment in `renderSingboxOutbound`
  documents why). A Shadowsocks inbound with
  per-user credentials still renders the same
  config for every user (the inbound's `method`
  and `password` from `params`).
- **`WithCreds(nil)` is the v0.7.2 path** —
  pre-compute is a no-op, the cache stays empty,
  every per-endpoint builder falls back to
  params. The existing test suite (which does
  not install `WithCreds`) keeps the v0.7.2
  output byte-for-byte.

## Pre-fetch trade-off

None. The single `ListByUser` per render is the
same query cost the operator's auth handler pays
on every sub URL request. The v0.7.2 path did
NO credential lookup (it just read `params`); the
Phase 2 path adds one DB round-trip per render.
This is acceptable because the round-trip
is O(1) per user (not per inbound), the
`user_inbound_credentials` table is small per
user (typically < 10 rows), and the sub URL is
rate-limited per sub_token (the existing rate
limiter in the handler), so the cumulative query
load is bounded.

## Phase 2 multi-user — status (after this PR)

- PR 1 (data model) — #167 (closed)
- PR 2 (renderer) — #168 (closed)
- PR 3 (builder + BatchedApplier narrow) — #169 (closed)
- **PR 4 (subs) — THIS PR** (opens; closes Phase 2)

Phase 2 multi-user render is end-to-end. The
panel can now issue per-user credentials via
the admin surface (a follow-up HTTP PR; not
in this PR's scope), and the running config
plus sub URL both pick up the per-user
credential automatically.

## Tests

- 4 new tests (Phase 2 path) + 0 existing test
  updates. Existing 25/25 unit packages green
  after the wiring change.
- `go vet -tags=integration ./...` clean.
- `golangci-lint v2` 0 issues.
- `gofmt` clean.

## File map

- `backend/internal/subscription/service.go`
  (modified, plus 80 lines)
- `backend/internal/subscription/render_singbox.go`
  (modified, plus 35 lines)
- `backend/internal/subscription/render_clash.go`
  (modified, plus 30 lines)
- `backend/internal/subscription/render_phase2_test.go`
  (new, ~200 lines)
- `backend/internal/app/app.go` (modified, plus
  8 lines)
- `.github/pr-body-feat-subscription-multi-user-render.md`
  (new)
