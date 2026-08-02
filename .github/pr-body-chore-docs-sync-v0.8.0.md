Post-v0.8.0 documentation sync. The release lands the
Phase 2 multi-user sing-box render end-to-end across 4 PRs
(#167, #168, #169, #170) plus the audit-log call-site
wiring (#166) and a frontend dependency batch (#159, #161,
#163, #165). v0.8.0 is end-to-end multi-user: per-(user,
inbound) credentials live in the `user_inbound_credentials`
table, the sing-box renderer emits multi-user `users: [...]`
arrays, the BatchedApplier fan-out is narrowed by
`user.HostsAllowlist` / `Blocklist`, and the per-user sub URL
renders the user's own UUID/password. The HTTP admin surface
for the credentials table is the v0.8.x follow-up; the data
flow is end-to-end today.

This PR is the canonical "the doc tree now matches what the
repo actually ships after #170" pass. 8 files, +325/-99.

What changed

- CHANGELOG.md: new `[0.8.0]` section covering the 9 PRs
  plus the "What this PR does NOT ship" list with the v0.8.x
  follow-ups (HTTP admin credentials, host->node mapping,
  inbound-templates, cosign re-sign, JSON logs, smoke-on-VM,
  cabinet).
- KNOWN_LIMITATIONS.md: title bumped to v0.8.0; intro
  paragraph rewritten to the v0.8.0 state. The "v0.8.0 -
  currently open" section now lists the HTTP admin credentials,
  the host->node mapping, the inbound-templates work, the
  pre-existing eslint warnings, plus the out-of-scope items.
  The "v0.8.0 - closed in v0.8.0" table covers the audit +
  Phase 2 + frontend dep batch.
- docs/ROADMAP.md: v0.7.2 row updated to shipped (#166, #167,
  #168, #169, #170); v0.8.0 status row added with planned
  v0.8.x follow-ups; existing v0.9.0 and v1.0.0-mvp-soft-launch
  rows preserved.
- docs/guide/architecture.md: new "v9.5 (2026-08-02, post-v0.8.0
  sync)" Current version note. The doc body itself is
  unchanged from v9.4; the §21 / §25 status tables and the
  Current version note are the only diffs.
- ARCHITECTURE.md: v9.5 entry in §25 covering the 9 PRs and
  the Phase 2 multi-user slice status (per-inbound query,
  per-render query, BatchedApplier fan-out narrow,
  `WithCreds(nil)` migration path). New "Slice status (per
  v9.5, Phase 2 multi-user sing-box render)" sub-section
  under MVP-0.4 in §21.
- README.md: headline v0.7.1 -> v0.8.0; status table marks
  v0.8.0 as shipped, v0.8.x as the next planned batch; Repo
  Layout updated to add `internal/credentials` and bumps
  the packages count (17 -> 21) and migrations count
  (16 -> 19); the v0.7.0-shipped API reference note stays
  accurate.
- docs/api/index.md: headline v0.7.2 -> v0.8.0; "Endpoints
  shipped in v0.8.0" clarifies that the OpenAPI spec is
  still at 0.7.0 and the HTTP admin for credentials is the
  v0.8.x follow-up. The "Endpoints shipped in a later
  version" block now lists the credentials admin mount.
- docs/README.md: Architecture (v9.3 -> v9.5), Backend and
  Frontend rows bumped to v0.8.0; 5 new component rows for
  the audit-log call-site wiring and the 4 Phase 2
  multi-user steps; the v0.8.x planned row for the HTTP
  admin credentials surface.

Validation

- `markdownlint-cli2` on the 8 modified files: 0 errors.
- `markdownlint-cli2` on the full tracked `.md` set
  (107 files): 0 errors.
- `gofmt`, `golangci-lint v2`, `go test -short`: not
  applicable to this PR (no source changes).

What this PR does NOT change

- The OpenAPI spec (`docs/openapi.yaml` stays at v0.7.0;
  v0.8.0 is purely infrastructure by API surface).
- The generated `frontend/src/types/api.d.ts` (no
  regeneration needed; `npm run codegen:check` passes
  without changes).
- The migration list (no new migrations; 0019 was
  shipped in #167).
- Source code in `internal/*` (no behaviour changes; this
  is a docs-only PR).

Closes the v0.7.2 KNOWN_LIMITATIONS "v0.8.0 release tag"
chore. The v0.8.0 git tag will land on this commit (or
on a follow-up docs-only commit) as part of the v0.8.0
release; the pattern is the v0.7.1 / v0.7.2 tag-on-docs-
commit dance.
