# docs(sync): bring docs in line with the v0.4.0 post-release state

## TL;DR

Project docs were stale relative to the current
`main` after the `v0.4.0` release (commit
`39d4d9e`) and the four post-release workflow
fixes (#102, #103, #104, #111). This PR is a
docs-only sync: no code changes, just bringing
`CHANGELOG.md`, `docs/ROADMAP.md`,
`docs/guide/architecture.md`, and
`KNOWN_LIMITATIONS.md` in line with the actual
repo state.

## What's in this PR

- **`CHANGELOG.md`**:
  - `[Unreleased]` was holding a stale "Documentation
    (v0.4.0 release, #100)" entry that is already
    in the `[0.4.0]` section. Removed.
  - Added a `### Fixed (release workflow
    post-v0.4.0 follow-ups, #102 / #103 / #104 /
    #111)` sub-section under `[Unreleased]` with
    per-PR rationale: lowercase GHCR image names
    (#102), `workflow_dispatch` push (#103), UI
    image tag input (#104), explicit panel semver
    tags (#111). These PRs land on `main` after the
    `v0.4.0` git tag (which points to `39d4d9e`)
    and are documented under `[Unreleased]` to be
    picked up by the next `v0.4.1` / `v0.5.0`
    release.
- **`docs/ROADMAP.md`**:
  - Status date: `2026-07-26` → `2026-07-27`.
  - `v0.4.0-d.r4` row: `🔄 this PR` →
    `✅ shipped (#100)`.
  - New `v0.4.0-post` row for #102, #103, #104,
    #111 (no application code change, only
    `.github/workflows/release.yml`).
  - `v0.4.0` row: `⏳ tag after merge` →
    `✅ shipped (tag on 39d4d9e, release notes
    via gh release create)`.
  - Path C d.r4 entry: `(this PR)` → `(#100) —
    landed`.
  - New "v0.4.0 release workflow contract" section
    that documents the post-#111 stable contract
    for both event paths (tag-push and
    `workflow_dispatch` re-run).
- **`docs/guide/architecture.md`**:
  - "Current version" bumped from `v8 (2026-07-17)
    — review-driven fixes` to `v9.2 (2026-07-23) —
    roadmap sync + post-v0.3.0 cleanup`, with the
    `v9.1` and `v9` entries added above the `v8`
    summary. The `v8` block is kept verbatim below.
  - New "Post-v0.4.0 release workflow fixes
    (2026-07-27)" section listing the four PRs
    with one-line rationale for each.
- **`KNOWN_LIMITATIONS.md`**:
  - Title bumped from `v0.3.0` to `v0.4.0`.
  - "v0.3.0 — currently open" reframed as
    "v0.4.0 — currently open". Items that
    `v0.3.0-mvp-byo-node` closed (Add node
    dialog, real `aegis-agent` binary) and items
    that `v0.4.0` closed (Batched apply) moved
    into a new "Closed in v0.3.0" / "Closed in
    v0.4.0" table format.
  - "Add node" / "Real `aegis-agent` binary" /
    "Per-node `AgentBearer` storage" /
    dependabot PRs #68 / #70 / #71 / #72 / chi
    v5.3.1 / trivy fix / eslint --fix /
    reserved-package `doc.go` stubs / vitest 4 /
    eslint 10 / vue-router 5 / `npm ci` standard /
    vite 7.3.6 / brace-expansion+js-yaml CVEs /
    custom Caddy all closed in `v0.3.0-mvp-byo-node`
    or the v0.3.0 cleanup batch. Listed in the
    "Closed in v0.3.0" table.
  - Batched apply, `internal/users` data layer,
    `users.User` wire-format compat, drop
    subscription-side user-CRUD, move
    `admin_handler.go` to `users`,
    `DefaultSubTokenRotationGrace` public
    constant, `docs/ROADMAP.md` published, and
    the four release workflow fixes (post-tag)
    all closed in `v0.4.0`. Listed in the
    "Closed in v0.4.0" table.
  - "Dependabot majors for the v0.x window"
    section: PRs #69 (frontend minor+patch) and
    #73 (zod 3→4) remain open, deferred to
    v0.5.0. PRs #70 / #71 / #72 closed in the
    v0.3.0 cleanup batch. PR #68 superseded by
    PR #75.
  - `/admin` HTTP surface removed from the
    "currently open" list — that surface ships
    in v0.2.0-mvp-agent (per the v0.2.0
    CHANGELOG entry; the previous
    `KNOWN_LIMITATIONS.md` reference to it as a
    v0.3+ gap was a holdover from the v0.1.0
    framing).

## Why this is needed

The four PRs that landed on `main` after the
`v0.4.0` git tag (`#102`, `#103`, `#104`, `#111`)
left a gap between what the docs described and
what the code actually does. Specifically:

- `CHANGELOG.md` was missing entries for the
  four PRs (they were the immediate cause of
  the user's `Ошибка: сбой buildx` report and
  the chain of re-runs).
- `docs/ROADMAP.md` still had `🔄 this PR` /
  `⏳ tag after merge` for items that had
  shipped over a week ago.
- `docs/guide/architecture.md` was pointing at
  `v8` even though the repo is on v9.2 (and
  has been since 2026-07-23).
- `KNOWN_LIMITATIONS.md` was framed as a
  v0.3.0 doc with v0.4+ items still listed as
  open. The "Add node" dialog and the real
  `aegis-agent` binary both shipped in
  `v0.3.0-mvp-byo-node` (commit `ba78b35`).

The release workflow contract in particular
wants to be discoverable for future maintainers,
so a future `v0.4.0` re-run doesn't have to
re-derive the same conclusions from
`.github/workflows/release.yml`. The
`docs/ROADMAP.md` "v0.4.0 release workflow
contract" section is the canonical writeup of
the post-#111 contract.

## What this PR does NOT do

- **No code changes.** All four files in this
  PR are documentation; no `.go` / `.vue` /
  `.ts` / `.yml` / workflow files are touched.
- **No new tags.** The `v0.4.0` tag stays on
  `39d4d9e`. The post-#102-#111 fixes are
  documented under `[Unreleased]` in
  `CHANGELOG.md` to be picked up by the next
  `v0.4.1` / `v0.5.0` release cut.
- **No `ARCHITECTURE.md` §25 entry.** The
  release workflow fixes are operational, not
  architectural. `ARCHITECTURE.md` §25 is for
  architectural changes (e.g. v9.2 was the
  last entry). The new "v0.4.0 release
  workflow contract" lives in `docs/ROADMAP.md`,
  which is the right home for the operational
  contract.

## Verification

- `git diff --stat` shows 4 files, +237/-86.
- `npm exec --no -- markdownlint-cli2 "**/*.md"`
  → 0 issues across 71 files.
- `gh pr checks` on this PR runs the standard
  CI matrix (backend, frontend, docs, security,
  ansible, shellcheck, migrations, containers).
  No code or workflow files changed, so the
  non-docs jobs should pass on existing green.

## Follow-up (post-merge)

- Decide whether the next cut is `v0.4.1`
  (release workflow patches only) or `v0.5.0`
  (the polish work in `docs/ROADMAP.md` §"v0.5.0
  — polish before v0.6.0+"). If `v0.4.1`, move
  the `[Unreleased]` "release workflow" entries
  into a new `[0.4.1]` section. If `v0.5.0`,
  keep them under `[Unreleased]` until the
  polish work lands.
- The `KNOWN_LIMITATIONS.md` "Dependabot majors
  for the v0.x window" entry has #69 and #73
  listed as v0.5.0 work. When the v0.5.0 polish
  PRs land, those entries should be moved to
  the appropriate "Closed in v0.5.0" table.
