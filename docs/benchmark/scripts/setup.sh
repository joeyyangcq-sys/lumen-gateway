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

APISIX_PROXY_URL="${APISIX_PROXY_URL:-http://localhost:${APISIX_PROXY_PORT:-9080}}"
APISIX_ADMIN="${APISIX_ADMIN:-http://localhost:${APISIX_ADMIN_PORT:-9180}}"
APISIX_KEY="benchmark-admin-key"

# ── Step 1: Check prerequisites ──────────────────────────────────────────────

info "Checking prerequisites..."
command -v k6     >/dev/null 2>&1 || fail "k6 not found. Install with: brew install k6"
command -v docker >/dev/null 2>&1 || fail "docker not found"
command -v curl   >/dev/null 2>&1 || fail "curl not found"

# ── Step 2: Start services (APISIX starts with passthrough config) ───────────

info "Starting benchmark services via Docker Compose..."
# Default APISIX_CONFIG is config-passthrough.yaml (access_log off).
# run.sh will restart APISIX with config-pipeline.yaml for pipeline scenarios.
APISIX_CONFIG="$BENCH_DIR/apisix/config-passthrough.yaml" \
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

# ── Step 3: Configure APISIX resources ──────────────────────────────────────

info "Configuring APISIX upstream (mock-server:9001)..."
curl -sf -X PUT "$APISIX_ADMIN/apisix/admin/upstreams/bench-upstream" \
    -H "X-API-KEY: $APISIX_KEY" \
    -H "Content-Type: application/json" \
    -d '{
        "type": "roundrobin",
        "nodes": {"mock-server:9001": 1},
        "timeout": {"connect": 3, "send": 5, "read": 5}
    }' >/dev/null

# Passthrough route — pure proxy, no plugins.
# Mirrors Lumen bench-echo route (no plugin overhead on either side).
info "Configuring APISIX route: /benchmark/echo (no plugins)..."
curl -sf -X PUT "$APISIX_ADMIN/apisix/admin/routes/bench-echo" \
    -H "X-API-KEY: $APISIX_KEY" \
    -H "Content-Type: application/json" \
    -d '{
        "uri": "/benchmark/echo",
        "methods": ["GET", "POST"],
        "upstream_id": "bench-upstream"
    }' >/dev/null

# Pipeline route — request-id plugin.
# The nginx access_log is configured at the server level via config-pipeline.yaml
# (buffer=16384, flush=1s) to match Lumen bench-pipeline access_log plugin.
info "Configuring APISIX route: /benchmark/pipeline (request-id plugin)..."
curl -sf -X PUT "$APISIX_ADMIN/apisix/admin/routes/bench-pipeline" \
    -H "X-API-KEY: $APISIX_KEY" \
    -H "Content-Type: application/json" \
    -d '{
        "uri": "/benchmark/pipeline",
        "methods": ["GET", "POST"],
        "upstream_id": "bench-upstream",
        "plugins": {
            "request-id": {
                "include_in_response": true
            }
        }
    }' >/dev/null

info "APISIX resources configured"

# ── Step 4: Verify both gateways ────────────────────────────────────────────

sleep 2

if curl -sf http://localhost:18080/benchmark/echo >/dev/null 2>&1; then
    info "lumen-gateway proxy verified: /benchmark/echo -> mock-server"
else
    fail "lumen-gateway /benchmark/echo verification failed"
fi

if curl -sf http://localhost:18080/benchmark/pipeline >/dev/null 2>&1; then
    info "lumen-gateway proxy verified: /benchmark/pipeline -> mock-server"
else
    fail "lumen-gateway /benchmark/pipeline verification failed"
fi

if curl -sf "$APISIX_PROXY_URL/benchmark/echo" >/dev/null 2>&1; then
    info "APISIX proxy verified: /benchmark/echo -> mock-server"
else
    fail "APISIX /benchmark/echo verification failed"
fi

if curl -sf "$APISIX_PROXY_URL/benchmark/pipeline" >/dev/null 2>&1; then
    info "APISIX proxy verified: /benchmark/pipeline -> mock-server"
else
    fail "APISIX /benchmark/pipeline verification failed"
fi

# ── Done ─────────────────────────────────────────────────────────────────────

echo ""
info "Setup complete! All services running in Docker."
info "  mock-server:         bench-mock-server (Docker internal)"
info "  lumen-gateway:"
info "    passthrough route: http://localhost:18080/benchmark/echo"
info "    pipeline route:    http://localhost:18080/benchmark/pipeline"
info "  APISIX:"
info "    passthrough route: $APISIX_PROXY_URL/benchmark/echo"
info "    pipeline route:    $APISIX_PROXY_URL/benchmark/pipeline"
echo ""
info "Run benchmarks with: bash $SCRIPT_DIR/run.sh"
info "Tear down with: docker compose -f $BENCH_DIR/docker-compose.yml down -v"
