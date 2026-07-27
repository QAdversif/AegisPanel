# fix(ci): allow `workflow_dispatch` to actually push in `release.yml`

## TL;DR

`release.yml` gated the GHCR `push` (and the GHCR
`Login`) on `github.event_name == 'push'`. That
meant the `workflow_dispatch` re-run path silently
**built but did not push** — the steps "succeeded"
because the build steps honour `push: false` and
skip the registry write. PR #102 fixed the tag
case-sensitivity; this PR finishes the job by
making `workflow_dispatch` a real re-push path so
operators can re-run the release images for an
existing tag (e.g. `v0.4.0`) without deleting and
re-creating the tag.

## What's in this PR

- `.github/workflows/release.yml`:
  - Login step: `if: github.event_name == 'push'`
    → `if: github.event_name == 'push' || github.event_name == 'workflow_dispatch'`.
  - `Build and push panel image`:
    `push: ${{ github.event_name == 'push' }}`
    → `push: ${{ github.event_name == 'push' || github.event_name == 'workflow_dispatch' }}`.
  - `Build and push UI image`: same change.
  - `Create GitHub release` step is intentionally
    **unchanged**: that step still gates on
    `github.event_name == 'push'` only, because
    `workflow_dispatch` is for re-pushing images,
    not re-creating the GitHub release (the release
    already exists for the tag).

## Why this was needed

After PR #102 (lowercase tag fix) merged, the
follow-up was a `workflow_dispatch` re-run for the
`v0.4.0` tag. The run completed with status
`success` (run `30232048372`), but the GHCR image
versions for `qadversif/aegispanel:v0.4.0` and
`qadversif/aegispanel-ui:v0.4.0` were not updated
— the only entry in the GHCR package versions
listing for `0.4.0` was the original push from
2026-07-27T01:43:54Z. The `Login to GHCR` step
was `skipped`, and the build steps' `push: ${{
github.event_name == 'push' }}` evaluated to
`false`, so the registry write was a no-op.

The whole point of the `workflow_dispatch` input
on this workflow is to support re-pushing images
without re-tagging. With the current gating, that
is impossible — and a green workflow run is
actively misleading because the operator sees
"success" but no images are pushed.

## What this PR does NOT do

- **No change to the `Create GitHub release`
  step.** A `workflow_dispatch` re-run with an
  existing tag is for re-pushing images, not for
  re-creating the GitHub release (the release
  already exists for the tag, and the auto-generated
  notes would be identical to the original).
  Keeping that step gated on `'push'` is the
  correct semantic.
- **No tag changes.** The `v0.4.0` tag is not
  moved. Re-pushing a tag with new image content
  is OK for an OCI image (the image manifest
  references are content-addressed, so a tag just
  points to a new digest), but the tag itself is
  not deleted.

## Verification

- `git diff .github/workflows/release.yml` — 3
  conditions updated (Login + 2 build/push). The
  `Create GitHub release` step is unchanged.
- The `ci.yml` matrix does not exercise
  `release.yml`, so the change is covered only by
  the next `workflow_dispatch` run against `main`.
  The follow-up to this PR is to trigger that run
  and verify the GHCR image versions for
  `qadversif/aegispanel:v0.4.0` and
  `qadversif/aegispanel-ui:v0.4.0` actually get
  new digests with `updated_at` timestamps after
  the merge time.

## Follow-up (post-merge)

1. Trigger the `release` workflow manually with
   the `v0.4.0` tag (workflow_dispatch). The panel
   and UI images will build, log in to GHCR, and
   push. Verify the new digests are present at
   `ghcr.io/qadversif/aegispanel:v0.4.0` and
   `ghcr.io/qadversif/aegispanel-ui:v0.4.0`.
2. The previous `30232048372` run is now a
   one-time stale "success" — its existence is a
   useful artefact for understanding the
   pre-fix-vs-post-fix behaviour delta.
