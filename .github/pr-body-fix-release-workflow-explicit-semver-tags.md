# fix(ci): explicit semver tags for panel image in release.yml

## TL;DR

`release.yml`'s panel `metadata-action` config used
`type=semver,pattern={{version}}` to emit
`0.4.0` / `0.4`. That works on `git push origin
v0.4.0` (the action reads the version from the
ref), but on `workflow_dispatch` the ref is the
branch (`main`) and the action emits no semver
tags — only `latest` via the `latest=auto` flavor.
Result on the post-#103 v0.4.0 re-run: the panel
`0.4.0` / `0.4` tags stayed on the original
01:43:54Z push, while the new build got only
`latest`. Both digests held the same v0.4.0 code
(only workflow files changed), so this is
semantically correct for v0.4.0 today — but the
next time we want to re-publish a release that
includes an application-code change, the semver
tag will lag. This PR makes the semver tags
explicit so both paths produce the same tag list.

## What's in this PR

- `.github/workflows/release.yml`:
  - New `Compute release version` step
    (`id: rel`) that derives `version` (`v0.4.0`
    → `0.4.0`) and `short` (`0.4`) from
    `env.release_tag` and writes them as step
    outputs. Uses bash parameter expansion
    (`${raw#v}`) and `sed` for the short form.
  - `Extract version metadata` (`id: meta`):
    `type=semver,pattern={{version}}` →
    `type=raw,value=${{ steps.rel.outputs.version }}`.
    Same change for the `{{major}}.{{minor}}`
    pattern, now using `steps.rel.outputs.short`.
  - `flavor: latest=auto` is kept — it still
    matters on `push` events for prerelease
    handling (the `latest` tag is suppressed for
    `-rc` / `-beta` / `-alpha` versions, which
    the raw `enable={{is_default_branch}}`
    expression would not catch on a tag-push
    event).
  - `type=raw,value=latest,enable={{is_default_branch}}`
    is kept — on `workflow_dispatch` it emits
    `latest` when the ref is the default branch
    (which is the intended use case for the
    re-run path).

## Why this was needed

The full state of the previous setup on a
`workflow_dispatch` re-run:

- The action had no way to extract a version
  from `github.ref_name = 'main'`.
- `type=semver,pattern={{version}}` and
  `{{major}}.{{minor}}` evaluate to empty on
  workflow_dispatch.
- Only `latest` got emitted.
- The `0.4.0` / `0.4` tags in GHCR continued to
  point at whatever the most recent tag-push
  event had produced.

For v0.4.0 today, this is harmless because the
original 01:43:54Z push and the post-#104 build
contain the same application code: the only
`release.yml` workflow file changes happened
across PRs 102, 103, and 104. But the design is
brittle: any future workflow re-run after an
application-code change would silently leave the
semver tags on stale digests. Making the semver
tags explicit fixes the contract — both paths
now produce `[version, short, latest]`, and a
re-run always moves the semver tags to the new
digest.

## What this PR does NOT do

- **No change to the UI image tag.** That step
  already uses `env.release_tag` (PR #104) and
  is correct for both paths.
- **No change to the `Create GitHub release`
  step.** It stays gated on `push` only.
- **No change to `git tag v0.4.0`'s target.**
  The git tag still points to `39d4d9e`
  (the original v0.4.0 release commit). This PR
  only changes how the panel image's GHCR
  manifest is constructed on a `workflow_dispatch`
  re-run.

## Verification

- `git diff .github/workflows/release.yml` — one
  new step (the version computation), one
  metadata-action config change. The
  `latest=auto` flavor and the `latest` raw tag
  are unchanged.
- The `ci.yml` matrix does not exercise
  `release.yml`. Coverage is the post-merge
  `workflow_dispatch` re-run on `main`:
  the panel `0.4.0` and `0.4` tags should now
  point to the new build (an `updated_at`
  timestamp after the merge time).

## Follow-up (post-merge)

1. Trigger the `release` workflow manually with
   the `v0.4.0` tag. The panel `0.4.0` and `0.4`
   tags should be re-pointed to the new build.
2. Verify via the API:
   `gh api /users/qadversif/packages/container/aegispanel/versions --jq '.[] | {updated_at, tags: .metadata.container.tags}'`
   — the version with `tags: ["0.4", "0.4.0", ...]`
   should have an `updated_at` after the merge
   time.
3. The previous package-version with
   `tags: ["0.4", "0.4.0"]` (the original
   01:43:54Z push) will lose those tags and
   end up with `tags: []` (or be garbage-
   collected by GHCR's housekeeping). Either is
   fine; it does not affect pull semantics since
   the semver tags now point to the new digest.
