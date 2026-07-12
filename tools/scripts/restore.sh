#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Aegis — restore the working tree to a previous release.
#
# Usage:
#   tools/scripts/restore.sh v0.1.0          # checkout tag v0.1.0
#   tools/scripts/restore.sh v0.1.0 --hard  # also reset the branch to it
#
# This never deletes any tag, branch, or reflog entry, so you can
# always come back from any state.

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

TARGET="${1:-}"
HARD=0

[[ -z "$TARGET" ]] && { echo "usage: $0 <tag|commit> [--hard]" >&2; exit 2; }
shift 2>/dev/null || true
[[ "${1:-}" == "--hard" ]] && HARD=1

# ---- safety nets --------------------------------------------------------

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "error: not inside a git work tree" >&2
    exit 2
fi

if ! git rev-parse --verify "$TARGET" >/dev/null 2>&1; then
    echo "error: target '$TARGET' not found" >&2
    echo "       available releases:" >&2
    git tag --list 'v*' | head -n 20 | sed 's/^/         /'
    exit 2
fi

# Stash any in-progress work so we can come back to it.
if [[ -n "$(git status --porcelain)" ]]; then
    STASH="restore-$(date -u +'%Y%m%dT%H%M%SZ')"
    git stash push -u -m "$STASH"
    echo "✓ stashed working tree as '$STASH' (use 'git stash list' to find it)"
fi

# Create a safety branch at HEAD so we never lose the current state,
# even with --hard.
SAFETY="safety/$(date -u +'%Y%m%dT%H%M%SZ')"
git branch "$SAFETY"
echo "✓ created safety branch '$SAFETY' at $(git rev-parse --short HEAD)"

# ---- restore ------------------------------------------------------------

if [[ $HARD -eq 1 ]]; then
    BRANCH="$(git rev-parse --abbrev-ref HEAD)"
    git fetch --all
    git reset --hard "$TARGET"
    echo "✓ hard-reset $BRANCH to $TARGET"
else
    git checkout "$TARGET"
    echo "✓ checked out $TARGET (read-only inspection — your branch is intact)"
fi

echo ""
echo "recovery:"
echo "  to return to where you were:  git checkout ${SAFETY%%/*}  # safety branch"
echo "  to pop the stashed work:      git stash pop"
