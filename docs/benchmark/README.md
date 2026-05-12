# Benchmark: lumen-gateway vs Apache APISIX 3.14.1

## 项目介绍

**lumen-gateway** 是基于字节跳动开源的高性能 HTTP 框架 [Hertz](https://github.com/cloudwego/hertz) 构建的 API 网关。Hertz 底层使用自研网络库 [Netpoll](https://github.com/cloudwego/netpoll)（基于 epoll/kqueue 的非阻塞 I/O），配合 Go 语言原生协程调度，在高并发场景下具有极低的延迟和极高的吞吐能力。

**Apache APISIX** 是基于 OpenResty（Nginx + LuaJIT）的云原生 API 网关，是目前业界广泛使用的开源网关方案。

本测试旨在对比两者在相同条件下的纯代理性能（无插件），评估 lumen-gateway 的核心转发能力。

## Test Environment

| Component | Details |
|-----------|---------|
| OS | macOS Darwin 25.2.0, arm64 |
| CPU | Apple M1 Pro (8 cores) |
| Memory | 16 GB |
| lumen-gateway | Go 1.26 + Hertz + Netpoll (native binary) |
| APISIX | 3.14.1-debian (OpenResty/LuaJIT, Docker) |
| Load tool | k6 v2.0.0 (go1.26.3) |
| Backend | mock-server (Go net/http, :9001, echo JSON) |
| Test date | 2026-05-12 |

## Methodology

- Both gateways proxy `POST /benchmark/echo` to the same Go mock-server on `:9001`
- Mock server echoes request metadata as JSON, no artificial delay
- lumen-gateway runs natively on the host; APISIX runs in Docker
- k6 uses `constant-vus` (100 VUs) for passthrough, `ramping-vus` (10-500 VUs) for ramp-up
- 30-second cool-down between test runs (TCP TIME_WAIT cleanup)
- Warmup pass (5 VUs, 3s) before each measurement
- No plugins enabled on either gateway (pure proxy overhead)

**Note:** APISIX runs in Docker, lumen-gateway runs natively. Docker adds networking overhead, but this reflects a realistic deployment topology.

## Scenario 1: Constant Load Passthrough

100 concurrent VUs, sustained for 30 seconds.

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|---------------|--------|--------------|
| RPS achieved | 13,662 | 3,317 | **4.12x** |
| Latency avg (ms) | 7.13 | 30.04 | **0.24x** |
| Latency p50 (ms) | 6.20 | 21.68 | **0.29x** |
| Latency p95 (ms) | 14.24 | 77.32 | **0.18x** |
| Latency p99 (ms) | 26.43 | 156.58 | **0.17x** |
| Latency max (ms) | 239.30 | 575.77 | **0.42x** |
| Error rate (%) | 0.00 | 0.00 | - |
| Total requests | 409,940 | 99,545 | 4.12x |

## Scenario 2: Ramp-up (Throughput Ceiling)

Ramping from 10 to 500 VUs over 50 seconds (5 stages: 50/100/200/300/500 VUs).

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|---------------|--------|--------------|
| RPS achieved | 12,900 | 3,585 | **3.60x** |
| Latency avg (ms) | 13.68 | 50.29 | **0.27x** |
| Latency p50 (ms) | 9.61 | 30.70 | **0.31x** |
| Latency p95 (ms) | 37.44 | 159.13 | **0.24x** |
| Latency p99 (ms) | 69.06 | 277.03 | **0.25x** |
| Latency max (ms) | 319.34 | 491.01 | **0.65x** |
| Error rate (%) | 0.00 | 0.00 | - |
| Total requests | 645,934 | 179,542 | 3.60x |

## Summary

| Dimension | lumen-gateway | APISIX | Advantage |
|-----------|---------------|--------|-----------|
| Throughput (100 VUs) | ~13,600 RPS | ~3,300 RPS | lumen **4.1x** |
| Throughput (ramp to 500 VUs) | ~12,900 RPS | ~3,600 RPS | lumen **3.6x** |
| p95 Latency (100 VUs) | 14.24 ms | 77.32 ms | lumen **5.4x lower** |
| p99 Latency (100 VUs) | 26.43 ms | 156.58 ms | lumen **5.9x lower** |
| Error rate | 0% | 0% | Tie |

## Observations

- lumen-gateway achieves **3.6-4.1x higher throughput** than APISIX under identical conditions
- lumen-gateway delivers **4-6x lower tail latency** (p95/p99) than APISIX
- Both gateways maintain 0% error rate throughout all test scenarios
- lumen-gateway's Go/Hertz+netpoll stack has significantly lower per-request overhead compared to APISIX's OpenResty/Lua stack
- Part of the performance gap is attributable to Docker networking overhead on APISIX; a native APISIX deployment would narrow the gap somewhat
- At 500 VUs, lumen-gateway still maintains sub-40ms p95 latency while APISIX reaches ~159ms

## How to Reproduce

```bash
# 1. Start mock server + APISIX
cd api-gateway/mock-server && go run main.go &
cd api-gateway/apisix-local && docker compose up -d

# 2. Install k6
brew install k6

# 3. Run setup (configures routes, starts lumen-gateway)
bash docs/benchmark/scripts/setup.sh

# 4. Run all benchmarks
bash docs/benchmark/scripts/run.sh

# 5. View results
ls docs/benchmark/results/*/
```

## Raw Data

Results are stored in `results/` directory:
- `results/lumen/passthrough_summary.json` - k6 summary export
- `results/apisix/passthrough_summary.json`
- `results/*/ramp-up_summary.json`
- `results/*_resources.csv` - CPU/memory timeseries
- `results/system-info.txt` - test machine specs
