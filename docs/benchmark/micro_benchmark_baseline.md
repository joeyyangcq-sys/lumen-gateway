# Micro-Benchmark Baseline & Diagnostic Report

This report documents the baseline performance, memory allocation, structure alignment analysis, and heap escape patterns of the **Lumen Gateway** project.

- **Test Date:** 2026-05-25
- **Go Version:** `go1.26.2 darwin/arm64`
- **CPU:** Apple M3 Max (16 cores)
- **OS:** macOS Darwin (arm64)

---

## 1. Micro-Benchmark Baselines

Run Command:
```bash
/Users/a1/sdk/go1.26.2/bin/go test -bench=. -benchmem -run=^$ ./...
```

### Balancer (`internal/balancer/roundrobin`)
| Benchmark Scenario | Iterations | Latency (`ns/op`) | Memory (`B/op`) | Allocations (`allocs/op`) |
| :--- | :---: | :---: | :---: | :---: |
| `BenchmarkRoundRobinPick10` | 195,864,297 | 5.965 | 0 | 0 |
| `BenchmarkRoundRobinPick100` | 201,574,873 | 5.994 | 0 | 0 |
| `BenchmarkRoundRobinPickParallel10` | 6,375,034 | 182.000 | 0 | 0 |
| `BenchmarkRoundRobinPickParallel100` | 6,294,818 | 189.300 | 0 | 0 |
| `BenchmarkRoundRobinUpdate` | 30,486,804 | 39.020 | 160 | 1 |

### Plugin System (`internal/plugin`)
| Benchmark Scenario | Iterations | Latency (`ns/op`) | Memory (`B/op`) | Allocations (`allocs/op`) |
| :--- | :---: | :---: | :---: | :---: |
| `BenchmarkPluginChain5` | 3,385,972 | 342.700 | 1536 | 1 |
| `BenchmarkPluginChain10` | 3,445,087 | 345.200 | 1536 | 1 |
| `BenchmarkFromRequestContext` | 33,299,340 | 35.360 | 0 | 0 |
| `BenchmarkSetContextValues` | 12,899,662 | 93.860 | 64 | 4 |

### HTTP Reverse Proxy (`internal/proxy`)
| Benchmark Scenario | Iterations | Latency (`ns/op`) | Memory (`B/op`) | Allocations (`allocs/op`) |
| :--- | :---: | :---: | :---: | :---: |
| `BenchmarkProxyServeHTTP` | 84,259 | 12,407.000 | 9,699 | 218 |
| `BenchmarkProxyServeHTTPWithBody` | 91,628 | 13,068.000 | 13,961 | 223 |
| `BenchmarkNewUpstreamRequest` | 3,199,450 | 378.500 | 624 | 7 |
| `BenchmarkIsHopByHopHeader` | 24,076,100 | 47.600 | 0 | 0 |

### Router Matching (`internal/router`)
| Benchmark Scenario | Iterations | Latency (`ns/op`) | Memory (`B/op`) | Allocations (`allocs/op`) |
| :--- | :---: | :---: | :---: | :---: |
| `BenchmarkRouterMatch100` | 972,566 | 1,224.000 | 0 | 0 |
| `BenchmarkRouterMatch1000` | 90,889 | 13,131.000 | 0 | 0 |
| `BenchmarkRouterMatchExact` | 56,090,271 | 21.630 | 0 | 0 |
| `BenchmarkRouterMatchRegex` | 13,332,561 | 90.070 | 0 | 0 |

---

## 2. Structure Field Alignment Analysis

Run Command:
```bash
fieldalignment ./...
```

The following structs contain padding waste due to suboptimal field order:

| File Location | Struct Info | Size/Pointers Change | Optimization Target |
| :--- | :--- | :--- | :--- |
| `internal/bootstrap/bootstrap.go:12:14` | Config/bootstrap options | `136 pointer bytes` $\to$ `120 pointer bytes` | Reduce 16 pointer bytes |
| `internal/controlplane/resource_summary.go:9:22` | `ResourceSummary` | `64 pointer bytes` $\to$ `48 pointer bytes` | Reduce 16 pointer bytes |
| `internal/adminapi/adminapi.go:167:20` | HTTP Handlers/Config structs | `size 80` $\to$ `72` | Reduce 8 bytes |
| `internal/adminapi/adminapi.go:175:22` | API structures | `112 pointer bytes` $\to$ `104 pointer bytes` | Reduce 8 pointer bytes |
| `internal/adminapi/adminapi.go:185:24` | API structures | `120 pointer bytes` $\to$ `112 pointer bytes` | Reduce 8 pointer bytes |
| `internal/adminapi/adminapi.go:195:16` | API structures | `24 pointer bytes` $\to$ `8 pointer bytes` | Reduce 16 pointer bytes |
| `internal/adminapi/adminapi.go:201:23` | API structures | `120 pointer bytes` $\to$ `88 pointer bytes` | Reduce 32 pointer bytes |
| `internal/adminapi/schema.go:17:21` | Admin schema representation | `104 pointer bytes` $\to$ `88 pointer bytes` | Reduce 16 pointer bytes |
| `internal/adminapi/schema.go:26:18` | Admin schema representation | `48 pointer bytes` $\to$ `40 pointer bytes` | Reduce 8 pointer bytes |
| `internal/adminapi/schema.go:41:26` | Admin schema representation | `96 pointer bytes` $\to$ `88 pointer bytes` | Reduce 8 pointer bytes |
| `internal/config/options.go:58:22` | Gateway Options | `184 pointer bytes` $\to$ `168 pointer bytes` | Reduce 16 pointer bytes |
| `internal/router/router.go:150:20` | Compiled Route representation | `144 pointer bytes` $\to$ `136 pointer bytes` | Reduce 8 pointer bytes |
| `internal/gateway/gateway.go:68:14` | `Service` runtime struct | `168 pointer bytes` $\to$ `144 pointer bytes` | Reduce 24 pointer bytes |
| `internal/gateway/gateway.go:77:15` | `Upstream` runtime struct | `288 pointer bytes` $\to$ `264 pointer bytes` | Reduce 24 pointer bytes |

---

## 3. Escape Analysis & Heap Allocation Sources

Run Command:
```bash
go build -gcflags="-m -l" ./...
```

### Key Findings
1. **Context Value Setting (`internal/plugin`)**:
   - `SetContextValues` has 4 allocations per call because the values passed to context methods are wrapped in `any` interface values, causing them to escape to the heap.
2. **Reverse Proxy request path (`internal/proxy`)**:
   - `BenchmarkProxyServeHTTP` has 218 allocations per request, primarily due to:
     - Header copying / merging.
     - Upstream request creation (`newUpstreamRequest`).
     - Request context wrapping.
3. **Bootstrapping and CLI configurations**:
   - CLI flags, configuration loading, and command creation (`*cli.Command` and `*cli.App`) escape to the heap, which is acceptable since it only happens during startup.
