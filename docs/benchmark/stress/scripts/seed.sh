#!/usr/bin/env bash
set -euo pipefail

SCALE="${1:?Usage: seed.sh <count> [gateway_url] [admin_key] [upstream_target]}"
GATEWAY_URL="${2:-http://localhost:18080}"
ADMIN_KEY="${3:-local-dev-admin-key}"
UPSTREAM_TARGET="${4:-host.docker.internal:9001}"
CONCURRENCY="${SEED_CONCURRENCY:-20}"

ADMIN="${GATEWAY_URL}/apisix/admin"

log() { printf "\033[36m[seed]\033[0m %s\n" "$*"; }
err() { printf "\033[31m[seed]\033[0m %s\n" "$*" >&2; }

create_resource() {
    local kind="$1" id="$2" body="$3"
    local status
    status=$(curl -s -o /dev/null -w "%{http_code}" \
        -X PUT "${ADMIN}/${kind}/${id}" \
        -H "X-API-KEY: ${ADMIN_KEY}" \
        -H "Content-Type: application/json" \
        -d "${body}" \
        --connect-timeout 5 --max-time 10)
    if [[ "$status" != "200" && "$status" != "201" ]]; then
        err "Failed ${kind}/${id}: HTTP ${status}"
        return 1
    fi
}

export -f create_resource err
export ADMIN ADMIN_KEY

log "Seeding ${SCALE} upstreams + services + routes (concurrency=${CONCURRENCY})"
log "Gateway: ${GATEWAY_URL}  Upstream target: ${UPSTREAM_TARGET}"

START_TIME=$(date +%s)

# --- Upstreams ---
log "Creating ${SCALE} upstreams..."
UP_START=$(date +%s)
seq 0 $((SCALE - 1)) | xargs -P "${CONCURRENCY}" -I {} bash -c \
    'create_resource upstreams "stress-up-{}" "{\"type\":\"roundrobin\",\"scheme\":\"http\",\"nodes\":{\"'"${UPSTREAM_TARGET}"'\":1}}"'
UP_END=$(date +%s)
log "Upstreams done in $((UP_END - UP_START))s"

# --- Services ---
log "Creating ${SCALE} services..."
SVC_START=$(date +%s)
seq 0 $((SCALE - 1)) | xargs -P "${CONCURRENCY}" -I {} bash -c \
    'create_resource services "stress-svc-{}" "{\"upstream_id\":\"stress-up-{}\"}"'
SVC_END=$(date +%s)
log "Services done in $((SVC_END - SVC_START))s"

# --- Routes ---
log "Creating ${SCALE} routes..."
RT_START=$(date +%s)
seq 0 $((SCALE - 1)) | xargs -P "${CONCURRENCY}" -I {} bash -c \
    'create_resource routes "stress-rt-{}" "{\"uri\":\"/stress/{}\",\"service_id\":\"stress-svc-{}\",\"methods\":[\"GET\",\"POST\"]}"'
RT_END=$(date +%s)
log "Routes done in $((RT_END - RT_START))s"

END_TIME=$(date +%s)
TOTAL=$((END_TIME - START_TIME))

# --- Verify ---
ROUTE_COUNT=$(curl -s -H "X-API-KEY: ${ADMIN_KEY}" \
    "${ADMIN}/routes?page_size=1" | python3 -c "import sys,json; print(json.load(sys.stdin).get('total',0))" 2>/dev/null || echo "?")

log "Seed complete: ${SCALE} resources in ${TOTAL}s (routes in etcd: ${ROUTE_COUNT})"

# Smoke test one route
SMOKE_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${GATEWAY_URL}/stress/0" \
    -H "Content-Type: application/json" -d '{"test":true}' --max-time 5)
if [[ "$SMOKE_STATUS" == "200" ]]; then
    log "Smoke test /stress/0 → 200 OK"
else
    err "Smoke test /stress/0 → HTTP ${SMOKE_STATUS}"
fi
