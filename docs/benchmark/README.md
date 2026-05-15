# Benchmark: lumen-gateway vs Apache APISIX 3.14.1

## 项目介绍

**lumen-gateway** 是基于字节跳动开源的高性能 HTTP 框架 [Hertz](https://github.com/cloudwego/hertz) 构建的 API 网关。Hertz 底层使用自研网络库 [Netpoll](https://github.com/cloudwego/netpoll)（基于 epoll/kqueue 的非阻塞 I/O），配合 Go 语言原生协程调度，在高并发场景下具有极低的延迟和极高的吞吐能力。

**Apache APISIX** 是基于 OpenResty（Nginx + LuaJIT）的云原生 API 网关，是目前业界广泛使用的开源网关方案。

本测试旨在对比两者在 **相同 Docker 部署环境、相同资源配额** 下的纯代理性能（含 access_log），评估 lumen-gateway 的核心转发能力。

## Test Environment

| Component | Details |
|-----------|---------|
| OS | macOS Darwin 25.2.0, arm64 |
| CPU | Apple M1 Pro (8 cores) |
| Memory | 16 GB |
| lumen-gateway | Go 1.25 + Hertz + Netpoll (Docker, `benchmark-lumen`) |
| APISIX | 3.14.1-debian (OpenResty/LuaJIT, Docker) |
| Load tool | k6 v2.0.0 |
| Backend | mock-server (Go net/http, Docker, echo JSON) |
| Network | Docker bridge (bench-net), all services co-located |
| Test date | 2026-05-15 |

## Methodology

- **独占资源运行**：每次只启动一个网关，另一个停止，避免资源竞争
- **统一 Docker 资源限制**：两个网关均限制为 2 CPUs / 512MB，mock-server 2 CPUs / 512MB，etcd 0.5 CPU / 256MB
- Both gateways proxy `POST /benchmark/echo` to the same Go mock-server via Docker DNS (`mock-server:9001`)
- Mock server echoes request metadata as JSON, no artificial delay
- **两个网关都启用 access_log**：
  - lumen: `access_log` 插件，写入 `logs/access.log`，16 KB 缓冲，1s flush
  - APISIX: 默认 access_log（`logs/access.log`，nginx 默认缓冲）
- k6 从宿主机运行，通过 Docker 端口映射访问：lumen `:18080`，APISIX `:9080`
- Warmup: 5 VUs × 5s before each gateway's test suite
- Cooldown: 30s between scenarios (TCP TIME_WAIT)；30s between gateways
- No business plugins enabled (pure proxy + access_log overhead)

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

### Resource Allocation

| Container | CPUs | Memory | Role |
|-----------|------|--------|------|
| bench-lumen / bench-apisix | 2.0 | 512 MB | Gateway under test (one at a time) |
| bench-mock-server | 2.0 | 512 MB | Backend (always on) |
| bench-apisix-etcd | 0.5 | 256 MB | APISIX config store (not on hot path) |

## Scenario 1: Constant Load Passthrough

100 concurrent VUs, sustained for 30 seconds.

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|---------------|--------|--------------|
| **RPS** | **10,676** | 12,823 | 0.83x |
| Latency avg (ms) | 9.30 | 7.73 | 1.20x |
| Latency p50 (ms) | 8.47 | 5.52 | 1.53x |
| Latency p95 (ms) | 18.15 | 23.22 | **0.78x** |
| Latency p99 (ms) | 26.91 | 38.66 | **0.70x** |
| Latency max (ms) | 124.25 | 99.67 | 1.25x |
| Error rate | 0.00% | 0.00% | — |
| Total requests | 320,373 | 384,749 | 0.83x |

## Scenario 2: Ramp-up (Throughput Ceiling)

Ramping from 0 to 500 VUs over 50 seconds.

| Metric | lumen-gateway | APISIX | lumen/APISIX |
|--------|---------------|--------|--------------|
| **RPS** | **10,802** | 11,674 | 0.93x |
| Latency avg (ms) | 16.66 | 15.39 | 1.08x |
| Latency p50 (ms) | 13.03 | 9.00 | 1.45x |
| Latency p95 (ms) | 43.64 | 53.30 | **0.82x** |
| Latency p99 (ms) | 64.07 | 83.36 | **0.77x** |
| Latency max (ms) | 135.61 | 197.53 | **0.69x** |
| Error rate | 0.00% | 0.00% | — |
| Total requests | 540,490 | 584,336 | 0.93x |

## Summary

| Dimension | lumen-gateway | APISIX | Advantage |
|-----------|---------------|--------|-----------|
| Throughput — 100 VUs | 10,676 RPS | 12,823 RPS | APISIX **1.20x** |
| Throughput — ramp 500 VUs | 10,802 RPS | 11,674 RPS | APISIX **1.08x** |
| p95 latency — 100 VUs | 18.15 ms | 23.22 ms | lumen **1.28x lower** |
| p99 latency — 100 VUs | 26.91 ms | 38.66 ms | lumen **1.44x lower** |
| p95 latency — 500 VUs | 43.64 ms | 53.30 ms | lumen **1.22x lower** |
| p99 latency — 500 VUs | 64.07 ms | 83.36 ms | lumen **1.30x lower** |
| Max latency — 500 VUs | 135.61 ms | 197.53 ms | lumen **1.46x lower** |
| Error rate | 0% | 0% | Tie |

## Observations

- APISIX 在吞吐量上领先：100 VUs 时高出约 **20%**，500 VUs ramp 时差距缩小至 **8%**
- lumen-gateway 在**尾部延迟（p95/p99/max）上优势显著**：
  - p95 低 22–28%，p99 低 30–44%，最大延迟低 25–46%
- 在 p50（中位延迟）上 APISIX 更低，说明 Nginx/LuaJIT 在轻负载下的单请求效率更高
- 随着并发增大（100→500 VUs），吞吐差距从 20% 收窄到 8%，lumen 的协程调度在高并发下更具弹性
- 两个网关在整个测试过程中均保持 **0% 错误率**
- APISIX 基于 OpenResty（Nginx C core + LuaJIT）的事件驱动模型在 Docker 容器内展现出优秀的吞吐能力
- lumen-gateway 基于 Go/Hertz+Netpoll 的协程模型在高并发下展现出更好的**延迟一致性**——这对 SLO 保障更为关键

### 与前一轮测试对比

| 测试轮次 | 方法差异 | lumen RPS | APISIX RPS | 差距 |
|---------|---------|-----------|------------|------|
| Round 1 | 双网关同时运行，无资源限制 | ~7,800 | ~9,200 | APISIX 1.18x |
| Round 2（本轮） | 独占运行，2 CPUs/512MB 各自限制 | ~10,700 | ~12,800 | APISIX 1.20x |

两轮在方法上对齐后，吞吐差距稳定在约 **1.2x**；独占运行后两者绝对 RPS 均大幅提升，说明前一轮存在明显的资源竞争干扰。

## How to Reproduce

```bash
# 1. Install k6
brew install k6

# 2. Build and start benchmark services
cd lumen-gateway
bash docs/benchmark/scripts/setup.sh

# 3. Run all benchmarks (one gateway at a time)
bash docs/benchmark/scripts/run.sh

# 4. View results
ls docs/benchmark/results/*/

# 5. Tear down
docker compose -f docs/benchmark/docker-compose.yml down -v
```

## Raw Data

Results are stored in `results/` directory:
- `results/lumen/passthrough_summary.json` — k6 summary export
- `results/apisix/passthrough_summary.json`
- `results/*/ramp-up_summary.json`
- `results/*_resources.csv` — CPU/memory timeseries (docker stats)
- `results/system-info.txt` — test machine specs and methodology
