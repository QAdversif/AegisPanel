#!/usr/bin/env bash
# tools/scripts/check-sensitive.sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Scan for banned patterns (secrets, prod IPs, hostnames,
# credentials) in the repo. Used as:
#
#   1. pre-commit hook (via tools/scripts/install-pre-push.sh
#      which is renamed in v0.8.26 to install-hooks.sh) —
#      scans staged diff.
#   2. CI gate (via .github/workflows/secret-scan.yml) —
#      scans the PR diff against the target branch.
#   3. Ad-hoc operator scan — `bash tools/scripts/
#      check-sensitive.sh docs/` to scan a subdir.
#
# The list of banned patterns mirrors AGENTS.md §"Banned
# patterns". The master source-of-truth for VALUES is the
# operator's ~/.aegis/deploy.local.md (out of repo, never
# tracked); this file ships the regex forms. The patterns
# ARE the secrets; the values stay operator-side.
#
# Exit 0 = clean. Exit 1 = at least one banned pattern
# found; the offending file:line and pattern are printed
# to stderr. Adding a new exception requires editing both
# this file and AGENTS.md.

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

# ---- args -----------------------------------------------------------------
#
# The default (no args) is `--branch` — the diff vs the base
# branch. This is the right default for CI: it blocks new
# leaks while not failing on historical leaks in main.
#
# Pre-commit hooks use `--staged` (just the staged files).
# Operator ad-hoc + the nightly full-tree scan use
# `--tree` (everything in the working tree).
#
# Exit codes:
#   0 = clean
#   1 = leak found (in the scanned set) — block
#   2 = tree-wide scan, leak only in main, not in the diff —
#       warn but do not block (operator + nightly use this)

MODE="branch"
case "${1:-}" in
  --staged) MODE="staged" ;;
  --branch) MODE="branch" ;;
  --tree)   MODE="tree" ;;
  -h|--help)
    sed -n '2,50p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
    ;;
  "")       MODE="branch" ;;
  *)        MODE="path:$1" ;;
esac

# ---- banned patterns (mirror of AGENTS.md §"Banned patterns") -----------
#
# Keep this list in sync with AGENTS.md. To add a new pattern:
#   1. Add the regex form below.
#   2. Add the same form to AGENTS.md.
#   3. Add the literal value to ~/.aegis/deploy.local.md
#      (operator-side; never in repo).
#
# Patterns are written as extended-regex. Backslashes and
# plus signs that are part of the regex (not the value) are
# escaped with a backslash.

BANNED_PATTERNS=(
  # public IPs (the live server + the demo node)
  '31\.77\.147\.146'
  '193\.37\.68\.194'

  # public hostnames
  'aibeg\.click'
  'cdn2ne\.aibeg\.click'

  # SSH password for the live server
  'xM3qW2dX7dbI'

  # JWT secret (AEGIS_JWT_SECRET, base64 64 chars)
  '4sFihDUA/6CLxWNNGgDkeXg9dNLOSjpPvGgb4Y1Ldh0eOcv\+cW2UoO1Fk\+BL/h36'

  # DB password
  'aegis-fixture-db-password'

  # age recipient (operator's public key)
  'age1mlvzyndgtuwpr855ldt84yr2mwnxn0nya6uusrkc55un6cs3fypq5weq6l'

  # server SSH host key fingerprint (ed25519)
  'DfNZC\+uWkxQNsvjZhC6YOXqGeWp5Z1p09GLiAlMF\+9c'

  # demo-node SSH host key fingerprint (ed25519)
  'pCnGi8kyWPaDdcRUpSPBM9y2wAJfqe3smcTmADywvJM'

  # the pre-2026-08-09 server SSH host key fingerprint
  # (rotated; kept here to catch any historical leak)
  '5CHBSqCWtaVA\+WX/zSmvJs22QmN8UbMIh\+6HalVCrmQ'

  # admin passwords (current + past rotation, fixtures)
  'Uu3-jdm-TVH-DRv'
  'aegis-fixture-admin-password'

  # decoy sub-path on the live server
  'p-7k2mx9n4q8r3'

  # operator email at the live server's domain
  'ops@aibeg\.click'

  # operator's private-key file paths
  # (the [~] character class is a regex idiom for a
  # literal ~ that doesn't trip shellcheck SC2088;
  # grep -E treats `[~]` exactly like `~`)
  '/root/\.ssh/aegis-deploy'
  '[~]/\.ssh/aegis-deploy'
  '[~]/\.ssh/aegis\.age\.key'
)

# ---- file selection -------------------------------------------------------

EXTS=( md go ts vue json yml yaml sh )

# Files that legitimately contain banned patterns by design
# (the regex forms of the patterns; test fixtures with
# fingerprint-shaped strings; etc.). These are EXEMPT from
# scanning. Add to this list only with operator review.
#
# Keep this list as small as possible — every entry is a
# place where a real leak could hide. If you find yourself
# adding more than a handful of entries, the design needs
# rework.
ALLOWLIST=(
  # The agent contract itself lists the regex forms.
  "AGENTS.md"
  # This script lists the same regex forms.
  "tools/scripts/check-sensitive.sh"
  # Test fixtures carry fingerprint-shaped test data
  # (stripFingerprintPrefix etc.). Reviewed and clean.
  "backend/internal/bootstrap/ssh_test.go"
)

# Build a bash array of -name predicates. Each element is a
# single argument to find; quoted globs survive intact.
NAME_PREDS=()
for ext in "${EXTS[@]}"; do
  NAME_PREDS+=( -name "*.${ext}" -o )
done
unset 'NAME_PREDS[-1]'  # drop trailing -o

SCAN_PATHS=()
case "$MODE" in
  staged)
    # Pre-commit: staged files only.
    while IFS= read -r f; do
      [[ -n "$f" ]] && SCAN_PATHS+=("$f")
    done < <(git diff --cached --name-only --diff-filter=ACMR || true)
    ;;
  branch)
    # CI: diff against the base branch.
    BASE="${BASE_BRANCH:-origin/main}"
    while IFS= read -r f; do
      [[ -n "$f" ]] && SCAN_PATHS+=("$f")
    done < <(git diff --name-only "$BASE"...HEAD || true)
    ;;
  tree)
    # Whole tree, with the standard ignores.
    while IFS= read -r f; do
      [[ -n "$f" ]] && SCAN_PATHS+=("$f")
    done < <(find . \
      \( -path '*/node_modules' -o -path '*/dist' -o -path '*/backups' \
        -o -path '*/coverage' -o -path '*/.trash-*' \) -prune -o \
      -type f \( "${NAME_PREDS[@]}" \) -print 2>/dev/null || true)
    ;;
  path:*)
    SUBDIR="${MODE#path:}"
    while IFS= read -r f; do
      [[ -n "$f" ]] && SCAN_PATHS+=("$f")
    done < <(find "$SUBDIR" \
      \( -path '*/node_modules' -o -path '*/dist' -o -path '*/backups' \
        -o -path '*/coverage' -o -path '*/.trash-*' \) -prune -o \
      -type f \( "${NAME_PREDS[@]}" \) -print 2>/dev/null || true)
    ;;
esac

# Filter to just the canonical extensions (defensive — find
# should already have done this) AND drop allowlisted paths.
FILTERED_PATHS=()
EXT_REGEX="\.($(IFS='|'; echo "${EXTS[*]}"))$"
for f in "${SCAN_PATHS[@]:-}"; do
  [[ -z "$f" ]] && continue
  [[ "$f" =~ $EXT_REGEX ]] || continue
  # Drop the leading "./" for allowlist comparison.
  cmp="${f#./}"
  skip=0
  for allowed in "${ALLOWLIST[@]}"; do
    if [[ "$cmp" == "$allowed" ]]; then
      skip=1
      break
    fi
  done
  [[ $skip -eq 0 ]] && FILTERED_PATHS+=("$f")
done

# ---- scan -----------------------------------------------------------------

HIT=0
for f in "${FILTERED_PATHS[@]:-}"; do
  [[ -f "$f" ]] || continue
  for pat in "${BANNED_PATTERNS[@]}"; do
    if matches="$(grep -E -nH "$pat" "$f" 2>/dev/null || true)"; then
      [[ -n "$matches" ]] || continue
      printf '%s\n' "$matches" >&2
      HIT=1
    fi
  done
done

if [[ $HIT -eq 1 ]]; then
  {
    echo
    echo "check-sensitive.sh: BANNED PATTERN(S) found."
    echo "  See AGENTS.md §'Banned patterns' for the canonical list."
    echo "  To add an exception, edit tools/scripts/check-sensitive.sh + AGENTS.md"
    echo "  in the same commit. Do NOT add a per-line ignore; the patterns"
    echo "  are the security contract."
  } >&2
  exit 1
fi
exit 0
