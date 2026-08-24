#!/bin/bash
# AegisPanel fresh install / upgrade wrapper.
#
# What it does (idempotent):
#   1. Ensures the external `aegis-net` Docker network exists.
#   2. Creates /var/lib/aegis/{migrations,known_hosts,backups} with
#      aegis-deploy:aegis-deploy ownership.
#   3. Ensures /etc/aegis/age.key is readable by the panel container
#      (chown 65532:65532, chmod 0640). Required by distroless UID 65532.
#   4. (If /opt/aegis/docker-compose.yml is missing) copies the canonical
#      compose file from this script's repo location to /opt/aegis/.
#      The operator may also place the file there manually — the script
#      leaves it alone if it already exists.
#   5. Runs `docker compose -f /opt/aegis/docker-compose.yml pull && up -d`.
#   6. Smoke-tests the panel's /api/v1/health and prints container status.
#
# Required env:
#   AEGIS_PROD_IP  public IP of THIS server (where the panel binds).
#
# Optional env (or set in /opt/aegis/.env next to the compose file):
#   AEGIS_PANEL_TAG    image tag for aegispanel     (default 0.8.28.6)
#   AEGIS_UI_TAG       image tag for aegispanel-ui  (default v0.8.28.6)
#   AEGIS_ENV_FILE     panel env file               (default /tmp/aegis-v0.8.28.env)
#   AEGIS_UI_CADDYFILE UI Caddyfile                 (default /tmp/ui-Caddyfile)
#
# This script is operator-runnable. It does NOT touch the DB, Redis, NATS,
# or any other service that already has its own docker-run invocation. Use
# the existing `tools/scripts/aegis-deploy-setup.sh` (or the matching
# `-remote.py`) for the one-time server-user setup that must happen first.

set -euo pipefail

: "${AEGIS_PROD_IP:?AEGIS_PROD_IP is required (export AEGIS_PROD_IP=...) }"
AEGIS_PANEL_TAG="${AEGIS_PANEL_TAG:-0.8.28.6}"
AEGIS_UI_TAG="${AEGIS_UI_TAG:-v0.8.28.6}"
AEGIS_ENV_FILE="${AEGIS_ENV_FILE:-/tmp/aegis-v0.8.28.env}"
AEGIS_UI_CADDYFILE="${AEGIS_UI_CADDYFILE:-/tmp/ui-Caddyfile}"

STACK_DIR="/opt/aegis"
COMPOSE_FILE="$STACK_DIR/docker-compose.yml"
# Repo-side canonical compose file (this script's sibling).
# SCRIPT_DIR is the dir of this script when run from tools/scripts/.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_COMPOSE="$SCRIPT_DIR/aegis-stack.yml"

echo "=== AegisPanel install (panel+ui) ==="
echo "AEGIS_PROD_IP=$AEGIS_PROD_IP"
echo "AEGIS_PANEL_TAG=$AEGIS_PANEL_TAG"
echo "AEGIS_UI_TAG=$AEGIS_UI_TAG"
echo "AEGIS_ENV_FILE=$AEGIS_ENV_FILE"
echo "AEGIS_UI_CADDYFILE=$AEGIS_UI_CADDYFILE"

# 1. External network.
if ! docker network inspect aegis-net >/dev/null 2>&1; then
    echo "--- creating aegis-net ---"
    docker network create aegis-net
fi

# 2. Host data dirs.
echo "--- host dirs ---"
for sub in migrations known_hosts backups; do
    if [ ! -d "/var/lib/aegis/$sub" ]; then
        install -d -m 0755 -o aegis-deploy -g aegis-deploy "/var/lib/aegis/$sub"
        echo "  created /var/lib/aegis/$sub"
    fi
done

# 3. /etc/aegis for the age envelope.
if [ -d /etc/aegis ]; then
    if [ -f /etc/aegis/age.key ]; then
        chown 65532:65532 /etc/aegis/age.key
        chmod 0640 /etc/aegis/age.key
        echo "  age.key chown 65532:65532 chmod 0640"
    fi
else
    install -d -m 0750 -o aegis-deploy -g aegis-deploy /etc/aegis
    echo "  created /etc/aegis (operator must drop age.key here before panel can start)"
fi

# 4. Compose file.
install -d -m 0755 "$STACK_DIR"
if [ ! -f "$COMPOSE_FILE" ]; then
    if [ -f "$REPO_COMPOSE" ]; then
        cp "$REPO_COMPOSE" "$COMPOSE_FILE"
        chmod 0644 "$COMPOSE_FILE"
        echo "--- copied $REPO_COMPOSE to $COMPOSE_FILE ---"
    else
        echo "FATAL: $COMPOSE_FILE missing and $REPO_COMPOSE not found." >&2
        echo "  Place a compose file at $COMPOSE_FILE or run from a repo checkout." >&2
        exit 2
    fi
fi

# 5. Pull and up.
echo "--- docker compose pull ---"
( cd "$STACK_DIR" && docker compose pull )
echo "--- docker compose up -d ---"
( cd "$STACK_DIR" && docker compose up -d )

# 6. Smoke.
sleep 3
echo "--- container status ---"
docker ps --filter "name=aegis-(panel|ui)" --format "table {{.Names}}\t{{.Image}}\t{{.Status}}"

echo "--- panel /api/v1/health ---"
HEALTH=$(curl -sS -o /dev/null -w "%{http_code}" "http://${AEGIS_PROD_IP}:8080/api/v1/health" || echo 000)
echo "  HTTP $HEALTH"
if [ "$HEALTH" != "200" ]; then
    echo "  WARN: panel health is not 200. Check 'docker logs aegis-panel --tail 50'." >&2
    exit 3
fi

echo
echo "=== install done ==="
echo "  panel:  http://${AEGIS_PROD_IP}:8080  (use top-level Caddy for public HTTPS)"
echo "  ui:     http://${AEGIS_PROD_IP}:8081  (internal — use top-level Caddy)"
echo "  compose project: $(cd $STACK_DIR && docker compose ls --format json | head -c 200)"


