## docs: v0.6.0 CHANGELOG + ROADMAP + plans API reference

Closes the v0.6.0 doc surface. The 4 code PRs
(PR #131, PR #132, PR #133, PR #134) shipped the
Go package, HTTP handler, OpenAPI spec, and the
admin UI; this PR adds the v0.6.0 row to the
user-facing docs so the release can be tagged.

### What ships in this PR

- `CHANGELOG.md` — new `## [0.6.0] - 2026-07-31`
  section above the existing `## [Unreleased]`
  block. Documents the four shipped PRs (plans
  package, admin handler, OpenAPI + hand-mirrored
  client, PlansView + sidebar + i18n) plus the
  explicit "what is NOT in v0.6.0" list
  (`plan_pool` writes, audit log call-sites,
  `plan_pool` UI, shared zod schema) so a future
  maintainer reading the changelog can tell what
  shipped vs. what was deferred. Matches the
  existing per-version format
  ([0.4.0] - 2026-07-26, [0.3.0-mvp-byo-node],
  [0.2.0-mvp-agent], [0.1.0-mvp-render], [0.0.1]) —
  a new dated section per release, with the
  "Unreleased" block kept for the in-progress
  next iteration.
- `docs/ROADMAP.md` — two changes:
  1. The headline table at the top gets a
     `✅ shipped (#131, #132, #133, #134)` cell
     on the v0.6.0 row (the row was `⏳` before
     this PR).
  2. The v0.6.0 detail section gets a `✅ shipped`
     marker, a `Closed by:` block listing the
     four PRs with a one-line description each,
     a `What landed` bullet list, and a
     `Deferred to v0.6.x` block with the four
     deferred items.
- `docs/api/index.md` — the v0.5.0-era
  "Under construction" notice is gone; the page
  now points at `docs/openapi.yaml` as the source
  of truth and lists the v0.6.0 endpoint groups
  in a table. The "Planned endpoints (Phase 1+)"
  list is replaced with an "Endpoints shipped in a
  later version" list that separates the v0.7.0
  webhooks work, the v1.2+ cabinet work, and the
  inbound payment webhook from the architecture
  doc.

### Why three docs in one PR

Each one is a small, isolated change to an
existing file. Splitting them into three PRs
would multiply the CI cost (one extra CI run
each) for no benefit — the diffs do not
interfere, and a future maintainer bisecting the
changelog can find the v0.6.0 release by
searching for the section header.

### Out of scope

- A `docs/plans/` page (a longer-form docs page
  explaining the data model + the `users.plan_id`
  reference + the `plan_pool` deferred work). The
  OpenAPI spec + the ROADMAP detail section are
  enough for v0.6.0; a dedicated page is a v0.6.x
  follow-up if real docs demand surfaces.
- A `v0.5.0` backfill on the CHANGELOG (the v0.5.0
  work landed under the existing `## [Unreleased]`
  block, never moved to its own `## [0.5.0]`
  section). Out of scope for v0.6.0; a separate
  chore PR if the operator ever wants the strict
  per-release layout.

### Tag plan

After this PR lands, the operator can tag
`v0.6.0` at the merge commit. The release
pipeline (`release.yml`) emits the `0.6`,
`0.6.0`, and (per the #127 fix) the `latest`
Docker image tags; the `verify-images` cosign
sign pipeline (added in #129) proves them
end-to-end before the operator ever pulls them.

### How to verify locally

```sh
# docs check (CI-only, WSL PATH doesn't see node)
npx --no-install markdownlint-cli2 '**/*.md' \
  '!node_modules/**' '!**/node_modules/**' '!**/dist/**' \
  '!**/.vuepress/.temp/**' '!**/.vuepress/.cache/**'
# expect: Summary: 0 error(s)
```

Then eyeball the diff:

- `CHANGELOG.md` — new `## [0.6.0] - 2026-07-31`
  section above `## [Unreleased]`
- `docs/ROADMAP.md` — v0.6.0 row in the headline
  table is `✅ shipped (#131, #132, #133, #134)`;
  the v0.6.0 detail section has the `✅ shipped`
  marker and the `Closed by:` block
- `docs/api/index.md` — the "Under construction"
  notice is gone; the v0.6.0 endpoint groups
  table is the new first content
