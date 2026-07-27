# fix(ci): lowercase the GHCR image names in `release.yml`

## TL;DR

`release.yml` hardcoded the image name as
`ghcr.io/QAdversif/AegisPanel` (panel) and
`ghcr.io/QAdversif/AegisPanel-ui` (UI), which violates
the OCI image-spec rule that the path portion
(after the registry) be lowercase. Buildx rejected
the v0.4.0 release build with:

```
ERROR: failed to build: invalid tag
"ghcr.io/QAdversif/AegisPanel-ui:v0.4.0":
repository name must be lowercase
```

The fix is one-character-per-line: lowercase the
two `QAdversif` and `AegisPanel` tokens. The
`ci.yml` workflow already uses the lowercase form
(`qadversif/aegispanel`); the `release.yml`
workflow was the hold-out.

## What's in this PR

- `.github/workflows/release.yml`:
  - Line 49: `images: ghcr.io/QAdversif/AegisPanel`
    → `images: ghcr.io/qadversif/aegispanel`.
  - Line 74: `tags: ghcr.io/QAdversif/AegisPanel-ui:...`
    → `tags: ghcr.io/qadversif/aegispanel-ui:...`.

The `qadversif` org (lowercase) is the canonical
GitHub username; the `aegispanel` and
`aegispanel-ui` image names match the repo
structure. The first build to use these names is
the v0.4.0 release (re-triggered after this PR
merges).

## Why this happened

The `release.yml` workflow was authored at
`5840c13` (the v0.1.0 cut) when the GHCR namespace
was just the GitHub username with default
capitalisation. The OCI spec is case-insensitive
in some registries (e.g. Docker Hub) and
case-sensitive in others (e.g. GHCR); the workflow
got away with `QAdversif` until the v0.4.0 build
because earlier releases either succeeded (the
metadata-action lowercased the panel tag
implicitly for the buildkit invocation) or were
not pushed through the GHCR publish path.

The `ci.yml` workflow was updated to lowercase
in the v0.3.0 cleanup batch (PRs #87, #89, #90, #91;
see the `lint` job in the ci matrix). The release
workflow was missed.

## What this PR does NOT do

- **No re-push of the v0.4.0 tag.** Re-pushing an
  annotated tag is not a clean operation (the
  previous SHA gets detached; consumers that
  reference `v0.4.0` by SHA see the old commit).
  The release workflow has a `workflow_dispatch`
  input that lets us re-run the v0.4.0 build with
  the new workflow definition without touching
  the tag. The follow-up: after this PR merges,
  trigger the workflow manually for `v0.4.0`.

## Verification

- `git diff .github/workflows/release.yml` — 2
  lines changed (lowercase).
- The existing `ci.yml` workflow was already
  passing the lowercase form (used as a
  reference); no changes there.

## Follow-up (post-merge)

1. Trigger the `release` workflow manually with
   the `v0.4.0` tag (workflow_dispatch on the
   workflow file). The panel + UI images will
   build with the new lowercase tags and push to
   `ghcr.io/qadversif/aegispanel:v0.4.0` and
   `ghcr.io/qadversif/aegispanel-ui:v0.4.0`.
2. The `ghcr.io/QAdversif/AegisPanel-ui:v0.4.0`
   build that failed is a one-time failure; the
   retry against the new workflow should
   succeed on first run.
