#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# install-pre-push.sh — install a git pre-push hook that runs
# `tools/scripts/pre-pr.sh` before any push. Idempotent: re-running
# it replaces the hook content rather than stacking hooks.
#
# Usage:
#   tools/scripts/install-pre-push.sh
#
# The hook lives at .git/hooks/pre-push. The hook itself is a
# one-line shell stub that delegates to pre-pr.sh; the
# delegation keeps the hook in lock-step with the canonical
# script (which is the one that gets tested + reviewed).
#
# Removal: `rm .git/hooks/pre-push`.

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT" || exit 1
HOOK="$REPO_ROOT/.git/hooks/pre-push"

if [[ -e "$HOOK" && ! -f "$HOOK" ]]; then
    echo "pre-push path exists but is not a regular file: $HOOK" >&2
    exit 1
fi

cat > "$HOOK" <<'EOF'
#!/usr/bin/env bash
# Auto-installed by tools/scripts/install-pre-push.sh. The body
# is regenerated on every install; edit the canonical script
# (tools/scripts/pre-pr.sh) instead of this stub.
set -e
REPO_ROOT="$(git rev-parse --show-toplevel)"
exec "$REPO_ROOT/tools/scripts/pre-pr.sh"
EOF
chmod +x "$HOOK"

echo "Installed: $HOOK"
echo "Pushes will now run pre-pr.sh before any ref lands on origin."
echo "Remove with: rm $HOOK"
