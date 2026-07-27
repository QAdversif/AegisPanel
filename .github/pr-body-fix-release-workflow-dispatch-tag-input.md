# fix(ci): use tag input for UI image in release.yml workflow_dispatch

## TL;DR

`release.yml` hardcoded the UI image tag as
`${{ github.ref_name }}`. On tag-push events that's
the pushed tag (e.g. `v0.4.0`), but on
`workflow_dispatch` `github.ref_name` is the branch
name (`main`), so the UI image gets pushed with
tag `main` instead of the operator-supplied `tag`
input. PR #103 made `workflow_dispatch` actually
push, but the UI image still ended up tagged
`ghcr.io/qadversif/aegispanel-ui:main` for the
`v0.4.0` re-run. This PR fixes the UI image tag
to resolve to the operator-supplied tag on
`workflow_dispatch` (and stays the same on
tag-push events).

## What's in this PR

- `.github/workflows/release.yml`:
  - New job-level `env.release_tag`:
    `${{ github.event_name == 'push' && github.ref_name || inputs.tag }}`
    - Resolves to `github.ref_name` on tag-push
      (e.g. `v0.4.0`).
    - Resolves to `inputs.tag` on
      `workflow_dispatch` (e.g. `v0.4.0` when
      `gh workflow run release -f tag=v0.4.0`).
  - `Build and push UI image`:
    `tags: ghcr.io/qadversif/aegispanel-ui:${{ github.ref_name }}`
    → `tags: ghcr.io/qadversif/aegispanel-ui:${{ env.release_tag }}`.
  - `Show tag` step now also echoes
    `release_tag = ${{ env.release_tag }}` for
    visibility in workflow logs.

## Why this was needed

After PR #103 merged, the `v0.4.0` re-run of the
release workflow successfully logged in to GHCR
and built/pushed the panel + UI images. The panel
`latest` tag was re-emitted (via the
`docker/metadata-action` `latest=auto` flavor).
The UI image, however, was tagged with
`github.ref_name` = `main` (the branch, not the
supplied `tag` input) because the UI build step
hardcoded `github.ref_name` instead of consulting
the `tag` input. The result was:

- `ghcr.io/qadversif/aegispanel-ui:v0.4.0` — does
  not exist.
- `ghcr.io/qadversif/aegispanel-ui:main` — the
  only version, image content is the v0.4.0 code
  (built from current `main` at the workflow run
  time, which is the same code as the `v0.4.0` git
  tag commit 39d4d9e plus the workflow fixes from
  PRs #102 and #103; the application code is
  unchanged).

The whole point of the `tag` input on
`workflow_dispatch` is to let an operator
re-publish a specific existing release's images.
With the current workflow, the UI image ends up
on the wrong tag. The fix is the one above.

## What this PR does NOT do

- **No change to the panel `metadata-action`
  config.** The `latest=auto` flavor correctly
  re-emits `latest` on `workflow_dispatch` but
  does not re-emit `0.4.0` / `0.4` semver tags
  (the action sees them as already-published and
  skips re-emission). The `0.4.0` / `0.4` tags
  still point to the original push from
  2026-07-27T01:43:54Z, which is the same v0.4.0
  code. Forcing the metadata-action to re-emit
  semver tags for `workflow_dispatch` would
  require either deleting the existing
  package-versions and re-pushing (a more
  invasive operation) or working around the
  metadata-action's "already published" logic
  (no clean API). The current state — `latest`
  on the new build, `0.4.0` / `0.4` on the
  original build, both with v0.4.0 code — is
  acceptable: the semver tags still point to
  v0.4.0 code, and `latest` is correctly tracking
  the most recent build.
- **No change to the `Create GitHub release`
  step.** That step stays gated on `'push'` only;
  a `workflow_dispatch` re-run is for re-pushing
  images, not re-creating the GitHub release
  (the release already exists for the tag).

## Verification

- `git diff .github/workflows/release.yml` — 1
  env var added, 1 line changed in the UI image
  build step, 1 echo line added to `Show tag`
  for log visibility.
- The `ci.yml` matrix does not exercise
  `release.yml`. Coverage for this change is the
  post-merge `workflow_dispatch` re-run on `main`:
  the UI image version with `tag=v0.4.0` should
  appear at `ghcr.io/qadversif/aegispanel-ui:v0.4.0`
  with an `updated_at` timestamp after the merge
  time.

## Follow-up (post-merge)

1. Trigger the `release` workflow manually with
   the `v0.4.0` tag. The UI image will be pushed
   to `ghcr.io/qadversif/aegispanel-ui:v0.4.0`
   (in addition to the existing `main` tag).
2. Verify both v0.4.0 images are present:
   - `ghcr.io/qadversif/aegispanel:0.4.0` (panel,
     from original 01:43:54Z push, same v0.4.0
     code).
   - `ghcr.io/qadversif/aegispanel-ui:v0.4.0`
     (UI, new after this PR).
3. The `main`-tagged UI image stays as-is (it is
   the same v0.4.0 code); no cleanup needed.
