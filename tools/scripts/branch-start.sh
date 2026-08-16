#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Aegis — create a Conventional Commits feature/fix branch.
#
# Usage:
#   tools/scripts/branch-start.sh feat backend/nodes-bootstrap
#   tools/scripts/branch-start.sh fix frontend/dashboard-null-render
#   tools/scripts/branch-start.sh feat backend/example --dry-run
#   tools/scripts/branch-start.sh --help
#
# The first argument is the type (feat, fix, docs, refactor, …);
# the second is the scope/name in slash form. `--dry-run` validates
# the would-be state (type, branch name, branch-exists check) without
# mutating the working tree or talking to origin. Exit codes mirror
# the real run: 0 = would succeed, 2 = would fail.

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

# ---- flag parsing ----------------------------------------------------------

DRY_RUN=0
POSITIONAL=()

# First pass: collect positional args + recognize flags in any order.
while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run) DRY_RUN=1; shift ;;
        -h|--help)
            sed -n '2,21p' "$0"
            exit 0
            ;;
        --*) echo "error: unknown flag '$1'" >&2; exit 2 ;;
        *) POSITIONAL+=("$1"); shift ;;
    esac
done

# Restore positional args into $@ for the existing read.
set -- "${POSITIONAL[@]+"${POSITIONAL[@]}"}"

TYPE="${1:-}"
NAME="${2:-}"

[[ -z "$TYPE" || -z "$NAME" ]] && {
    echo "usage: $0 [--dry-run] <type> <scope/name>" >&2
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

# ---- dry-run path ----------------------------------------------------------

if [[ $DRY_RUN -eq 1 ]]; then
    echo "✓ would create branch: $BRANCH (base: $BASE)"
    exit 0
fi

# ---- real run --------------------------------------------------------------

git fetch origin "$BASE" 2>/dev/null || true
git checkout -b "$BRANCH" "$BASE"
echo "✓ created and checked out $BRANCH (base: $BASE)"
