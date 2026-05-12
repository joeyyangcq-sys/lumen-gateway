# Benchmark: lumen-gateway vs Apache APISIX 3.14.1

## 项目介绍

**lumen-gateway** 是基于字节跳动开源的高性能 HTTP 框架 [Hertz](https://github.com/cloudwego/hertz) 构建的 API 网关。Hertz 底层使用自研网络库 [Netpoll](https://github.com/cloudwego/netpoll)（基于 epoll/kqueue 的非阻塞 I/O），配合 Go 语言原生协程调度，在高并发场景下具有极低的延迟和极高的吞吐能力。

**Apache APISIX** 是基于 OpenResty（Nginx + LuaJIT）的云原生 API 网关，是目前业界广泛使用的开源网关方案。

本测试旨在对比两者在 **相同 Docker 部署环境** 下的纯代理性能（含 access_log），评估 lumen-gateway 的核心转发能力。

## Test Environment

| Component | Details |
|-----------|---------|
| OS | macOS Darwin 25.2.0, arm64 |
| CPU | Apple M1 Pro (8 cores) |
| Memory | 16 GB |
| lumen-gateway | Go 1.26 + Hertz + Netpoll (Docker) |
| APISIX | 3.14.1-debian (OpenResty/LuaJIT, Docker) |
| Load tool | k6 v2.0.0 |
| Backend | mock-server (Go net/http, Docker, echo JSON) |
| Network | Docker bridge (bench-net), all services on same network |
| Test date | 2026-05-12 |

## Methodology

- **所有服务统一 Docker 部署**：mock-server、lumen-gateway、APISIX 在同一个 Docker bridge 网络中运行
- Both gateways proxy `POST /benchmark/echo` to the same Go mock-server via Docker DNS (`mock-server:9001`)
- Mock server echoes request metadata as JSON, no artificial delay
- **两个网关都启用 access_log**：
  - lumen: `access_log` 插件，写入 `logs/access.log`，16KB 缓冲，1s flush
  - APISIX: 默认 access_log（`logs/access.log`，16KB 缓冲，3s flush）
- k6 从宿主机运行，通过 Docker 端口映射访问：lumen `:18080`，APISIX `:9080`
- k6 uses `constant-vus` (100 VUs) for passthrough, `ramping-vus` (10-500 VUs) for ramp-up
- 30-second cool-down between test runs (TCP TIME_WAIT cleanup)
- Warmup pass (5 VUs, 3s) before each measurement
- No business plugins enabled (pure proxy + access_log overhead)
- Resource collection: `docker stats` for both gateways (unified metric source)

### Network Topology

```
┌─ Docker bench-net ─────────────────────────────────────┐
│                                                         │
│  mock-server:9001 ←── lumen:18080 (exposed to host)    │
│                   ←── apisix:9080 (exposed to host)    │
│                                                         │
│  apisix-etcd:2379 ←── apisix (config store)            │
│                                                         │
└─────────────────────────────────────────────────────────┘
         ↑                        ↑
    k6 (host)                k6 (host)
    localhost:18080          localhost:9080
```

## Scenario 1: Constant Load Passthrough

100 concurrent VUs, sustained for 30 seconds.

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|---------------|--------|--------------|
| RPS achieved | 7,764 | 9,167 | 0.85x |
| Latency avg (ms) | 12.80 | 10.83 | 1.18x |
| Latency p50 (ms) | 11.36 | 9.00 | 1.26x |
| Latency p95 (ms) | 24.68 | 22.27 | 1.11x |
| Latency p99 (ms) | 36.48 | 37.42 | **0.97x** |
| Latency max (ms) | 182.83 | 316.17 | **0.58x** |
| Error rate (%) | 0.00 | 0.00 | - |
| Total requests | 232,988 | 275,061 | 0.85x |

## Scenario 2: Ramp-up (Throughput Ceiling)

Ramping from 10 to 500 VUs over 50 seconds (5 stages: 50/100/200/300/500 VUs).

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|---------------|--------|--------------|
| RPS achieved | 7,825 | 9,662 | 0.81x |
| Latency avg (ms) | 23.00 | 18.60 | 1.24x |
| Latency p50 (ms) | 17.46 | 12.80 | 1.36x |
| Latency p95 (ms) | 60.83 | 53.38 | 1.14x |
| Latency p99 (ms) | 88.24 | 86.87 | **1.02x** |
| Latency max (ms) | 370.67 | 430.41 | **0.86x** |
| Error rate (%) | 0.00 | 0.00 | - |
| Total requests | 391,450 | 483,389 | 0.81x |

## Summary

| Dimension | lumen-gateway | APISIX | Advantage |
|-----------|---------------|--------|-----------|
| Throughput (100 VUs) | ~7,800 RPS | ~9,200 RPS | APISIX **1.18x** |
| Throughput (ramp to 500 VUs) | ~7,800 RPS | ~9,700 RPS | APISIX **1.24x** |
| p95 Latency (100 VUs) | 24.68 ms | 22.27 ms | APISIX **1.11x lower** |
| p99 Latency (100 VUs) | 36.48 ms | 37.42 ms | lumen **1.03x lower** |
| Max Latency (100 VUs) | 182.83 ms | 316.17 ms | lumen **1.73x lower** |
| Error rate | 0% | 0% | Tie |

## Observations

- 在相同 Docker 部署条件下，APISIX 的**平均吞吐量高出 lumen-gateway 约 18-24%**
- APISIX 在中位延迟和 p95 延迟上略优于 lumen-gateway
- lumen-gateway 在**尾部延迟（p99 和 max）上表现更稳定**，最大延迟仅为 APISIX 的 58%
- 两个网关在整个测试过程中均保持 **0% 错误率**
- APISIX 基于 OpenResty（Nginx C core + LuaJIT）的事件驱动模型在 Docker 容器环境下展现出优秀的吞吐能力
- lumen-gateway 基于 Go/Hertz+Netpoll 的协程模型在高并发下展现出更好的延迟一致性（更低的 tail latency）
- 在 500 VUs 时，两者的 p99 延迟接近（88ms vs 87ms），说明高并发下两者性能差距收窄

### 与前一轮测试对比

前一轮测试中 lumen（native）vs APISIX（Docker）显示 3.6-4.1x 差距，主要来自：
1. Docker 网络虚拟化开销（~3ms/req，影响最大）
2. APISIX 启用 access_log 而 lumen 未启用
3. Native vs Docker 运行时差异

本轮公平测试（同 Docker 网络 + 双方都启用 access_log）显示真实差距约为 **0.8-0.85x**（APISIX 略优），验证了前一轮分析中"实际差距远小于 4x"的判断。

## How to Reproduce

```bash
# 1. Install k6
brew install k6

# 2. Start all services (mock-server + lumen + APISIX in Docker)
cd lumen-gateway
bash docs/benchmark/scripts/setup.sh

# 3. Run all benchmarks
bash docs/benchmark/scripts/run.sh

# 4. View results
ls docs/benchmark/results/*/

# 5. Tear down
docker compose -f docs/benchmark/docker-compose.yml down -v
```

## Raw Data

Results are stored in `results/` directory:
- `results/lumen/passthrough_summary.json` - k6 summary export
- `results/apisix/passthrough_summary.json`
- `results/*/ramp-up_summary.json`
- `results/*_resources.csv` - CPU/memory timeseries (docker stats)
- `results/system-info.txt` - test machine specs
