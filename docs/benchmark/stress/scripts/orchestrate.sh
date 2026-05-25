#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
STRESS_DIR="$(dirname "$SCRIPT_DIR")"
K6_DIR="${STRESS_DIR}/k6"
RESULTS_BASE="${STRESS_DIR}/results"

GATEWAY_URL="${GATEWAY_URL:-http://localhost:18080}"
ADMIN_KEY="${ADMIN_KEY:-local-dev-admin-key}"
UPSTREAM_TARGET="${UPSTREAM_TARGET:-host.docker.internal:9001}"

TIERS="${TIERS:-1000 3000 5000 10000}"
VUS="${VUS:-100}"
DURATION="${DURATION:-30s}"
CONTAINER_NAME="${CONTAINER_NAME:-lumen-gateway-1}"

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
RESULTS_DIR="${RESULTS_BASE}/${TIMESTAMP}"
mkdir -p "${RESULTS_DIR}"

log() { printf "\033[35m[orchestrate]\033[0m %s\n" "$*"; }
separator() { printf "\n%s\n\n" "════════════════════════════════════════════════════════════════"; }

collect_memory() {
    docker stats "${CONTAINER_NAME}" --no-stream --format "{{.MemUsage}}" 2>/dev/null || echo "N/A"
}

# --- Check prerequisites ---
log "Checking prerequisites..."
command -v k6 >/dev/null 2>&1 || { echo "k6 not found. Install: brew install grafana/k6/k6"; exit 1; }
curl -sf "${GATEWAY_URL}/health" >/dev/null || { echo "Gateway not reachable at ${GATEWAY_URL}"; exit 1; }
log "Gateway OK. Results → ${RESULTS_DIR}"

# --- Summary arrays ---
declare -a TIER_LABELS=()
declare -a SEED_TIMES=()
declare -a MEMORY_VALS=()
declare -a PROXY_RPS=()
declare -a PROXY_P50=()
declare -a PROXY_P75=()
declare -a PROXY_P90=()
declare -a PROXY_P95=()
declare -a PROXY_P99=()
declare -a PROXY_ERR=()

# --- Run each tier ---
for TIER in ${TIERS}; do
    separator
    log ">>> TIER: ${TIER} routes <<<"

    TIER_DIR="${RESULTS_DIR}/${TIER}"
    mkdir -p "${TIER_DIR}"

    # Cleanup any stale data
    log "Pre-cleanup..."
    bash "${SCRIPT_DIR}/cleanup.sh" "${TIER}" "${GATEWAY_URL}" "${ADMIN_KEY}" 2>/dev/null || true

    # Seed
    log "Seeding ${TIER} resources..."
    SEED_START=$(date +%s)
    bash "${SCRIPT_DIR}/seed.sh" "${TIER}" "${GATEWAY_URL}" "${ADMIN_KEY}" "${UPSTREAM_TARGET}" 2>&1 | tee "${TIER_DIR}/seed.log"
    SEED_END=$(date +%s)
    SEED_DUR=$((SEED_END - SEED_START))
    log "Seed took ${SEED_DUR}s"

    # Memory after seed
    MEM=$(collect_memory)
    log "Memory after seed: ${MEM}"

    # Warmup
    log "Warmup (5 VUs, 5s)..."
    k6 run --quiet \
        -e GATEWAY_URL="${GATEWAY_URL}" \
        -e ROUTE_COUNT="${TIER}" \
        --vus 5 --duration 5s \
        "${K6_DIR}/proxy-latency.js" 2>/dev/null || true
    sleep 2

    # Proxy latency test
    log "Running proxy-latency (${VUS} VUs, ${DURATION})..."
    k6 run \
        -e GATEWAY_URL="${GATEWAY_URL}" \
        -e ROUTE_COUNT="${TIER}" \
        --summary-export "${TIER_DIR}/proxy-latency.json" \
        "${K6_DIR}/proxy-latency.js" 2>&1 | tee "${TIER_DIR}/proxy-latency.log"

    # Route spread test
    log "Running route-spread (${VUS} VUs, ${DURATION})..."
    k6 run \
        -e GATEWAY_URL="${GATEWAY_URL}" \
        -e ROUTE_COUNT="${TIER}" \
        --summary-export "${TIER_DIR}/route-spread.json" \
        "${K6_DIR}/route-spread.js" 2>&1 | tee "${TIER_DIR}/route-spread.log"

    # Memory during test
    MEM_AFTER=$(collect_memory)

    # Cleanup
    log "Cleaning up ${TIER} resources..."
    bash "${SCRIPT_DIR}/cleanup.sh" "${TIER}" "${GATEWAY_URL}" "${ADMIN_KEY}" 2>&1 | tee "${TIER_DIR}/cleanup.log"

    # Collect summary values
    TIER_LABELS+=("${TIER}")
    SEED_TIMES+=("${SEED_DUR}")
    MEMORY_VALS+=("${MEM_AFTER}")

    # Parse k6 JSON summary
    if [[ -f "${TIER_DIR}/proxy-latency.json" ]]; then
        RPS=$(python3 -c "
import json
with open('${TIER_DIR}/proxy-latency.json') as f:
    d = json.load(f)
m = d.get('metrics',{})
rps_metric = m.get('http_reqs',{})
rps = rps_metric.get('values', rps_metric).get('rate',0)
print(f'{rps:.1f}')
" 2>/dev/null || echo "N/A")

        P50=$(python3 -c "
import json
with open('${TIER_DIR}/proxy-latency.json') as f:
    d = json.load(f)
metric = d.get('metrics',{}).get('http_req_duration',{})
v = metric.get('values', metric).get('p(50)',0)
print(f'{v:.2f}')
" 2>/dev/null || echo "N/A")

        P75=$(python3 -c "
import json
with open('${TIER_DIR}/proxy-latency.json') as f:
    d = json.load(f)
metric = d.get('metrics',{}).get('http_req_duration',{})
v = metric.get('values', metric).get('p(75)',0)
print(f'{v:.2f}')
" 2>/dev/null || echo "N/A")

        P90=$(python3 -c "
import json
with open('${TIER_DIR}/proxy-latency.json') as f:
    d = json.load(f)
metric = d.get('metrics',{}).get('http_req_duration',{})
v = metric.get('values', metric).get('p(90)',0)
print(f'{v:.2f}')
" 2>/dev/null || echo "N/A")

        P95=$(python3 -c "
import json
with open('${TIER_DIR}/proxy-latency.json') as f:
    d = json.load(f)
metric = d.get('metrics',{}).get('http_req_duration',{})
v = metric.get('values', metric).get('p(95)',0)
print(f'{v:.2f}')
" 2>/dev/null || echo "N/A")

        P99=$(python3 -c "
import json
with open('${TIER_DIR}/proxy-latency.json') as f:
    d = json.load(f)
metric = d.get('metrics',{}).get('http_req_duration',{})
v = metric.get('values', metric).get('p(99)',0)
print(f'{v:.2f}')
" 2>/dev/null || echo "N/A")

        ERR=$(python3 -c "
import json
with open('${TIER_DIR}/proxy-latency.json') as f:
    d = json.load(f)
checks = d.get('metrics',{}).get('checks',{}).get('values',{})
rate = checks.get('rate',1)
print(f'{(1-rate)*100:.2f}%')
" 2>/dev/null || echo "N/A")

        PROXY_RPS+=("${RPS}")
        PROXY_P50+=("${P50}")
        PROXY_P75+=("${P75}")
        PROXY_P90+=("${P90}")
        PROXY_P95+=("${P95}")
        PROXY_P99+=("${P99}")
        PROXY_ERR+=("${ERR}")
    else
        PROXY_RPS+=("N/A")
        PROXY_P50+=("N/A")
        PROXY_P75+=("N/A")
        PROXY_P90+=("N/A")
        PROXY_P95+=("N/A")
        PROXY_P99+=("N/A")
        PROXY_ERR+=("N/A")
    fi

    log "Tier ${TIER} complete. Cooling down 10s..."
    sleep 10
done

# --- Admin throughput test (independent of route count) ---
separator
log "Running admin-throughput test (20 VUs, 30s)..."
ADMIN_DIR="${RESULTS_DIR}/admin"
mkdir -p "${ADMIN_DIR}"
k6 run \
    -e GATEWAY_URL="${GATEWAY_URL}" \
    -e ADMIN_KEY="${ADMIN_KEY}" \
    --summary-export "${ADMIN_DIR}/admin-throughput.json" \
    "${K6_DIR}/admin-throughput.js" 2>&1 | tee "${ADMIN_DIR}/admin-throughput.log"

# --- Generate report ---
separator
log "Generating comparison report..."

REPORT="${RESULTS_DIR}/report.md"
cat > "${REPORT}" <<'HEADER'
# Lumen Gateway — Scale Stress Test Report

HEADER
echo "**Date:** $(date '+%Y-%m-%d %H:%M:%S')" >> "${REPORT}"
echo "**VUs:** ${VUS}  **Duration:** ${DURATION}" >> "${REPORT}"
echo "" >> "${REPORT}"

cat >> "${REPORT}" <<'TABLE_HEADER'
## Proxy Latency vs Route Count

| Routes | Seed Time | RPS | p50 (ms) | p75 (ms) | p90 (ms) | p95 (ms) | p99 (ms) | Error Rate | Memory |
|--------|-----------|-----|----------|----------|----------|----------|----------|------------|--------|
TABLE_HEADER

for i in "${!TIER_LABELS[@]}"; do
    printf "| %s | %ss | %s | %s | %s | %s | %s | %s | %s | %s |\n" \
        "${TIER_LABELS[$i]}" \
        "${SEED_TIMES[$i]}" \
        "${PROXY_RPS[$i]}" \
        "${PROXY_P50[$i]}" \
        "${PROXY_P75[$i]}" \
        "${PROXY_P90[$i]}" \
        "${PROXY_P95[$i]}" \
        "${PROXY_P99[$i]}" \
        "${PROXY_ERR[$i]}" \
        "${MEMORY_VALS[$i]}" >> "${REPORT}"
done

# Admin throughput summary
if [[ -f "${ADMIN_DIR}/admin-throughput.json" ]]; then
    ADMIN_RPS=$(python3 -c "
import json
with open('${ADMIN_DIR}/admin-throughput.json') as f:
    d = json.load(f)
rps = d.get('metrics',{}).get('http_reqs',{}).get('values',{}).get('rate',0)
print(f'{rps:.1f}')
" 2>/dev/null || echo "N/A")

    ADMIN_P50=$(python3 -c "
import json
with open('${ADMIN_DIR}/admin-throughput.json') as f:
    d = json.load(f)
v = d.get('metrics',{}).get('admin_create_latency',{}).get('values',{}).get('p(50)',0)
print(f'{v:.2f}')
" 2>/dev/null || echo "N/A")

    ADMIN_P99=$(python3 -c "
import json
with open('${ADMIN_DIR}/admin-throughput.json') as f:
    d = json.load(f)
v = d.get('metrics',{}).get('admin_create_latency',{}).get('values',{}).get('p(99)',0)
print(f'{v:.2f}')
" 2>/dev/null || echo "N/A")

    cat >> "${REPORT}" <<EOF

## Admin API Throughput

| Metric | Value |
|--------|-------|
| RPS (total HTTP) | ${ADMIN_RPS} |
| Create p50 | ${ADMIN_P50} ms |
| Create p99 | ${ADMIN_P99} ms |
EOF
fi

echo "" >> "${REPORT}"
echo "---" >> "${REPORT}"
echo "*Raw data: \`${RESULTS_DIR}/\`*" >> "${REPORT}"

log "Report saved to ${REPORT}"
cat "${REPORT}"
