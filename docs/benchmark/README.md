# Benchmark: lumen-gateway vs Apache APISIX 3.14.1

> **Round 4** — 2026-05-25 · Apple M3 Max · Docker 2 CPUs / 512 MB per gateway

## Overview

**lumen-gateway** is built on [Hertz](https://github.com/cloudwego/hertz) + [Netpoll](https://github.com/cloudwego/netpoll) (ByteDance), using Go's goroutine scheduler and epoll/kqueue non-blocking I/O.

**Apache APISIX** is a cloud-native API gateway built on OpenResty (Nginx + LuaJIT), widely used in production.

**Round 4 conclusion:** after proxy hot-path optimization, lumen-gateway now keeps p90/p95/p99 lower than APISIX in all four benchmark scenarios. APISIX still has slightly higher throughput (+1–5%) and lower p50, but Lumen has the better sustained tail latency profile.

---

## Performance Evolution

This benchmark evolved in three steps:

| Step | Change | Evidence |
|------|--------|----------|
| Baseline | Measured proxy, plugin, routing, and load-balancing microbenchmarks; proxy path showed the highest allocation pressure. | `BenchmarkProxyServeHTTP`: 218 allocs/op in the initial baseline. |
| Hot-path diagnosis | Memory profiles showed that the dominant proxy allocation source was observability label-key construction plus per-request trace timing state, not only upstream request creation. | pprof moved focus from generic request construction to metrics labels and trace timing lifecycle. |
| Optimization | Reused proxy trace timing state with `sync.Pool`, replaced status-class formatting with constants, and removed hot-path `fmt.Fprintf("%q")` label escaping. | `BenchmarkProxyServeHTTP`: 67 -> 65 allocs/op in the focused benchmark, 7.163 KiB/op -> 6.787 KiB/op. |

The macro effect shows up most clearly in the APISIX comparison: Lumen narrowed throughput to within **1–5%** of APISIX while moving the decisive latency range to Lumen's side. In Round 4, Lumen's p95 is **48–59% lower** and p99 is **40–51% lower** across passthrough, pipeline, ramp-up, and spike scenarios.

In short: **优化后尾延迟超过 APISIX**. APISIX remains faster at p50, but under sustained concurrency the optimized Lumen path avoids APISIX's p90/p95/p99 queuing cliff.

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
| Test date | 2026-05-25 |

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
│                   ←── apisix:9080  (exposed :19080)       │
│                                                            │
│  apisix-etcd:2379 ←── apisix (0.5 CPU / 256 MB)          │
│                                                            │
└────────────────────────────────────────────────────────────┘
         ↑                         ↑
    k6 (host)                 k6 (host)
    localhost:18080           localhost:19080
```

---

## Results

### Scenario 1 — Passthrough: Pure Proxy (300 VUs × 60 s)

*No plugins on either gateway. Measures raw proxy overhead.*

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|:-------------:|:------:|:------------:|
| **RPS** | 24,855 | **26,168** | 0.95× |
| avg latency | 12.02 ms | **11.35 ms** | 1.06× |
| p50 | 11.45 ms | **6.33 ms** | 1.81× |
| p75 | 14.45 ms | **9.31 ms** | 1.55× |
| p90 | **18.28 ms** | 41.93 ms | **0.44×** |
| p95 | **21.23 ms** | 49.43 ms | **0.43×** |
| p99 | **29.27 ms** | 57.81 ms | **0.51×** |
| max | **93.08 ms** | 102.40 ms | 0.91× |
| error rate | 0.00% | 0.00% | — |
| total requests | 1,491,613 | 1,570,818 | — |

### Scenario 2 — Pipeline: request_id + access_log (300 VUs × 60 s)

*Both gateways run request_id and buffered access_log. Measures plugin overhead on top of proxy.*

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|:-------------:|:------:|:------------:|
| **RPS** | 22,378 | **22,534** | 0.99× |
| avg latency | 13.35 ms | **13.23 ms** | 1.01× |
| p50 | 12.81 ms | **6.57 ms** | 1.95× |
| p75 | 16.19 ms | **9.95 ms** | 1.63× |
| p90 | **20.46 ms** | 51.14 ms | **0.40×** |
| p95 | **23.61 ms** | 57.82 ms | **0.41×** |
| p99 | **31.95 ms** | 65.65 ms | **0.49×** |
| max | **85.43 ms** | 102.54 ms | 0.83× |
| error rate | 0.00% | 0.00% | — |
| total requests | 1,343,037 | 1,352,218 | — |
| Plugin overhead vs passthrough | −10.0% RPS | −13.9% RPS | — |

### Scenario 3 — Ramp-up: Gradual Saturation (0→500 VUs, pipeline route, 150 s)

*5 stages: 0→100→300→500 VUs (30s each), sustain 500, ramp down.*

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|:-------------:|:------:|:------------:|
| **RPS (avg over full ramp)** | 20,645 | **21,564** | 0.96× |
| avg latency | 13.51 ms | **12.87 ms** | 1.05× |
| p50 | 12.76 ms | **6.77 ms** | 1.88× |
| p75 | 19.63 ms | **11.25 ms** | 1.74× |
| p90 | **25.25 ms** | 49.88 ms | **0.51×** |
| p95 | **29.43 ms** | 58.36 ms | **0.50×** |
| p99 | **39.68 ms** | 67.71 ms | **0.59×** |
| max | **99.06 ms** | 102.99 ms | 0.96× |
| error rate | 0.00% | 0.00% | — |
| total requests | 3,096,772 | 3,234,668 | — |

### Scenario 4 — Spike: Instant Traffic Spike (50→500→50 VUs, pipeline route, 110 s)

*Baseline 50 VU → instant spike to 500 VU (10 s) → sustain 30 s → instant drop → recovery.*

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|:-------------:|:------:|:------------:|
| **RPS (avg over full spike)** | 19,281 | **20,078** | 0.96× |
| avg latency | 11.04 ms | **10.54 ms** | 1.05× |
| p50 | 6.41 ms | **4.44 ms** | 1.44× |
| p75 | 18.90 ms | **10.29 ms** | 1.84× |
| p90 | **25.12 ms** | 33.98 ms | **0.74×** |
| p95 | **29.44 ms** | 56.27 ms | **0.52×** |
| p99 | **39.98 ms** | 67.10 ms | **0.60×** |
| max | **97.29 ms** | 107.59 ms | 0.90× |
| error rate | 0.00% | 0.00% | — |
| total requests | 2,120,987 | 2,208,548 | — |

---

## Summary

| Dimension | lumen-gateway | APISIX | Winner |
|-----------|:-------------:|:------:|:------:|
| Throughput (all scenarios) | 19K–25K RPS | **20K–26K RPS** | APISIX **+1–5%** |
| Median latency (p50) | 6–13 ms | **4–7 ms** | APISIX lower |
| p90 latency (all scenarios) | **18–25 ms** | 34–51 ms | lumen **26–60% lower** |
| p95 latency (all scenarios) | **21–29 ms** | 49–58 ms | lumen **48–59% lower** |
| p99 latency (all scenarios) | **29–40 ms** | 58–68 ms | lumen **40–51% lower** |
| Error rate | 0% | 0% | Tie |
| Latency distribution | Uniform (unimodal) | Bimodal | lumen |

---

## Analysis

### Why APISIX Has Higher Throughput and Lower Median Latency

APISIX uses **nginx worker processes** with LuaJIT coroutines. Each worker handles one request at a time cooperatively. When all workers are idle, a new request gets picked up immediately — explaining the sub-7ms p50. Nginx's battle-hardened C I/O path processes simple requests with extremely low overhead.

Lumen uses **Go goroutines** scheduled across GOMAXPROCS threads (= 2 in the container). Go's scheduler has higher per-request baseline cost than Nginx's C event loop for simple requests, explaining the higher p50 (~6–13ms vs ~4–7ms).

### Why Lumen Has Much Better Tail Latency (p90–p99)

At 300 VUs sustained load, APISIX exhibits a **bimodal latency distribution**: requests either complete in ~6ms (fast path, free worker available) or wait in queue (~50–65ms, all workers busy). This pattern is inherent to the nginx worker process model: with 2 CPUs, there are typically 2 workers, and when both are processing requests, additional requests must wait.

The p90 jumping from ~4–7ms (p50) to ~34–51ms (p90) is the "queue wait" signal — roughly 10% of requests are hitting a full worker pool.

Lumen's goroutine scheduler distributes hundreds of VUs across 2 OS threads cooperatively without a fixed worker count, so the **latency distribution is uniform** — no queuing cliff. The p50→p90 progression is smooth: 11.45ms → 18.28ms (passthrough) instead of APISIX's 6.33ms → 41.93ms.

### Spike Scenario: Recovery Behavior

Both gateways handled the 50→500 VU instant spike with **0% errors**, which is the primary resilience metric. Lumen's spike p99 (39.98ms) vs APISIX's spike p99 (67.10ms) reflects the same bimodal pattern amplified at higher concurrency. After the spike drops back to 50 VUs, both recover immediately (tail latency improvement visible in the p50 = 6.41ms for lumen, 4.44ms for APISIX).

### Plugin Overhead

Adding `request_id` + `access_log` (passthrough → pipeline):
- APISIX: 26,168 → 22,534 RPS = **−13.9%**
- Lumen: 24,855 → 22,378 RPS = **−10.0%**

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

# If local port 9080/9180 is already occupied, move APISIX host ports:
APISIX_PROXY_PORT=19080 APISIX_ADMIN_PORT=19180 \
APISIX_PROXY_URL=http://localhost:19080 APISIX_ADMIN=http://localhost:19180 \
bash docs/benchmark/scripts/setup.sh

APISIX_PROXY_PORT=19080 APISIX_ADMIN_PORT=19180 \
APISIX_PROXY_URL=http://localhost:19080 APISIX_ADMIN=http://localhost:19180 \
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
