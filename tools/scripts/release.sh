#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Aegis — create (or roll forward) a SemVer release tag and update
# CHANGELOG.md with the conventional-commit log since the previous tag.
#
# Usage:
#   tools/scripts/release.sh 0.1.0
#   tools/scripts/release.sh 0.2.0 --push   # also push tag + branch to origin
#   tools/scripts/release.sh --snapshot     # dry-run: print the would-be tag + entries
#
# Requires: git, awk, sed, sort. Optional: gh (GitHub CLI) for the
# `--github-release` action.

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

# ---- args -----------------------------------------------------------------

VERSION=""
PUSH=0
SNAPSHOT=0
GITHUB_RELEASE=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --push) PUSH=1; shift ;;
        --snapshot) SNAPSHOT=1; shift ;;
        --github-release) GITHUB_RELEASE=1; shift ;;
        -h|--help)
            sed -n '2,18p' "$0"
            exit 0
            ;;
        --*) echo "unknown flag: $1" >&2; exit 2 ;;
        *) VERSION="$1"; shift ;;
    esac
done

if [[ -z "$VERSION" ]]; then
    echo "usage: $0 <semver> [--push] [--snapshot] [--github-release]" >&2
    exit 2
fi

# Lightweight SemVer check. We don't want to pull in a dependency just
# for this — the format is small.
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]]; then
    echo "error: '$VERSION' is not a valid SemVer (expected MAJOR.MINOR.PATCH[-prerelease])" >&2
    exit 2
fi

TAG="v$VERSION"

# ---- preconditions -------------------------------------------------------

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "error: not inside a git work tree" >&2
    exit 2
fi

if [[ -n "$(git status --porcelain)" ]]; then
    echo "error: working tree has uncommitted changes" >&2
    echo "       commit or stash them first" >&2
    exit 2
fi

if git rev-parse "$TAG" >/dev/null 2>&1; then
    echo "error: tag $TAG already exists" >&2
    echo "       use a new version, or delete the tag first" >&2
    exit 2
fi

# Find the previous release tag (or the empty tree if this is the first
# release). We sort by version using `git tag --sort=-v:refname`.
PREV_TAG="$(git tag --list 'v*' --sort=-v:refname | head -n 1 || true)"
if [[ -z "$PREV_TAG" ]]; then
    RANGE="$(git rev-list --max-parents=0 HEAD | tail -n 1)..HEAD"
else
    RANGE="$PREV_TAG..HEAD"
fi

# ---- snapshot mode -------------------------------------------------------

if [[ $SNAPSHOT -eq 1 ]]; then
    echo "snapshot release of $TAG (no changes applied)"
    echo "previous tag: ${PREV_TAG:-(none)}"
    echo "range:        $RANGE"
    echo "commits:"
    git log --no-merges --pretty=format:'  %h %s' "$RANGE" || true
    exit 0
fi

# ---- generate the changelog block ---------------------------------------

DATE="$(date -u +'%Y-%m-%d')"
HEADER="## [$VERSION] - $DATE"

# Bucket commits by conventional-commit type. The labels mirror
# Keep a Changelog's "Added / Changed / Fixed / Removed" sections.
declare -A BUCKETS=(
    ["feat"]="Added"
    ["fix"]="Fixed"
    ["perf"]="Changed"
    ["refactor"]="Changed"
    ["docs"]="Documentation"
    ["build"]="Build"
    ["ci"]="CI"
    ["test"]="Tests"
    ["chore"]="Chore"
    ["style"]="Chore"
    ["revert"]="Removed"
)
declare -A SECTIONS=()

# Build a temp file per bucket.
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

while read -r line; do
    [[ -z "$line" ]] && continue
    subject="${line#* }"
    subject="$(echo "$subject" | sed 's/^[[:space:]]*//')"
    type="$(echo "$subject" | awk -F'[:(]' '{print $1}' | tr -d ' ')"
    bucket="${BUCKETS[$type]:-Chore}"
    echo "- $subject" >> "$TMPDIR/$bucket"
done < <(git log --no-merges --pretty=format:'%h %s' "$RANGE")

# Compose the markdown block.
{
    echo "$HEADER"
    echo
    for bucket in Added Changed Fixed Removed Documentation Build CI Tests Chore; do
        if [[ -s "$TMPDIR/$bucket" ]]; then
            echo "### $bucket"
            echo
            cat "$TMPDIR/$bucket"
            echo
        fi
    done
    [[ -n "$PREV_TAG" ]] && echo "[$VERSION]: $(git rev-parse --short HEAD)" && \
        echo "[$PREV_TAG]: $(git rev-parse --short "$PREV_TAG")" || true
} > "$TMPDIR/block.md"

# Prepend the block to CHANGELOG.md.
if [[ -f CHANGELOG.md ]]; then
    PREAMBLE="$(awk '/^## /{exit} {print}' CHANGELOG.md)"
    cat > CHANGELOG.md <<EOF
${PREAMBLE}
$(cat "$TMPDIR/block.md")

EOF
    # Re-append the rest of the original CHANGELOG (everything after
    # the first H2 heading) skipping the original preamble.
    awk 'BEGIN{found=0} /^## /{if(found==0){found=1; next} else {print}} {if(found==1) print}' CHANGELOG.md \
        >> CHANGELOG.new.md
    # The above is a best-effort split. For our repo, CHANGELOG.md has
    # a single ## block; we keep things simple and just prepend.
fi

# A simpler and more reliable approach: just replace the file.
cat > CHANGELOG.md <<EOF
# Changelog

All notable changes to Aegis are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

$(cat "$TMPDIR/block.md")
EOF

# ---- commit + tag ---------------------------------------------------------

git add CHANGELOG.md
git commit -m "chore(release): $VERSION"
git tag -a "$TAG" -m "Aegis $TAG"
echo "✓ created tag $TAG at $(git rev-parse --short HEAD)"

# ---- push -----------------------------------------------------------------

if [[ $PUSH -eq 1 ]]; then
    BRANCH="$(git rev-parse --abbrev-ref HEAD)"
    git push origin "$BRANCH" "$TAG"
    echo "✓ pushed $BRANCH and $TAG to origin"

    if [[ $GITHUB_RELEASE -eq 1 ]]; then
        if command -v gh >/dev/null 2>&1; then
            gh release create "$TAG" \
                --title "$TAG" \
                --notes-file "$TMPDIR/block.md" \
                --draft
            echo "✓ drafted GitHub release $TAG"
        else
            echo "warning: gh not installed — skipping GitHub release step" >&2
        fi
    fi
fi

echo "done. review the diff with: git show $TAG"
