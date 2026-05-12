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

SCENARIOS=("passthrough" "ramp-up")
GATEWAYS=("lumen" "apisix")

LUMEN_CONTAINER="bench-lumen"
APISIX_CONTAINER="bench-apisix"

# ── Preflight checks ────────────────────────────────────────────────────────

command -v k6 >/dev/null 2>&1 || { echo "k6 not found. Run: brew install k6"; exit 1; }

for gw in "${GATEWAYS[@]}"; do
    if [ "$gw" = "lumen" ]; then
        url="http://localhost:18080/benchmark/echo"
    else
        url="http://localhost:9080/benchmark/echo"
    fi
    if ! curl -sf "$url" >/dev/null 2>&1; then
        echo "WARNING: $gw not reachable at $url — run setup.sh first"
    fi
done

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
    echo "lumen_image: $(docker inspect $LUMEN_CONTAINER --format '{{.Config.Image}}' 2>/dev/null || echo unknown)"
    echo "apisix_image: $(docker inspect $APISIX_CONTAINER --format '{{.Config.Image}}' 2>/dev/null || echo unknown)"
} > "$RESULTS_DIR/system-info.txt"

info "System info saved to results/system-info.txt"

# ── Warmup ───────────────────────────────────────────────────────────────────

for gw in "${GATEWAYS[@]}"; do
    if [ "$gw" = "lumen" ]; then
        url="http://localhost:18080/benchmark/echo"
    else
        url="http://localhost:9080/benchmark/echo"
    fi
    step "Warming up $gw..."
    k6 run --quiet --duration 3s --vus 5 - <<WARMUP 2>/dev/null || true
import http from 'k6/http';
export default function() { http.get('${url}'); }
WARMUP
    sleep 2
done

# ── Run benchmarks ───────────────────────────────────────────────────────────

for scenario in "${SCENARIOS[@]}"; do
    for gw in "${GATEWAYS[@]}"; do
        echo ""
        step "Running [$scenario] against [$gw]..."

        bash "$SCRIPT_DIR/collect-resources.sh" "$gw" \
            "$RESULTS_DIR/$gw/${scenario}_resources.csv" &
        RESOURCE_PID=$!

        GATEWAY=$gw k6 run \
            --summary-export="$RESULTS_DIR/$gw/${scenario}_summary.json" \
            "$K6_DIR/${scenario}.js" 2>&1 | tee "$RESULTS_DIR/$gw/${scenario}_output.txt"

        kill $RESOURCE_PID 2>/dev/null || true
        wait $RESOURCE_PID 2>/dev/null || true

        info "Results saved to results/$gw/${scenario}_summary.json"
        info "Waiting 30s for TCP TIME_WAIT cleanup..."
        sleep 30
    done
done

# ── Generate comparison ─────────────────────────────────────────────────────

echo ""
info "=========================================="
info "  Benchmark Complete!"
info "=========================================="
echo ""
info "Results directory: $RESULTS_DIR"
info "System info:       $RESULTS_DIR/system-info.txt"
echo ""

for scenario in "${SCENARIOS[@]}"; do
    echo -e "${CYAN}=== $scenario ===${NC}"
    for gw in "${GATEWAYS[@]}"; do
        summary="$RESULTS_DIR/$gw/${scenario}_summary.json"
        if [ -f "$summary" ]; then
            echo -e "${YELLOW}--- $gw ---${NC}"
            if command -v python3 >/dev/null 2>&1; then
                python3 -c "
import json, sys
with open('$summary') as f:
    d = json.load(f)
m = d.get('metrics', {})
dur = m.get('http_req_duration', {}).get('values', {})
reqs = m.get('http_reqs', {}).get('values', {})
fails = m.get('http_req_failed', {}).get('values', {})
print(f\"  RPS:       {reqs.get('rate', 'N/A'):.1f}\")
print(f\"  p50:       {dur.get('p(50)', 'N/A'):.2f} ms\")
print(f\"  p95:       {dur.get('p(95)', 'N/A'):.2f} ms\")
print(f\"  p99:       {dur.get('p(99)', 'N/A'):.2f} ms\")
print(f\"  avg:       {dur.get('avg', 'N/A'):.2f} ms\")
print(f\"  max:       {dur.get('max', 'N/A'):.2f} ms\")
print(f\"  err rate:  {fails.get('rate', 0)*100:.2f}%\")
" 2>/dev/null || echo "  (install python3 or check $summary manually)"
            fi
        fi
    done
    echo ""
done

info "To view full results: ls $RESULTS_DIR/*/"
info "To update report:     edit $BENCH_DIR/README.md with the numbers above"
info "To tear down:         docker compose -f $BENCH_DIR/docker-compose.yml down -v"
