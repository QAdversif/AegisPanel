#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# smoke-local.sh
#
# v0.9.0-pre local end-to-end smoke test. Spins up
# the docker-compose dev stack, brings the Go
# panel up against a hybrid backend (memory auth
# so the seeded `admin / aegis-dev-password`
# credential works out of the box; pg for every
# other store so the production codepath is what
# we're exercising), then walks the panel through
# the minimum set of happy-path API calls a real
# operator would run on day 1.
#
# The smoke is intentionally narrow: it is the
# "did the v0.7.1 panel boot, accept a login, and
# round-trip the major CRUD surfaces" gate. It is
# NOT a full end-to-end test (the Go test suite at
# `backend/internal/...` covers the unit + pg
# integration cases; this is the last-mile "stack
# really wires together" check).
#
# Usage:
#   ./tools/scripts/smoke-local.sh           # full local (docker compose up)
#   ./tools/scripts/smoke-local.sh --no-up   # assume compose is already up
#   ./tools/scripts/smoke-local.sh --down    # tear compose down at the end
#   ./tools/scripts/smoke-local.sh --keep    # keep the panel running for manual poking
#   ./tools/scripts/smoke-local.sh --port N  # panel listens on N (default 8088)
#
# Required:
#   - bash 4+, curl, jq
#   - docker + docker compose v2 (or an
#     external postgres reachable at the DSN
#     below — the script picks that up from
#     AEGIS_POSTGRES_DSN if set)
#   - go 1.24+ (for the panel binary)
#   - age-keygen (filippo.io/age) for the
#     transient webhook secret envelope key
#
# Exit code 0 = every check passed. Non-zero =
# the failing step's first failing line is
# printed to stderr (and to the per-step
# $ROOT/.smoke-logs/ subdir).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LOG_DIR="$ROOT/.smoke-logs"
mkdir -p "$LOG_DIR"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_LOG="$LOG_DIR/smoke-$STAMP.log"

# --- Args --------------------------------------------------------------------

NO_UP=0
DO_DOWN=0
KEEP=0
PORT="${SMOKE_PORT:-8088}"
for arg in "$@"; do
  case "$arg" in
    --no-up) NO_UP=1; shift ;;
    --down) DO_DOWN=1; shift ;;
    --keep) KEEP=1; shift ;;
    --port) shift; PORT="$1"; shift ;;
    --port=*) PORT="${arg#*=}"; shift ;;
    --help|-h)
      sed -n '2,40p' "$0"; exit 0 ;;
    *) echo "smoke-local: unknown arg $arg" >&2; exit 2 ;;
  esac
done

# --- Logging -----------------------------------------------------------------

# tee everything to RUN_LOG, mirror to stdout. The
# `step` helper makes the per-step headers readable
# in both streams.
exec > >(tee -a "$RUN_LOG") 2>&1

step() {
  printf '\n==> %s\n' "$*"
}

die() {
  printf '\n[FAIL] %s\n' "$*" >&2
  printf 'Logs:   %s\n' "$RUN_LOG" >&2
  printf 'Per-step logs: %s/<step>.log\n' "$LOG_DIR" >&2
  exit 1
}

# --- Pre-flight --------------------------------------------------------------

step "Pre-flight"
command -v docker >/dev/null || die "docker not installed"
command -v curl   >/dev/null || die "curl not installed"
command -v jq     >/dev/null || die "jq not installed"
command -v go     >/dev/null || die "go not installed"
command -v age-keygen >/dev/null || die "age-keygen not installed"
[ -d "$ROOT/backend" ]  || die "backend/ not found at $ROOT/backend"
[ -d "$ROOT/deploy/docker" ] || die "deploy/docker/ not found at $ROOT/deploy/docker"
echo "  root   = $ROOT"
echo "  port   = $PORT"
echo "  log    = $RUN_LOG"

# --- DSN ---------------------------------------------------------------------

# If AEGIS_POSTGRES_DSN is set in the env, use that
# (CI mode / external pg). Otherwise, point at the
# docker-compose default (`aegis / aegis` user on
# `aegis` db). The script does not own the DSN when
# the operator supplied one — the operator is also
# responsible for the up/down of the backing DB.
PG_DSN="${AEGIS_POSTGRES_DSN:-postgres://aegis:aegis@localhost:5432/aegis?sslmode=disable}"
echo "  pg dsn = $PG_DSN"

# --- docker compose up -------------------------------------------------------

PANEL_PID=""
PANEL_LOG="$LOG_DIR/panel-$STAMP.log"
KEY_DIR="$(mktemp -d)"
trap 'cleanup' EXIT

cleanup() {
  if [ -n "$PANEL_PID" ] && kill -0 "$PANEL_PID" 2>/dev/null; then
    step "Stopping panel (pid $PANEL_PID)"
    kill "$PANEL_PID" 2>/dev/null || true
    wait "$PANEL_PID" 2>/dev/null || true
  fi
  if [ "$DO_DOWN" = "1" ]; then
    step "Tearing down docker-compose dev stack"
    ( cd "$ROOT/deploy/docker" && PROJECT=aegis-smoke docker compose -f docker-compose.dev.yml down -v ) \
      || echo "  (compose down failed; continuing)" >&2
  fi
  if [ -d "$KEY_DIR" ]; then
    rm -rf "$KEY_DIR"
  fi
}

if [ "$NO_UP" = "0" ]; then
  step "Bringing up docker-compose dev stack"
  ( cd "$ROOT/deploy/docker" && \
    PROJECT=aegis-smoke docker compose -f docker-compose.dev.yml up -d postgres )
fi

# --- Wait for postgres -------------------------------------------------------

step "Waiting for postgres to be ready"
DEADLINE=$(( $(date +%s) + 60 ))
until ( cd "$ROOT/deploy/docker" && \
        PROJECT=aegis-smoke docker compose -f docker-compose.dev.yml exec -T postgres \
          pg_isready -U aegis -d aegis ) >/dev/null 2>&1; do
  if [ "$(date +%s)" -ge "$DEADLINE" ]; then
    die "postgres did not become ready within 60s"
  fi
  sleep 1
done
echo "  postgres OK"

# --- Generate a transient age keypair ---------------------------------------

step "Generating transient age keypair for the webhook envelope"
AGE_KEY_FILE="$KEY_DIR/age.key"
age-keygen -o "$AGE_KEY_FILE" 2>"$LOG_DIR/age-keygen-$STAMP.log"
AGE_PUBLIC="$(head -1 "$AGE_KEY_FILE")"
[ -n "$AGE_PUBLIC" ] || die "age-keygen did not emit a public key on stdout"
echo "  recipient = $AGE_PUBLIC"

# --- Build the panel binary --------------------------------------------------

step "Building the panel binary"
( cd "$ROOT/backend" && \
  go build -trimpath -ldflags "-s -w" -o "$KEY_DIR/aegis" ./cmd/aegis ) \
  || die "go build failed"
[ -x "$KEY_DIR/aegis" ] || die "aegis binary not produced"

# --- Launch the panel --------------------------------------------------------

step "Launching the panel on :$PORT"
# Hybrid backend: memory auth (so the seeded
# `admin / aegis-dev-password` works out of the
# box — see main.go seedAdmin); pg for every
# other store, so the production codepath is
# what we're exercising. The transient age key
# is loaded via the AEGIS_WEBHOOKS_SECRET_AGE_*
# pair, so a fresh secret created in the smoke
# round-trips through real crypto.
AEGIS_POSTGRES_DSN="$PG_DSN" \
AEGIS_AUTH_BACKEND=memory \
AEGIS_NODES_BACKEND=pg \
AEGIS_HOSTS_BACKEND=pg \
AEGIS_INBOUNDS_BACKEND=pg \
AEGIS_USERS_BACKEND=pg \
AEGIS_PLANS_BACKEND=pg \
AEGIS_WEBHOOKS_BACKEND=pg \
AEGIS_PANELCFG_BACKEND=pg \
AEGIS_AUDITS_BACKEND=pg \
AEGIS_SUBSCRIPTION_BACKEND=pg \
AEGIS_WEBHOOKS_RETRY_WORKER_ENABLED=true \
AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS="$AGE_PUBLIC" \
AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE="$AGE_KEY_FILE" \
AEGIS_JWT_SECRET="smoke-jwt-secret-not-for-prod" \
AEGIS_LISTEN=":$PORT" \
AEGIS_ENV=dev \
  "$KEY_DIR/aegis" > "$PANEL_LOG" 2>&1 &
PANEL_PID=$!
echo "  panel pid = $PANEL_PID, log = $PANEL_LOG"

# --- Wait for /api/v1/health -------------------------------------------------

step "Waiting for /api/v1/health"
DEADLINE=$(( $(date +%s) + 60 ))
HEALTH_URL="http://127.0.0.1:$PORT/api/v1/health"
until curl -sf "$HEALTH_URL" >/dev/null 2>&1; do
  if [ "$(date +%s)" -ge "$DEADLINE" ]; then
    echo "--- last 40 lines of panel log ---" >&2
    tail -40 "$PANEL_LOG" >&2 || true
    die "panel did not become ready within 60s (url=$HEALTH_URL)"
  fi
  sleep 1
done
HEALTH_BODY="$(curl -sf "$HEALTH_URL")"
echo "  health = $HEALTH_BODY"

# --- Login -------------------------------------------------------------------

step "Logging in as the seeded admin"
LOGIN_BODY="$(curl -sf -X POST \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"aegis-dev-password"}' \
  "http://127.0.0.1:$PORT/api/v1/auth/login")"
TOKEN="$(printf '%s' "$LOGIN_BODY" | jq -er '.access_token // .token // .jwt // empty')"
[ -n "$TOKEN" ] || die "no token in login response: $LOGIN_BODY"
echo "  token length = ${#TOKEN}"

AUTH=( -H "Authorization: Bearer $TOKEN" )

# --- /auth/me ----------------------------------------------------------------

step "GET /api/v1/auth/me"
ME="$(curl -sf "${AUTH[@]}" "http://127.0.0.1:$PORT/api/v1/auth/me")"
echo "  me = $ME"
printf '%s' "$ME" | jq -e '.username == "admin"' >/dev/null \
  || die "auth/me did not return username=admin: $ME"

# --- /api/v1/health is also reachable while bearer is attached -------------

step "Authenticated /api/v1/health"
curl -sf "${AUTH[@]}" "$HEALTH_URL" >/dev/null \
  || die "auth'd /health failed"

# --- users CRUD --------------------------------------------------------------

step "POST /api/v1/users (create)"
USERNAME="smoke-user-$RANDOM"
CREATE_USER_BODY="$(curl -sf "${AUTH[@]}" -X POST \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USERNAME\",\"status\":\"active\",\"device_limit\":1}" \
  "http://127.0.0.1:$PORT/api/v1/users")"
USER_ID="$(printf '%s' "$CREATE_USER_BODY" | jq -er '.id // .user.id // empty')"
[ -n "$USER_ID" ] || die "no id in create-user response: $CREATE_USER_BODY"
echo "  created user id = $USER_ID"

step "GET /api/v1/users (list)"
LIST="$(curl -sf "${AUTH[@]}" "http://127.0.0.1:$PORT/api/v1/users")"
printf '%s' "$LIST" | jq -e --arg id "$USER_ID" \
  '(.users // .) | any(.id == $id)' >/dev/null \
  || die "list did not include the user we just created: $LIST"

step "DELETE /api/v1/users/{id}"
curl -sf "${AUTH[@]}" -X DELETE "http://127.0.0.1:$PORT/api/v1/users/$USER_ID" >/dev/null \
  || die "delete-user failed"

# --- plans CRUD --------------------------------------------------------------

step "POST /api/v1/plans (create)"
PLAN_BODY="$(curl -sf "${AUTH[@]}" -X POST \
  -H 'Content-Type: application/json' \
  -d '{"name":"smoke-plan","reset_period":"never","duration_seconds":2592000,"traffic_limit_bytes":0,"device_limit":1}' \
  "http://127.0.0.1:$PORT/api/v1/plans")"
PLAN_ID="$(printf '%s' "$PLAN_BODY" | jq -er '.id // .plan.id // empty')"
[ -n "$PLAN_ID" ] || die "no id in create-plan response: $PLAN_BODY"
echo "  plan id = $PLAN_ID"
curl -sf "${AUTH[@]}" -X DELETE "http://127.0.0.1:$PORT/api/v1/plans/$PLAN_ID" >/dev/null \
  || die "delete-plan failed"

# --- webhooks round-trip ----------------------------------------------------

step "POST /api/v1/webhooks (create endpoint)"
HOOK_BODY="$(curl -sf "${AUTH[@]}" -X POST \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://httpbin.org/anything","secret":"smoke-fixture-secret-aaaaaaaaaaaaaaaaaa","enabled":true}' \
  "http://127.0.0.1:$PORT/api/v1/webhooks")"
HOOK_ID="$(printf '%s' "$HOOK_BODY" | jq -er '.id // .webhook.id // empty')"
[ -n "$HOOK_ID" ] || die "no id in create-webhook response: $HOOK_BODY"
echo "  webhook id = $HOOK_ID"

step "POST /api/v1/webhooks/{id}/test (synthetic event)"
TEST_BODY="$(curl -sf "${AUTH[@]}" -X POST \
  "http://127.0.0.1:$PORT/api/v1/webhooks/$HOOK_ID/test")"
echo "  test = $TEST_BODY"
# httpbin.org may rate-limit; the smoke does not
# require 2xx from the remote, only that the panel
# itself accepted the request and produced a
# delivery row.
printf '%s' "$TEST_BODY" | jq -e '.status // .result // .delivery' >/dev/null \
  || die "test event response shape unexpected: $TEST_BODY"

step "GET /api/v1/webhooks/{id}/deliveries"
DELIV="$(curl -sf "${AUTH[@]}" \
  "http://127.0.0.1:$PORT/api/v1/webhooks/$HOOK_ID/deliveries")"
echo "  deliveries = $DELIV"
# We accept both "no deliveries yet" and a populated
# list — httpbin.org may or may not have responded
# before the smoke exits.
printf '%s' "$DELIV" | jq -e '.deliveries // .[]' >/dev/null \
  || die "deliveries response shape unexpected: $DELIV"

step "DELETE /api/v1/webhooks/{id}"
curl -sf "${AUTH[@]}" -X DELETE "http://127.0.0.1:$PORT/api/v1/webhooks/$HOOK_ID" >/dev/null \
  || die "delete-webhook failed"

# --- Done --------------------------------------------------------------------

if [ "$KEEP" = "1" ]; then
  trap - EXIT
  printf '\n[smoke] OK (panel still running pid=%d, port=%d)\n' "$PANEL_PID" "$PORT"
  exit 0
fi

printf '\n[smoke] OK\n'
