#!/usr/bin/env bash
# tools/scripts/install-hooks.sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Install the pre-commit hook that runs
# `tools/scripts/check-sensitive.sh --staged` before every
# commit. Refuses the commit if the staged diff contains any
# banned pattern (see AGENTS.md §"Banned patterns").
#
# Idempotent: re-running replaces the hook content rather
# than stacking hooks. The hook is a small shell stub that
# delegates to check-sensitive.sh; keeping the delegation
# in sync with the canonical script.
#
# Usage:
#   tools/scripts/install-hooks.sh
#
# Removal:
#   rm .git/hooks/pre-commit

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
HOOK="$REPO_ROOT/.git/hooks/pre-commit"

cat > "$HOOK" <<'HOOK_EOF'
#!/usr/bin/env bash
# Auto-installed by tools/scripts/install-hooks.sh.
# Runs the sensitive-pattern scanner against staged files.
# See AGENTS.md §"Banned patterns" + tools/scripts/check-sensitive.sh.

set -e
exec bash "$(git rev-parse --show-toplevel)/tools/scripts/check-sensitive.sh" --staged
HOOK_EOF

chmod +x "$HOOK"
echo "installed pre-commit hook at $HOOK"
echo "  scanner: tools/scripts/check-sensitive.sh --staged"
echo "  block-on-leak: yes (exit 1 aborts the commit)"
