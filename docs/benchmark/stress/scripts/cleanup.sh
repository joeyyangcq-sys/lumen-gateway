#!/usr/bin/env bash
set -euo pipefail

SCALE="${1:?Usage: cleanup.sh <count> [gateway_url] [admin_key]}"
GATEWAY_URL="${2:-http://localhost:18080}"
ADMIN_KEY="${3:-local-dev-admin-key}"
CONCURRENCY="${SEED_CONCURRENCY:-20}"

ADMIN="${GATEWAY_URL}/apisix/admin"

log() { printf "\033[33m[cleanup]\033[0m %s\n" "$*"; }

delete_resource() {
    local kind="$1" id="$2"
    curl -s -o /dev/null -X DELETE "${ADMIN}/${kind}/${id}" \
        -H "X-API-KEY: ${ADMIN_KEY}" \
        --connect-timeout 5 --max-time 10 || true
}

export -f delete_resource
export ADMIN ADMIN_KEY

log "Cleaning up ${SCALE} routes + services + upstreams (concurrency=${CONCURRENCY})"

START=$(date +%s)

# Reverse dependency order: routes → services → upstreams
log "Deleting routes..."
seq 0 $((SCALE - 1)) | xargs -P "${CONCURRENCY}" -I {} bash -c 'delete_resource routes "stress-rt-{}"'

log "Deleting services..."
seq 0 $((SCALE - 1)) | xargs -P "${CONCURRENCY}" -I {} bash -c 'delete_resource services "stress-svc-{}"'

log "Deleting upstreams..."
seq 0 $((SCALE - 1)) | xargs -P "${CONCURRENCY}" -I {} bash -c 'delete_resource upstreams "stress-up-{}"'

END=$(date +%s)

ROUTE_COUNT=$(curl -s -H "X-API-KEY: ${ADMIN_KEY}" \
    "${ADMIN}/routes?page_size=1" | python3 -c "import sys,json; print(json.load(sys.stdin).get('total',0))" 2>/dev/null || echo "?")

log "Cleanup done in $((END - START))s (routes remaining: ${ROUTE_COUNT})"
