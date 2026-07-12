#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Aegis — create a local backup bundle: git bundle + manifest + sha256.
#
# Usage:
#   tools/scripts/backup.sh                      # → backups/aegis-<date>.bundle
#   tools/scripts/backup.sh /path/to/backup/dir  # custom output directory
#
# Restore from a backup:
#   git clone backups/aegis-<date>.bundle aegis-restored
#   cd aegis-restored && git checkout <tag-or-branch>

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

OUT_DIR="${1:-backups}"
mkdir -p "$OUT_DIR"

DATE="$(date -u +'%Y%m%dT%H%M%SZ')"
BRANCH="$(git rev-parse --abbrev-ref HEAD)"
HEAD_SHA="$(git rev-parse --short HEAD)"
BUNDLE="$OUT_DIR/aegis-${BRANCH}-${DATE}-${HEAD_SHA}.bundle"

# Bundle the entire repository (all refs: branches, tags, reflog) into
# a single file. `git bundle create` is the canonical, lossless way.
git bundle create "$BUNDLE" --all

# Manifest: every tag + branch + a list of all commits.
{
    echo "# Aegis backup manifest"
    echo "created: $DATE"
    echo "source:  $REPO_ROOT"
    echo "branch:  $BRANCH"
    echo "head:    $HEAD_SHA"
    echo
    echo "## tags"
    git tag --list | sed 's/^/  /'
    echo
    echo "## branches"
    git branch --list | sed 's/^/  /'
    echo
    echo "## recent commits (HEAD)"
    git log --pretty=format:'  %h %ad %s' --date=short -n 20
} > "$BUNDLE.manifest"

# Hash file.
sha256sum "$BUNDLE" > "$BUNDLE.sha256"

echo "✓ backup: $BUNDLE"
echo "  bundle:    $(du -h "$BUNDLE" | cut -f1)"
echo "  manifest:  $BUNDLE.manifest"
echo "  sha256:    $BUNDLE.sha256"
echo ""
echo "restore: git clone \"$BUNDLE\" aegis-restored"
