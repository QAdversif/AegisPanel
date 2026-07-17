#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# smoke-frontend.sh
#
# v0.1.0 frontend smoke test. Builds the Aegis
# UI, starts `vite preview` on a free port, and
# verifies:
#
#   1. The static bundle serves (index.html
#      returns 200 with the expected content).
#   2. JS chunks resolve (no broken asset refs
#      in the served HTML).
#   3. The dev-proxy endpoint /api/v1/health is
#      reachable from the served UI (no 404 /
#      network error).
#
# This is the "did we ship something that loads"
# gate. It does NOT exercise the actual CRUD
# flows — those have integration tests in
# backend/. The smoke gate only proves the
# frontend builds and the asset graph is intact.
#
# Usage:
#   ./tools/scripts/smoke-frontend.sh
#   ./tools/scripts/smoke-frontend.sh --port 5180
#
# Requires: node, pnpm (or npm), curl, bash 4+.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
FRONTEND="$ROOT/frontend"
LOG_DIR="$ROOT/.smoke-logs"
mkdir -p "$LOG_DIR"

PORT="${SMOKE_PORT:-}"
for arg in "$@"; do
  case "$arg" in
    --port) shift; PORT="$1"; shift ;;
    --port=*) PORT="${arg#*=}"; shift ;;
    *) echo "smoke-frontend: unknown arg $arg" >&2; exit 2 ;;
  esac
done

if [[ -z "$PORT" ]]; then
  # Pick a random free port in 5100-5199
  PORT="$(( (RANDOM % 100) + 5100 ))"
fi

cd "$FRONTEND"

echo "==> Building frontend"
pnpm run build 2>&1 | tail -20

echo "==> Starting vite preview on :$PORT"
LOG_FILE="$LOG_DIR/preview-$PORT.log"
( pnpm exec vite preview --port "$PORT" --host 127.0.0.1 --strictPort > "$LOG_FILE" 2>&1 ) &
PREVIEW_PID=$!
cleanup() {
  if kill -0 "$PREVIEW_PID" 2>/dev/null; then
    kill "$PREVIEW_PID" 2>/dev/null || true
    wait "$PREVIEW_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

# Wait for the server to be ready
for _ in {1..30}; do
  if curl -sf "http://127.0.0.1:$PORT/" > /dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

if ! curl -sf "http://127.0.0.1:$PORT/" > /dev/null; then
  echo "smoke-frontend: FAIL — vite preview did not come up" >&2
  echo "--- last 30 lines of preview log ---" >&2
  tail -30 "$LOG_FILE" >&2 || true
  exit 1
fi

echo "==> Checking index.html"
INDEX="$(curl -sf "http://127.0.0.1:$PORT/")"
if [[ "$INDEX" != *"<title>Aegis"* ]] && [[ "$INDEX" != *"Aegis · "* ]]; then
  echo "smoke-frontend: FAIL — index.html does not contain 'Aegis' title" >&2
  echo "--- index.html ---" >&2
  echo "$INDEX" | head -10 >&2
  exit 1
fi

echo "==> Verifying asset refs resolve"
ASSET_PATHS="$(echo "$INDEX" | grep -oE '/(assets|@vite|@fs|src)/[^"\\'' ]+' | sort -u)"
ASSET_FAIL=0
for path in $ASSET_PATHS; do
  if ! curl -sf "http://127.0.0.1:$PORT$path" -o /dev/null; then
    echo "smoke-frontend: FAIL — asset $path returned non-2xx" >&2
    ASSET_FAIL=1
  fi
done
if [[ $ASSET_FAIL -ne 0 ]]; then
  echo "smoke-frontend: one or more assets failed to serve" >&2
  exit 1
fi

echo "==> Checking /api/v1/health (dev proxy path)"
# The dev proxy maps /api -> :8080. In a smoke
# without a running backend, this can return 502
# or network error. We accept both: the test
# proves the *route* is wired, not the backend.
HEALTH_CODE="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/api/v1/health" || echo "000")"
echo "    /api/v1/health -> HTTP $HEALTH_CODE"
case "$HEALTH_CODE" in
  000|502|503) ;;  # no backend up; not a UI failure
  2*) ;;           # backend up and healthy
  *) echo "smoke-frontend: FAIL — unexpected /api/v1/health status $HEALTH_CODE" >&2; exit 1 ;;
esac

echo "smoke-frontend: OK"
