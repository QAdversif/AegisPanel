# chore(release): v0.4.0 CHANGELOG entry

## TL;DR

The v0.4.0 release: CHANGELOG entry for the d-r-series
(PRs #95 / #96 / #97 / #99 / #100) and the v0.4.0-mvp-batched
work stream (PRs #92 / #93 / #94). The annotated tag
`v0.4.0` lands on the merge commit; the tag push
happens after this PR merges.

This is a docs-only change. No code, no config, no
build output. The CHANGELOG is the single source of
truth for "what shipped when"; the git tag is the
single source of truth for "which commit is the
release". The project's tagging-policy convention
(both in `docs/ROADMAP.md` and in the
`v0.1.0-mvp-render` / `v0.2.0-mvp-agent` /
`v0.3.0-mvp-byo-node` retro-tag commits) is to
keep the two in lock-step.

## What's in this PR

- `CHANGELOG.md` — new `[0.4.0] - 2026-07-26` section
  with the v0.4.0-mvp-batched work stream and the
  v0.4.0-d Path C work stream. Two sub-sections
  (the 5 sub-PRs that landed in d-r, and the 3
  sub-PRs that landed in -mvp-batched). Behaviour
  changes (64-hex `sub_token`, 24h rotation grace)
  documented inline.
- `CHANGELOG.md` — also a new
  `[0.3.0-mvp-byo-node] - 2026-07-23` section
  that was sitting under `[Unreleased]` since
  the v0.3.0 tag landed. The cleanup-batch items
  (chi v5.3 / trivy / eslint / vitest / eslint 10
  / vue-router 5 / `npm ci` standardisation / vite
  7.3.6 / brace-expansion / custom Caddy) and the
  v9.2 roadmap-sync documentation items belong
  to v0.3.0's lifetime, not v0.4.0's. Moving
  them to the right section keeps the
  release-history time-aligned.
- `CHANGELOG.md` — `[Unreleased]` keeps the
  v0.4.0-release entry from PR #100 (the
  `docs/ROADMAP.md` doc). The v0.4.0 section is
  the canonical place for the v0.4.0 content;
  the [Unreleased] entry references the release
  but does not duplicate the content.

## What this PR does NOT do

- No `git tag v0.4.0`. The tag is a local
  operation; it lands in a follow-up step after
  this PR merges (the same pattern as the
  v0.1.0 / v0.2.0 / v0.3.0 tags).
- No `git push origin v0.4.0`. The push
  happens after the tag is created locally.
- No CHANGELOG re-format. The file is already
  in Keep-a-Changelog format; the new sections
  follow the existing structure.

## Why PR-style

The project's policy is "one PR = one change". The
CHANGELOG update is one change; the tag is another
(operationally separate — the tag is created on
main after the PR merges). Doing both in the same
PR would conflate two operations and create a
single commit that does two things. PR-style
keeps the git history clean: `git log CHANGELOG.md`
shows the per-release changelog; `git tag -l
v0.4*` shows the release tags.

## Verification

- `markdownlint-cli2 CHANGELOG.md` — 0 issues.
- The CHANGELOG file is unchanged in shape — just
  the addition of two new sections and a
  `[Unreleased]` entry.
- `git diff CHANGELOG.md` — diff is bounded to the
  new sections (verified locally).

## Follow-up (post-merge)

1. On the merge commit, run:
   ```
   git tag -a v0.4.0 -m "v0.4.0: v0.4.0-mvp-batched + v0.4.0-d Path C

   Ships:
   - v0.4.0-mvp-batched (#92, #93, #94): BatchedApplier,
     real apply transport, install_singbox role.
   - v0.4.0-d Path C (#95, #96, #97, #99, #100):
     user-CRUD moves from subscription to users;
     subscription is now a pure render orchestrator.

   See CHANGELOG.md and docs/ROADMAP.md for details."
   git push origin v0.4.0
   ```
2. Update the GitHub Releases page (the `gh release`
   command auto-generates notes from the tag message).
3. Send a one-line "v0.4.0 is out" status update
   on the operator Slack channel (out of scope
   for this PR — the Slack webhook config is
   `internal/obs/slack.go` and not yet wired in
   production).

The tag push + release page are the operator-
visible end of v0.4.0. The CHANGELOG is the
developer-visible end.
