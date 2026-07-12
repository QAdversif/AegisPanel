#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Aegis — create a Conventional Commits feature/fix branch.
#
# Usage:
#   tools/scripts/branch-start.sh feat backend/nodes-bootstrap
#   tools/scripts/branch-start.sh fix frontend/dashboard-null-render
#
# The first argument is the type (feat, fix, docs, refactor, …);
# the second is the scope/name in slash form.

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

TYPE="${1:-}"
NAME="${2:-}"

[[ -z "$TYPE" || -z "$NAME" ]] && {
    echo "usage: $0 <type> <scope/name>" >&2
    echo "       e.g. $0 feat backend/nodes-bootstrap" >&2
    exit 2
}

case "$TYPE" in
    feat|fix|docs|style|refactor|perf|test|build|chore|revert) ;;
    *) echo "error: unknown type '$TYPE'" >&2; exit 2 ;;
esac

BRANCH="${TYPE}/${NAME}"
BASE="$(git rev-parse --abbrev-ref HEAD)"

if git rev-parse --verify "$BRANCH" >/dev/null 2>&1; then
    echo "error: branch '$BRANCH' already exists" >&2
    exit 2
fi

git fetch origin "$BASE" 2>/dev/null || true
git checkout -b "$BRANCH" "$BASE"
echo "✓ created and checked out $BRANCH (base: $BASE)"
