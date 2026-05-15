#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BENCH_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
K6_DIR="$BENCH_DIR/k6"
RESULTS_DIR="$BENCH_DIR/results"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
step()  { echo -e "${CYAN}[STEP]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }

SCENARIOS=("passthrough" "ramp-up")
GATEWAYS=("lumen" "apisix")

LUMEN_CONTAINER="bench-lumen"
APISIX_CONTAINER="bench-apisix"

COMPOSE="docker compose -f $BENCH_DIR/docker-compose.yml"

# ── Preflight checks ────────────────────────────────────────────────────────

command -v k6 >/dev/null 2>&1 || { echo "k6 not found. Run: brew install k6"; exit 1; }

# ── Helpers ──────────────────────────────────────────────────────────────────

gateway_url() {
    if [ "$1" = "lumen" ]; then echo "http://localhost:18080"; else echo "http://localhost:9080"; fi
}

gateway_container() {
    if [ "$1" = "lumen" ]; then echo "$LUMEN_CONTAINER"; else echo "$APISIX_CONTAINER"; fi
}

# Start only the target gateway; stop the other to prevent resource contention.
# Both share the same cpu/memory limits in docker-compose (2 CPUs / 512 MB),
# so whichever gateway is stopped frees its quota to the OS.
start_only() {
    local gw="$1"
    local other
    if [ "$gw" = "lumen" ]; then other="apisix"; else other="lumen"; fi

    step "Stopping $other to free resources..."
    $COMPOSE stop "$other" 2>/dev/null || true

    step "Starting $gw..."
    $COMPOSE start "$gw"

    local url
    url="$(gateway_url "$gw")/benchmark/echo"
    local retries=20
    for i in $(seq 1 $retries); do
        if curl -sf "$url" >/dev/null 2>&1; then
            info "$gw is ready at $url"
            return 0
        fi
        sleep 2
    done
    echo "ERROR: $gw did not become healthy after ${retries} retries" >&2
    exit 1
}

# ── Clean previous results ───────────────────────────────────────────────────

rm -rf "$RESULTS_DIR"
mkdir -p "$RESULTS_DIR/lumen" "$RESULTS_DIR/apisix"

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
info "Benchmark started at $TIMESTAMP"
echo "$TIMESTAMP" > "$RESULTS_DIR/timestamp.txt"

# ── Collect system info ──────────────────────────────────────────────────────

{
    echo "date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "os: $(uname -srm)"
    echo "cpu: $(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo unknown)"
    echo "cores: $(sysctl -n hw.ncpu 2>/dev/null || echo unknown)"
    echo "memory: $(sysctl -n hw.memsize 2>/dev/null | awk '{printf "%.0f GB", $1/1073741824}' || echo unknown)"
    echo "k6: $(k6 version 2>/dev/null || echo unknown)"
    echo "docker: $(docker --version 2>/dev/null || echo unknown)"
    echo "gateway_limit_cpus: 2.0"
    echo "gateway_limit_memory: 512M"
    echo "mock_server_limit_cpus: 2.0"
    echo "mock_server_limit_memory: 512M"
    echo "lumen_image: $(docker inspect $LUMEN_CONTAINER --format '{{.Config.Image}}' 2>/dev/null || echo unknown)"
    echo "apisix_image: $(docker inspect $APISIX_CONTAINER --format '{{.Config.Image}}' 2>/dev/null || echo unknown)"
    echo "methodology: one_gateway_at_a_time"
} > "$RESULTS_DIR/system-info.txt"

info "System info saved to results/system-info.txt"

# ── Run benchmarks — one gateway at a time ───────────────────────────────────
#
# Each gateway gets exclusive use of its Docker resource quota (2 CPUs, 512 MB)
# while the other container is stopped.  All scenarios for a gateway are run
# back-to-back before switching, so TCP state and OS page-cache stay warm.

for gw in "${GATEWAYS[@]}"; do
    echo ""
    step "========================================"
    step "  Gateway: $gw"
    step "========================================"

    # Bring up only this gateway
    start_only "$gw"

    # Warmup — prime connection pools and JIT (LuaJIT for APISIX)
    step "Warming up $gw (5 VUs × 5s)..."
    k6 run --quiet --duration 5s --vus 5 - <<WARMUP 2>/dev/null || true
import http from 'k6/http';
export default function() { http.post('$(gateway_url "$gw")/benchmark/echo', JSON.stringify({warmup:true}), {headers:{'Content-Type':'application/json'}}); }
WARMUP
    sleep 3

    # Run all scenarios for this gateway
    for scenario in "${SCENARIOS[@]}"; do
        echo ""
        step "Running [$scenario] on [$gw]..."

        bash "$SCRIPT_DIR/collect-resources.sh" "$gw" \
            "$RESULTS_DIR/$gw/${scenario}_resources.csv" &
        RESOURCE_PID=$!

        GATEWAY=$gw k6 run \
            --summary-export="$RESULTS_DIR/$gw/${scenario}_summary.json" \
            "$K6_DIR/${scenario}.js" 2>&1 | tee "$RESULTS_DIR/$gw/${scenario}_output.txt"

        kill $RESOURCE_PID 2>/dev/null || true
        wait $RESOURCE_PID 2>/dev/null || true

        info "Saved → results/$gw/${scenario}_summary.json"

        if [ "$scenario" != "${SCENARIOS[${#SCENARIOS[@]}-1]}" ]; then
            info "Cooling down 30s before next scenario..."
            sleep 30
        fi
    done

    # Stop this gateway before switching
    step "Stopping $gw..."
    $COMPOSE stop "$gw" 2>/dev/null || true

    if [ "$gw" != "${GATEWAYS[${#GATEWAYS[@]}-1]}" ]; then
        info "Cooling down 30s (TCP TIME_WAIT) before next gateway..."
        sleep 30
    fi
done

# ── Print comparison ─────────────────────────────────────────────────────────

echo ""
info "=========================================="
info "  Benchmark Complete!"
info "=========================================="
echo ""

for scenario in "${SCENARIOS[@]}"; do
    echo -e "${CYAN}=== $scenario ===${NC}"
    for gw in "${GATEWAYS[@]}"; do
        summary="$RESULTS_DIR/$gw/${scenario}_summary.json"
        if [ -f "$summary" ]; then
            echo -e "${YELLOW}--- $gw ---${NC}"
            if command -v python3 >/dev/null 2>&1; then
                python3 -c "
import json
with open('$summary') as f:
    d = json.load(f)
m = d.get('metrics', {})
dur = m.get('http_req_duration', {})
reqs = m.get('http_reqs', {})
fails = m.get('http_req_failed', {})
print(f\"  RPS:       {reqs.get('rate', 0):.1f}\")
print(f\"  avg:       {dur.get('avg', 0):.2f} ms\")
print(f\"  p50:       {dur.get('p(50)', 0):.2f} ms\")
print(f\"  p95:       {dur.get('p(95)', 0):.2f} ms\")
print(f\"  p99:       {dur.get('p(99)', 0):.2f} ms\")
print(f\"  max:       {dur.get('max', 0):.2f} ms\")
print(f\"  err rate:  {fails.get('rate', 0)*100:.2f}%\")
" 2>/dev/null || echo "  (check $summary manually)"
            fi
        fi
    done
    echo ""
done

info "Results: $RESULTS_DIR"
info "Tear down: docker compose -f $BENCH_DIR/docker-compose.yml down -v"
