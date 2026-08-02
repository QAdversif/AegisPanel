feat(credentials+cores): wire credentials through builder and narrow BatchedApplier fan-out (Phase 2 step 3)

Wires the `user_inbound_credentials` table (PR 1
data model, #167) into the sing-box pipeline
end-to-end. The Builder now populates
`cfg.Experimental[ExperimentalInboundCredentialsKey]`
for every enabled inbound, and
`users.Service.enqueueUserDelta` narrows the
BatchedApplier fan-out by the user's
`HostsAllowlist` / `Blocklist`. The sing-box
renderer signature already accepts the
per-inbound credential list (PR 2, #168), so
this PR lands the "panel-side wiring" seam
without further renderer changes.

Closes the v0.7.2 KNOWN_LIMITATIONS entry
"Phase 2 multi-user sing-box render — Phase 2"
step 3 (builder + BatchedApplier narrow). Step 4
(subs + cabinet) is the only remaining slice.

## What lands

- `backend/internal/cores/builder/builder.go`:
  - New `ListCredentialsByInbound` source
    interface (matches `credentials.Store` and
    `credentials.Service` shapes — returns
    `[]*credentials.Credential`).
  - `BuildCoreConfigForNode` and `NewFlushFn`
    take a `credSrc ListCredentialsByInbound`
    parameter. For each enabled inbound, the
    builder calls `ListByInbound(ctx, inb.ID)`,
    dereferences the `*Credential` slice to a
    value slice (the sing-box renderer's
    `extractCredentialsByTag` type-assertion
    requires a value slice, see PR 2 for the
    rationale), and populates
    `cfg.Experimental["inbound_credentials"][tag]`.
  - Per-inbound query failures are logged at
    WARN level and treated as "no credentials
    for this inbound" — the sing-box renderer's
    Phase 1 fallback path takes over. A fatal
    error here would prevent any node from
    rendering during a transient pg blip, which
    is the wrong failure mode for the
    BatchedApplier's 20s flush window.
  - A nil `credSrc` is a valid "Phase 1
    fallback" state — the builder produces a
    CoreConfig with an empty
    `inbound_credentials` map; the sing-box
    renderer falls back to params-based
    single-user for every inbound.
  - Empty per-inbound result (`len(creds) == 0`)
    skips the per-tag entry (the sing-box
    renderer's Phase 1 fallback path).
- `backend/internal/cores/builder/builder_test.go`:
  - 4 new tests cover the credentials path
    (headline multi-user shape, nil credSrc
    defensive, per-inbound query error, empty
    list = Phase 1 fallback). 4 existing tests
    updated to pass the new `credSrc` parameter.
  - 1 new `fakeCredentialsSource` test double
    (mirrors the existing `fakeInboundsSource`).
- `backend/internal/cores/builder/flushfn_smoke_test.go` +
  `flushfn_integration_test.go`:
  - 2 call sites updated to pass `nil` for
    `credSrc`. The smoke / integration paths do
    not exercise the credentials flow (they
    cover the Apply transport, not the panel
    pipeline).
- `backend/internal/cores/batched.go`:
  - New `QueueLen()` method on `BatchedApplier`.
    Returns the depth of the input channel.
    Used by the new fan-out tests; also a
    future enqueue-pressure metric.
- `backend/internal/users/service.go`:
  - `enqueueUserDelta(d, user)` — added the
    `user *User` parameter; filters the
    BatchedApplier map by `user.HostsAllowlist`
    and `user.HostsBlocklist`. Blocklist wins
    over allowlist (a node in BOTH is skipped).
    Empty allowlist + empty blocklist fans out
    to every applier (the v0.5.0 default-allow
    behaviour; a panel that has not yet
    populated the allowlist keeps its existing
    fan-out).
  - 4 call sites updated to pass the user
    (`out` for Create / Update / RotateSubToken,
    `cur` for Delete — the pre-delete user has
    the right allowlist for the fan-out filter).
- `backend/internal/users/batchapplier_fanout_test.go`
  (new, 178 lines):
  - 5 tests cover the fan-out filter (default
    allow, allowlist narrows, blocklist
    excludes, allow + block → block wins, nil
    user → default allow). All 5 green.
- `backend/cmd/aegis/main.go`:
  - `builder.NewFlushFn` call site updated to
    pass `a.Credentials` as the credentials
    source (a `*credentials.Service`).

## Design choices

- **`Builder does not filter by user allowlist`** —
  the Builder fetches every credential for the
  inbound and includes it in the rendered config.
  The user-level filter is `users.Service.enqueueUserDelta`
  (which decides WHICH nodes get a FlushFn
  re-render). The `HostsAllowlist` / `Blocklist`
  semantic for the Builder requires a
  host-to-inbound mapping the panel does not yet
  have; the model today treats `HostsAllowlist`
  as "node IDs" for the BatchedApplier fan-out
  (per the docstring in `enqueueUserDelta`). A
  future PR that adds a host-to-inbound mapping
  can re-introduce the Builder-side filter.
- **`enqueueUserDelta` uses a nil-allowlist
  default-allow semantic** — an empty
  `HostsAllowlist` AND empty `HostsBlocklist`
  fans out to every applier. This is the v0.5.0
  behaviour; a panel that has not yet populated
  the allowlist keeps its existing fan-out. The
  migration path is "populate the allowlist
  gradually as you onboard users; the system
  keeps working until you do".
- **Blocklist wins over allowlist** — a node in
  BOTH lists is skipped. The blocklist is
  authoritative for "this user must NEVER touch
  this node". This matches the principle of
  "deny beats allow".
- **`nil` user falls back to default-allow** —
  defensive. The four call sites always pass a
  non-nil user, but a caller bug should not
  silently drop deltas.
- **`*Credential` → value slice conversion
  happens in the Builder, not at the boundary**
  — the credentials.Service / Store API uses
  the canonical Go `*Credential` shape (rows
  the caller might mutate); the Builder
  dereferences once per FlushFn call to the
  value slice the sing-box renderer's
  `extractCredentialsByTag` requires. The
  conversion cost is one struct copy per row
  per flush (20s default), well under the
  per-flush noise floor.

## Pre-fetch trade-off

`Service.Delete` already pre-fetches the user
before deleting (PR #166 audit #2 pattern).
The pre-delete user is what we pass to
`enqueueUserDelta` for the Delete path — the
allowlist filter must use the user's allowlist
as it was when the row was alive, not the
empty allowlist of a post-delete state. Same
trade-off as PR #166: one extra read per Delete
to populate the filter, but Delete is the
slowest CRUD verb (preceded by an "are you
sure" dialog 99% of the time).

## Follow-up PRs

- **PR 4 (subs + cabinet)**: subscription
  service renders per-user config URL
  (resolves the user's per-inbound credentials
  via `credentials.ListByUser`); cabinet
  endpoints to view and manage own credentials.
  ~2-3 hours solo.

## Tests

- 25 of 25 unit packages green.
- New tests: 5 (enqueueUserDelta fan-out) +
  4 (builder credentials path) = 9 new test
  functions. 24 existing tests updated.
- `go vet -tags=integration ./...` clean.
- `golangci-lint v2` 0 issues (after 2
  `#nosec G101` for the constant name
  "credentials" in the builder; same false-
  positive pattern as PR 2's renderer constant).
- `gofmt` clean.

## File map

- `backend/internal/cores/builder/builder.go`
  (modified, plus 95 lines)
- `backend/internal/cores/builder/builder_test.go`
  (modified, plus 145 lines)
- `backend/internal/cores/builder/flushfn_smoke_test.go`
  (modified, plus 2 lines)
- `backend/internal/cores/builder/flushfn_integration_test.go`
  (modified, plus 1 line)
- `backend/internal/cores/batched.go` (modified,
  plus 8 lines)
- `backend/internal/users/service.go` (modified,
  plus 50 lines)
- `backend/internal/users/batchapplier_fanout_test.go`
  (new, 178 lines)
- `backend/cmd/aegis/main.go` (modified, plus
  6 lines)
- `.github/pr-body-feat-builder-multi-user-renderer-wiring.md`
  (new)
