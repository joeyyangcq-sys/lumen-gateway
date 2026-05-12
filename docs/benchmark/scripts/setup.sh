#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BENCH_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail()  { echo -e "${RED}[FAIL]${NC} $*"; exit 1; }

APISIX_ADMIN="http://localhost:9180"
APISIX_KEY="benchmark-admin-key"

# ── Step 1: Check prerequisites ──────────────────────────────────────────────

info "Checking prerequisites..."
command -v k6 >/dev/null 2>&1 || fail "k6 not found. Install with: brew install k6"
command -v docker >/dev/null 2>&1 || fail "docker not found"
command -v curl >/dev/null 2>&1 || fail "curl not found"

# ── Step 2: Start all services via Docker Compose ────────────────────────────

info "Starting benchmark services via Docker Compose..."
docker compose -f "$BENCH_DIR/docker-compose.yml" up -d --build

info "Waiting for services to become healthy..."

wait_for() {
    local name="$1" url="$2" retries=30
    for i in $(seq 1 $retries); do
        if curl -sf "$url" >/dev/null 2>&1; then
            info "$name is ready"
            return 0
        fi
        sleep 2
    done
    fail "$name failed to start after ${retries} retries"
}

wait_for "lumen-gateway" "http://localhost:18080/health"
wait_for_apisix() {
    local retries=30
    for i in $(seq 1 $retries); do
        if curl -sf "$APISIX_ADMIN/apisix/admin/routes" -H "X-API-KEY: $APISIX_KEY" >/dev/null 2>&1; then
            info "APISIX Admin is ready"
            return 0
        fi
        sleep 2
    done
    fail "APISIX Admin failed to start after ${retries} retries"
}
wait_for_apisix

# ── Step 3: Configure APISIX routes ─────────────────────────────────────────

info "Configuring APISIX upstream (mock-server:9001)..."
curl -sf -X PUT "$APISIX_ADMIN/apisix/admin/upstreams/bench-upstream" \
    -H "X-API-KEY: $APISIX_KEY" \
    -H "Content-Type: application/json" \
    -d '{
        "type": "roundrobin",
        "nodes": {"mock-server:9001": 1},
        "timeout": {"connect": 3, "send": 5, "read": 5}
    }' >/dev/null

info "Configuring APISIX route: /benchmark/echo..."
curl -sf -X PUT "$APISIX_ADMIN/apisix/admin/routes/bench-echo" \
    -H "X-API-KEY: $APISIX_KEY" \
    -H "Content-Type: application/json" \
    -d '{
        "uri": "/benchmark/echo",
        "methods": ["GET", "POST"],
        "upstream_id": "bench-upstream"
    }' >/dev/null

info "APISIX routes configured"

# ── Step 4: Verify both gateways ────────────────────────────────────────────

sleep 2

if curl -sf http://localhost:18080/benchmark/echo >/dev/null 2>&1; then
    info "lumen-gateway proxy verified: /benchmark/echo -> mock-server"
else
    fail "lumen-gateway proxy verification failed"
fi

if curl -sf http://localhost:9080/benchmark/echo >/dev/null 2>&1; then
    info "APISIX proxy verified: /benchmark/echo -> mock-server"
else
    fail "APISIX proxy verification failed"
fi

# ── Done ─────────────────────────────────────────────────────────────────────

echo ""
info "Setup complete! All services running in Docker."
info "  mock-server:    bench-mock-server (Docker internal)"
info "  lumen-gateway:  http://localhost:18080/benchmark/echo"
info "  APISIX:         http://localhost:9080/benchmark/echo"
echo ""
info "Run benchmarks with: bash $SCRIPT_DIR/run.sh"
info "Tear down with: docker compose -f $BENCH_DIR/docker-compose.yml down -v"
