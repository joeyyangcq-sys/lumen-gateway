#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../../.." && pwd)"

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail()  { echo -e "${RED}[FAIL]${NC} $*"; exit 1; }

APISIX_ADMIN="http://localhost:9180"
APISIX_KEY="local-dev-admin-key"

# ── Step 1: Check prerequisites ──────────────────────────────────────────────

info "Checking prerequisites..."

command -v k6 >/dev/null 2>&1 || fail "k6 not found. Install with: brew install k6"
command -v curl >/dev/null 2>&1 || fail "curl not found"

# ── Step 2: Start mock server ────────────────────────────────────────────────

if curl -sf http://localhost:9001/ >/dev/null 2>&1; then
    info "Mock server already running on :9001"
else
    info "Starting mock server..."
    cd "$ROOT_DIR/mock-server"
    go run main.go &
    MOCK_PID=$!
    echo "$MOCK_PID" > /tmp/benchmark-mock-server.pid
    sleep 2
    if ! curl -sf http://localhost:9001/ >/dev/null 2>&1; then
        fail "Mock server failed to start"
    fi
    info "Mock server started (PID: $MOCK_PID)"
fi

# ── Step 3: Configure APISIX routes ─────────────────────────────────────────

info "Checking APISIX availability..."
if ! curl -sf "$APISIX_ADMIN/apisix/admin/routes" -H "X-API-KEY: $APISIX_KEY" >/dev/null 2>&1; then
    warn "APISIX not reachable at $APISIX_ADMIN"
    warn "Start it with: cd $ROOT_DIR/apisix-local && docker compose up -d"
    warn "Skipping APISIX route setup. You can re-run setup.sh after starting APISIX."
else
    info "Configuring APISIX upstream..."
    curl -sf -X PUT "$APISIX_ADMIN/apisix/admin/upstreams/bench-upstream" \
        -H "X-API-KEY: $APISIX_KEY" \
        -H "Content-Type: application/json" \
        -d '{
            "type": "roundrobin",
            "nodes": {"host.docker.internal:9001": 1},
            "timeout": {"connect": 3, "send": 5, "read": 5}
        }' >/dev/null

    info "Configuring APISIX route: /benchmark/echo (passthrough)..."
    curl -sf -X PUT "$APISIX_ADMIN/apisix/admin/routes/bench-echo" \
        -H "X-API-KEY: $APISIX_KEY" \
        -H "Content-Type: application/json" \
        -d '{
            "uri": "/benchmark/echo",
            "methods": ["GET", "POST"],
            "upstream_id": "bench-upstream"
        }' >/dev/null

    info "APISIX routes configured successfully"

    if curl -sf http://localhost:9080/benchmark/echo >/dev/null 2>&1; then
        info "APISIX proxy verified: /benchmark/echo -> mock-server"
    else
        warn "APISIX proxy verification failed (may need a moment to sync)"
    fi
fi

# ── Step 4: Start lumen-gateway ──────────────────────────────────────────────

if curl -sf http://localhost:18080/health >/dev/null 2>&1; then
    info "lumen-gateway already running on :18080"
else
    info "Starting lumen-gateway with benchmark config..."
    cd "$ROOT_DIR/lumen-gateway"
    go run ./cmd/lumen-gateway --config configs/benchmark-bootstrap.yaml &
    LUMEN_PID=$!
    echo "$LUMEN_PID" > /tmp/benchmark-lumen-gateway.pid
    sleep 3
    if ! curl -sf http://localhost:18080/health >/dev/null 2>&1; then
        fail "lumen-gateway failed to start"
    fi
    info "lumen-gateway started (PID: $LUMEN_PID)"
fi

if curl -sf http://localhost:18080/benchmark/echo >/dev/null 2>&1; then
    info "lumen-gateway proxy verified: /benchmark/echo -> mock-server"
else
    warn "lumen-gateway proxy verification failed"
fi

# ── Done ─────────────────────────────────────────────────────────────────────

echo ""
info "Setup complete!"
info "  Mock server:    http://localhost:9001"
info "  lumen-gateway:  http://localhost:18080/benchmark/echo"
info "  APISIX:         http://localhost:9080/benchmark/echo"
echo ""
info "Run benchmarks with: bash $SCRIPT_DIR/run.sh"
