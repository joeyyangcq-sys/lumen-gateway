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

### Router Matching Optimization Round 1 (`internal/router`)
Run Commands:
```bash
go test ./internal/router -bench=BenchmarkRouterMatch -benchmem -count=10 -run=^$ > /tmp/lumen-router-before.txt
go test ./internal/router -bench=BenchmarkRouterMatch -benchmem -count=10 -run=^$ > /tmp/lumen-router-after.txt
benchstat /tmp/lumen-router-before.txt /tmp/lumen-router-after.txt
```

| Benchmark Scenario | Before | After | Change | Memory |
| :--- | :---: | :---: | :---: | :---: |
| `BenchmarkRouterMatch100` | 1,206.0 ns/op | 334.4 ns/op | -72.28% | 0 B/op, 0 allocs/op |
| `BenchmarkRouterMatch1000` | 13.332 us/op | 3.186 us/op | -76.11% | 0 B/op, 0 allocs/op |
| `BenchmarkRouterMatchExact` | 21.41 ns/op | 12.21 ns/op | -42.98% | 0 B/op, 0 allocs/op |
| `BenchmarkRouterMatchRegex` | 90.77 ns/op | 89.50 ns/op | -1.39% | 0 B/op, 0 allocs/op |

Implementation note: pure hostless exact/prefix route tables now use a pre-sorted fast path by route priority and path specificity. Mixed host or regex route tables keep the original full matcher to preserve behavior.

### Proxy Allocation Optimization Round 1 (`internal/proxy`)
Run Commands:
```bash
go test ./internal/proxy -bench=BenchmarkProxyServeHTTP -benchmem -count=8 -run=^$ > /tmp/lumen-proxy-before-labels.txt
go test ./internal/proxy -bench=BenchmarkProxyServeHTTP -benchmem -count=8 -run=^$ > /tmp/lumen-proxy-after-pool.txt
benchstat /tmp/lumen-proxy-before-labels.txt /tmp/lumen-proxy-after-pool.txt
```

| Benchmark Scenario | Before | After | Change | Memory Change |
| :--- | :---: | :---: | :---: | :---: |
| `BenchmarkProxyServeHTTP` | 5.872 us/op, 67 allocs/op | 5.707 us/op, 65 allocs/op | -2.81% latency, -2 allocs/op | 7.163 KiB/op -> 6.787 KiB/op |
| `BenchmarkProxyServeHTTPWithBody` | 6.694 us/op, 72 allocs/op | 6.538 us/op, 70 allocs/op | -2.32% latency, -2 allocs/op | 11.33 KiB/op -> 10.94 KiB/op |

Profiler diagnosis: the largest allocation source in proxy benchmarks was not `newUpstreamRequest` itself, but upstream observability label-key construction plus per-request trace timing state. The accepted change reuses `traceTimings` with `sync.Pool` and replaces hot-path status-class formatting with constant strings. A manual `http.Request` construction experiment reduced `BenchmarkNewUpstreamRequest` latency but regressed full `ServeHTTP`, so it was rejected.

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

Current focused scan after runtime struct reordering:
```bash
fieldalignment ./internal/gateway ./internal/config ./internal/plugin ./internal/proxy
```

`internal/gateway.Service`, `internal/gateway.Upstream`, and `internal/config.Options` no longer report padding opportunities. Remaining focused findings are limited to `internal/plugin.hertzPluginContext` and two test-only structs.

---

## 3. Escape Analysis & Heap Allocation Sources

Run Command:
```bash
go build -gcflags="-m -l" ./...
```

### Key Findings
1. **Context Value Setting (`internal/plugin`)**:
   - Current `BenchmarkSetContextValues` is `~35 ns/op`, `0 B/op`, `0 allocs/op`. The typed `hertzPluginContext` fast path avoids the earlier implicit `any` heap allocation.
2. **Reverse Proxy request path (`internal/proxy`)**:
   - Current `BenchmarkProxyServeHTTP` is `65 allocs/op` after trace timing reuse. Remaining allocations primarily come from `httptrace.ClientTrace` closures, `net/http` request cloning/header handling, Hertz request/URI handling in the benchmark context, and response/header copying.
3. **Bootstrapping and CLI configurations**:
   - CLI flags, configuration loading, and command creation (`*cli.Command` and `*cli.App`) escape to the heap, which is acceptable since it only happens during startup.
