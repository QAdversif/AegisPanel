feat(cores): multi-user sing-box renderer (Phase 2 step 2)

Changes the sing-box renderer's per-protocol signatures to
accept a per-(user, inbound) credential list (PR 1 of
Phase 2 multi-user, the data model, was #167). The
multi-user path is gated on the Experimental map
containing a new `inbound_credentials` key; Phase 1
deployments (which do not populate the key) fall back
to the existing params-based single-user path with
zero behavior change.

Closes the v0.7.2 KNOWN_LIMITATIONS entry
"Phase 2 multi-user sing-box render — Phase 2" step 2
(renderer signature).

## What lands

- `backend/internal/cores/singbox/render.go`:
  - New constant `ExperimentalInboundCredentialsKey = "inbound_credentials"` (the Experimental map key the Builder will populate in PR 3).
  - `Provider.RenderConfig` extracts the per-tag credential list from `cfg.Experimental` and passes the per-tag slice to `renderInbound`. The extraction helper `extractCredentialsByTag` is defensive: missing key, wrong-typed top-level value, and wrong-typed per-tag value all fall through to the Phase 1 fallback rather than crash the renderer.
  - `renderInbound` dispatch updated to thread the per-tag credential slice to VLESS, HY2, and Trojan. Shadowsocks dispatch is unchanged (single-password protocol, no per-user concept).
- `backend/internal/cores/singbox/protocols.go`:
  - `renderVLESS(spec, params, users)`, `renderHY2(spec, params, users)`, `renderTrojan(spec, params, users)`: new third arg `[]credentials.Credential`. When non-empty, the renderer emits a `users: [{name, uuid|password}, ...]` array of length N. When empty (Phase 1), the renderer falls back to `params["uuid"]` (VLESS) or `params["password"]` (HY2 / Trojan) and emits a length-1 array.
  - `renderShadowsocks(spec, params)` is unchanged. Shadowsocks 2022-blake3 is a single-password protocol by design; per-user credentials do not apply.
- `backend/internal/cores/singbox/render_test.go`:
  - 5 new tests covering the Phase 2 path: `TestRenderConfig_VLESSMultiUser_Phase2` (2 users, flow propagates to every user), `TestRenderConfig_HY2MultiUser_Phase2` (3 users, no Phase 1 fallback value), `TestRenderConfig_TrojanMultiUser_Phase2` (1 user), `TestRenderConfig_MixedPhase1AndPhase2` (per-tag fallback), `TestRenderConfig_CredentialsWrongType_FallsBackToParams` (defensive path for Builder bugs).
  - Existing 14 tests pass unchanged. The Phase 1 behavior is byte-identical because no test populates the new key.

## Why this is a zero-behavior-change refactor

The signature change is what makes this PR worth its own commit: it lands the seam for the Phase 2 multi-user path before the Builder learns to populate it. PR 3 (next slice) will make `internal/cores/builder/builder.go` query the credentials table per inbound tag, populate `cfg.Experimental[ExperimentalInboundCredentialsKey]`, and the sing-box renderer will start emitting multi-user `users: [...]` arrays automatically — no further renderer changes needed.

Phase 1 deployments do not populate the new key. The renderer's defensive `extractCredentialsByTag` returns `nil` for the missing-key case, `renderInbound` receives an empty `users` slice, and the per-protocol renderers fall back to the existing `params["uuid"]` / `params["password"]` path. Byte-identical output to v0.7.2 and earlier.

## Per-user `name` choice

In Phase 2, each user entry's sing-box `name` field is set to `c.UserID.String()`. sing-box uses `name` as a display label (not an auth identity); the actual auth material is `uuid` (VLESS) or `password` (HY2 / Trojan). Using `UserID.String()` guarantees uniqueness across users in the same inbound. In Phase 1 the name fell back to the inbound tag, which was the only "name" available without a UserID; the fallback is preserved when `users` is empty.

## Test fixture API contract: `map[string]any` at the top level

The credentials Experimental value is `map[string]any` (not `map[string][]credentials.Credential`) with each value being `[]credentials.Credential`. The renderer's type-assertion on the top-level map (`raw.(map[string]any)`) matches the existing `inbound_params` pattern. A typed map of slice values would fail that assertion because Go reflection treats `map[string]any` and `map[string][]T` as distinct types even when the slice element type matches. The Builder in PR 3 will use the same `map[string]any` shape; this is documented inline in the test fixture's `multiUserCfg` doc comment.

## Why Shadowsocks is single-password by design

Shadowsocks 2022-blake3 (the only AEAD method sing-box 1.8+ accepts) is a single-password protocol. Every client connecting to a Shadowsocks inbound shares the same auth material. There is no per-user concept in the sing-box schema — sing-box does not have a `users: [...]` array for Shadowsocks inbounds; it has `method` and `password` at the top level. Operators who want per-user auth on a Shadowsocks inbound should pick VLESS or Trojan instead. The Phase 2 multi-user work does not change this; the Shadowsocks signature stays `(spec, params)`.

## Follow-up PRs

- **PR 3 (builder)**: `internal/cores/builder/builder.go` queries the credentials table per inbound tag (filtered by `users.HostsAllowlist` / `HostsBlocklist`), populates `cfg.Experimental[ExperimentalInboundCredentialsKey]`. `users.Service.enqueueUserDelta` narrows to nodes matching `HostsAllowlist` instead of fan-out to all nodes.
- **PR 4 (subs and cabinet)**: subscription service renders per-user config URL; cabinet endpoints to view and manage own credentials.

## Tests

- 33 of 33 singbox tests pass (28 existing + 5 new Phase 2).
- 25 of 25 unit packages green.
- `go vet -tags=integration ./...` clean.
- `golangci-lint v2` 0 issues (after one `#nosec G101` for the constant name containing the substring "credentials"; the constant value is an Experimental-map key, not a credential).
- `gofmt` clean.

## File map

- `backend/internal/cores/singbox/render.go` (modified, plus 80 lines)
- `backend/internal/cores/singbox/protocols.go` (modified, three per-protocol renderers updated)
- `backend/internal/cores/singbox/render_test.go` (modified, plus 220 lines of Phase 2 tests)
- `.github/pr-body-feat-cores-multi-user-renderer.md` (new)
