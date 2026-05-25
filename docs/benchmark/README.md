# Benchmark: lumen-gateway vs Apache APISIX 3.14.1

> **Round 3** — 2026-05-21 · Apple M3 Max · Docker 2 CPUs / 512 MB per gateway

## Overview

**lumen-gateway** is built on [Hertz](https://github.com/cloudwego/hertz) + [Netpoll](https://github.com/cloudwego/netpoll) (ByteDance), using Go's goroutine scheduler and epoll/kqueue non-blocking I/O.

**Apache APISIX** is a cloud-native API gateway built on OpenResty (Nginx + LuaJIT), widely used in production.

---

## Test Environment

| Component | Details |
|-----------|---------|
| Host OS | macOS Darwin 25.4.0, arm64 |
| CPU | Apple M3 Max (16 cores) |
| Memory | 48 GB |
| lumen-gateway | Go 1.25 + Hertz + Netpoll (Docker) |
| APISIX | 3.14.1-debian (OpenResty/LuaJIT, Docker) |
| Load tool | k6 v2.0.0 (darwin/arm64) |
| Backend | mock-server — Go net/http, echoes request as JSON |
| Network | Docker bridge (`bench-net`), all services co-located |
| Test date | 2026-05-21 |

---

## Methodology

### Fairness Principles

| Dimension | How parity is achieved |
|-----------|----------------------|
| **Resources** | Both gateways: 2 CPUs / 512 MB Docker limit. One gateway runs at a time — the other container is stopped |
| **Backend** | Single Go mock-server (2 CPUs / 512 MB) shared, always running |
| **Plugin stacks** | Passthrough: no plugins on either side. Pipeline: `request_id` + `access_log` on both sides |
| **access_log config** | Passthrough: both disabled (`access_log off`). Pipeline: Lumen `buffer_size=16384` `flush_interval=1s`; APISIX nginx `access_log off; access_log bench-access.log buffer=16384 flush=1s` |
| **Concurrency** | 300 VUs constant (passthrough/pipeline), 0→500 VU ramp (ramp-up), 50→500→50 VU spike |
| **Warmup** | 5 VUs × 15s before each scenario group to prime connection pools and JIT caches |
| **Cooldown** | 30s between scenarios; 60s between gateways (TCP TIME_WAIT drain) |

### Scenarios

| Scenario | Load | Route | Plugin stack |
|----------|------|-------|-------------|
| **passthrough** | 300 VUs constant · 60 s | `/benchmark/echo` | None (pure proxy) |
| **pipeline** | 300 VUs constant · 60 s | `/benchmark/pipeline` | `request_id` + buffered `access_log` |
| **ramp-up** | 0→100→300→500 VUs · 150 s total | `/benchmark/pipeline` | Same as pipeline |
| **spike** | 50 VUs baseline → 500 VUs (10 s) → sustain → 50 VUs recovery | `/benchmark/pipeline` | Same as pipeline |

### Config Parity

```
Lumen (single binary, same config for all scenarios):
  /benchmark/echo      — no plugins (pure proxy)
  /benchmark/pipeline  — request_id (uuid, include_in_response=true)
                       + access_log (buffer=16384, flush=1s)

APISIX (restarted between passthrough and pipeline groups):
  config-passthrough.yaml:
    http_server_configuration_snippet: access_log off;
    /benchmark/echo — no plugins

  config-pipeline.yaml:
    http_server_configuration_snippet:
      access_log off;  (disables default log)
      access_log bench-access.log combined buffer=16384 flush=1s;
    /benchmark/pipeline — request-id plugin (include_in_response=true)
```

### Network Topology

```
┌─ Docker bench-net ────────────────────────────────────────┐
│                                                            │
│  mock-server:9001 ←── lumen:18080  (exposed :18080)       │
│                   ←── apisix:9080  (exposed :9080)        │
│                                                            │
│  apisix-etcd:2379 ←── apisix (0.5 CPU / 256 MB)          │
│                                                            │
└────────────────────────────────────────────────────────────┘
         ↑                         ↑
    k6 (host)                 k6 (host)
    localhost:18080           localhost:9080
```

---

## Results

### Scenario 1 — Passthrough: Pure Proxy (300 VUs × 60 s)

*No plugins on either gateway. Measures raw proxy overhead.*

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|:-------------:|:------:|:------------:|
| **RPS** | 21,335 | **25,228** | 0.85× |
| avg latency | 14.01 ms | 11.81 ms | 1.19× |
| p50 | 13.31 ms | **6.39 ms** | 2.08× |
| p75 | 17.11 ms | 9.69 ms | 1.77× |
| p90 | **21.80 ms** | 41.70 ms | **0.52×** |
| p95 | **25.27 ms** | 50.06 ms | **0.50×** |
| p99 | **33.37 ms** | 61.06 ms | **0.55×** |
| max | 125.10 ms | 114.15 ms | 1.10× |
| error rate | 0.00% | 0.00% | — |
| total requests | 1,280,313 | 1,514,673 | — |

### Scenario 2 — Pipeline: request_id + access_log (300 VUs × 60 s)

*Both gateways run request_id and buffered access_log. Measures plugin overhead on top of proxy.*

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|:-------------:|:------:|:------------:|
| **RPS** | 18,849 | **21,957** | 0.86× |
| avg latency | 15.86 ms | 13.57 ms | 1.17× |
| p50 | 15.02 ms | **6.81 ms** | 2.21× |
| p75 | 19.53 ms | 10.04 ms | 1.94× |
| p90 | **24.99 ms** | 51.82 ms | **0.48×** |
| p95 | **28.99 ms** | 58.41 ms | **0.50×** |
| p99 | **38.52 ms** | 66.02 ms | **0.58×** |
| max | 85.79 ms | 97.70 ms | 0.88× |
| error rate | 0.00% | 0.00% | — |
| Plugin overhead vs passthrough | −12% RPS | −13% RPS | — |

### Scenario 3 — Ramp-up: Gradual Saturation (0→500 VUs, pipeline route, 150 s)

*5 stages: 0→100→300→500 VUs (30s each), sustain 500, ramp down.*

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|:-------------:|:------:|:------------:|
| **RPS (avg over full ramp)** | 17,748 | **21,103** | 0.84× |
| avg latency | 15.73 ms | 13.14 ms | 1.20× |
| p50 | 14.95 ms | **6.93 ms** | 2.16× |
| p75 | 22.79 ms | 11.28 ms | 2.02× |
| p90 | **29.70 ms** | 51.45 ms | **0.58×** |
| p95 | **34.97 ms** | 58.33 ms | **0.60×** |
| p99 | **46.14 ms** | 66.72 ms | **0.69×** |
| max | 119.49 ms | 99.05 ms | 1.21× |
| error rate | 0.00% | 0.00% | — |
| total requests | 2,662,122 | 3,165,392 | — |

### Scenario 4 — Spike: Instant Traffic Spike (50→500→50 VUs, pipeline route, 110 s)

*Baseline 50 VU → instant spike to 500 VU (10 s) → sustain 30 s → instant drop → recovery.*

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|:-------------:|:------:|:------------:|
| **RPS (avg over full spike)** | 16,235 | **19,482** | 0.83× |
| avg latency | 13.12 ms | 10.87 ms | 1.21× |
| p50 | 7.41 ms | **4.76 ms** | 1.56× |
| p75 | 22.10 ms | 10.32 ms | 2.14× |
| p90 | **30.14 ms** | 38.46 ms | **0.78×** |
| p95 | **36.11 ms** | 57.24 ms | **0.63×** |
| p99 | **49.02 ms** | 67.95 ms | **0.72×** |
| max | 119.87 ms | 123.32 ms | 0.97× |
| error rate | 0.00% | 0.00% | — |

---

## Summary

| Dimension | lumen-gateway | APISIX | Winner |
|-----------|:-------------:|:------:|:------:|
| Throughput (all scenarios) | 16K–21K RPS | **20K–25K RPS** | APISIX **+16–20%** |
| Median latency (p50) | 7–15 ms | **5–7 ms** | APISIX **2–3× lower** |
| p90 latency (all scenarios) | **22–30 ms** | 38–52 ms | lumen **42–52% lower** |
| p95 latency (all scenarios) | **25–36 ms** | 50–58 ms | lumen **38–50% lower** |
| p99 latency (all scenarios) | **33–49 ms** | 61–68 ms | lumen **28–45% lower** |
| Error rate | 0% | 0% | Tie |
| Latency distribution | Uniform (unimodal) | Bimodal | lumen |

---

## Analysis

### Why APISIX Has Higher Throughput and Lower Median Latency

APISIX uses **nginx worker processes** with LuaJIT coroutines. Each worker handles one request at a time cooperatively. When all workers are idle, a new request gets picked up immediately — explaining the sub-7ms p50. Nginx's battle-hardened C I/O path processes simple requests with extremely low overhead.

Lumen uses **Go goroutines** scheduled across GOMAXPROCS threads (= 2 in the container). Go's scheduler has higher per-request baseline cost than Nginx's C event loop for simple requests, explaining the higher p50 (~14ms vs ~7ms).

### Why Lumen Has Much Better Tail Latency (p90–p99)

At 300 VUs sustained load, APISIX exhibits a **bimodal latency distribution**: requests either complete in ~6ms (fast path, free worker available) or wait in queue (~50–65ms, all workers busy). This pattern is inherent to the nginx worker process model: with 2 CPUs, there are typically 2 workers, and when both are processing requests, additional requests must wait.

The p90 jumping from ~7ms (p50) to ~42–52ms (p90) is the "queue wait" signal — roughly 10% of requests are hitting a full worker pool.

Lumen's goroutine scheduler distributes 300 VUs across 2 OS threads cooperatively without a fixed worker count, so the **latency distribution is uniform** — no queuing cliff. The p50→p90 progression is smooth: 13ms → 22ms (passthrough) instead of APISIX's 6ms → 42ms.

### Spike Scenario: Recovery Behavior

Both gateways handled the 50→500 VU instant spike with **0% errors**, which is the primary resilience metric. Lumen's spike p99 (49ms) vs APISIX's spike p99 (68ms) reflects the same bimodal pattern amplified at higher concurrency. After the spike drops back to 50 VUs, both recover immediately (tail latency improvement visible in the p50 = 7ms for lumen, 4.8ms for APISIX).

### Plugin Overhead

Adding `request_id` + `access_log` (passthrough → pipeline):
- APISIX: 25,228 → 21,957 RPS = **−13.0%**
- Lumen: 21,335 → 18,849 RPS = **−11.6%**

Plugin overhead is similar on both gateways. Lumen's plugin chain (Go function calls) and APISIX's LuaJIT plugin execution have comparable relative cost at this concurrency level.

### When to Choose Each

| Choose **lumen-gateway** | Choose **APISIX** |
|--------------------------|-------------------|
| Strict P90/P99 SLOs under sustained load | Maximum raw throughput |
| Predictable latency under traffic spikes | Low single-request latency (low concurrency) |
| Go-first team, single language stack | Need 100+ plugins, Lua scripting |
| Single-binary deploy, no runtime deps | Already invested in OpenResty ecosystem |

---

## How to Reproduce

```bash
# 1. Install k6
brew install k6

# 2. Build and start all benchmark services
cd lumen-gateway
bash docs/benchmark/scripts/setup.sh

# 3. Run all four scenarios (one gateway at a time, ~25 min total)
bash docs/benchmark/scripts/run.sh

# 4. Results
ls docs/benchmark/results/*/
cat docs/benchmark/results/system-info.txt

# 5. Tear down
docker compose -f docs/benchmark/docker-compose.yml down -v
```

## Raw Data Layout

```
results/
├── system-info.txt                  host specs and methodology
├── lumen/
│   ├── passthrough_summary.json     k6 JSON summary export
│   ├── passthrough_resources.csv    CPU/memory timeseries (docker stats)
│   ├── pipeline_summary.json
│   ├── pipeline_resources.csv
│   ├── ramp-up_summary.json
│   ├── ramp-up_resources.csv
│   ├── spike_summary.json
│   └── spike_resources.csv
└── apisix/
    └── (same structure)
```
