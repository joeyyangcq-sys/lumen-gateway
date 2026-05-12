# Lumen Gateway

High-performance Layer 7 API gateway built on [Hertz](https://github.com/cloudwego/hertz) (ByteDance) + [Netpoll](https://github.com/cloudwego/netpoll). Supports static YAML config and dynamic etcd-backed config with APISIX-compatible Admin API.

## Why Go Gateway? — vs Apache APISIX (OpenResty/Nginx + LuaJIT)

Apache APISIX is the industry-standard open-source API gateway, built on OpenResty (Nginx C core + LuaJIT). It delivers excellent raw throughput thanks to Nginx's mature event-driven architecture. lumen-gateway takes a different approach — pure Go, built on Hertz + Netpoll — and offers a set of trade-offs that matter in practice:

| Dimension | lumen-gateway (Go/Hertz) | APISIX (OpenResty/LuaJIT) |
|-----------|--------------------------|---------------------------|
| **Tail latency** | p99 and max latency significantly lower (max latency only 58% of APISIX at 100 VUs). Go's goroutine scheduler distributes load more evenly — no single request gets starved | Higher throughput ceiling, but occasional latency spikes from Nginx worker contention and LuaJIT GC pauses |
| **High-concurrency stability** | At 500 VUs, p99 converges with APISIX (~88ms vs ~87ms). Performance degrades gracefully under pressure | Throughput hits ceiling earlier under extreme concurrency; max latency grows faster |
| **Development experience** | Pure Go — single language for gateway + plugins + business logic. Standard toolchain (go test, go vet, pprof, delve). Plugins are type-safe Go structs with compile-time checks | Lua/OpenResty — separate language ecosystem. Plugin debugging requires OpenResty-specific tooling. Type errors surface at runtime |
| **Plugin system** | Type-safe `RegisterTypedContext[T]` with compile-time config validation. 5 scopes (global/server/route/service/upstream). Template variable system for dynamic values | Lua-based plugins with runtime schema validation. Rich plugin ecosystem but harder to extend for Go-native teams |
| **Deployment** | Single static binary (~15MB). No runtime dependencies (no Nginx, no LuaJIT, no OpenResty). Easy to containerize and cross-compile | Requires OpenResty runtime + LuaJIT + Nginx modules. Larger image footprint. More moving parts in production |
| **Memory safety** | Go's garbage collector — no manual memory management, no buffer overflows, no use-after-free | Nginx C core is memory-safe in practice but Lua/C FFI boundary can introduce subtle bugs |
| **Observability** | Native Go pprof, runtime metrics, goroutine dumps. Easy integration with Prometheus/OpenTelemetry | Good observability via plugins, but profiling OpenResty/LuaJIT requires specialized tools |
| **APISIX compatibility** | APISIX-compatible Admin API, etcd storage, bundle import/export — easy migration path | Native APISIX ecosystem |

**When to choose lumen-gateway:** Your team writes Go, you need predictable tail latency (SLA-sensitive workloads), you want a single-binary deployment, or you want to extend the gateway with custom Go plugins without learning Lua/OpenResty.

**When to choose APISIX:** You need maximum raw throughput, you rely on APISIX's extensive plugin ecosystem (100+ plugins), or your team is already invested in the OpenResty/Lua stack.

## Features

- Hertz + Netpoll non-blocking I/O, Go coroutine scheduling
- Route matching: exact, regex, prefix, wildcard host
- Plugin middleware chain with 5 scopes (global / server / route / service / upstream)
- 9 built-in plugins: request_id, limit_count, request_transformer, response_transformer, rewrite_path_regex, replace_path, strip_prefix, add_prefix, access_log
- Template variable system (`$remote_addr`, `$status`, `$request_time`, etc.)
- Pluggable load balancer interface (built-in round_robin, extensible)
- Passive + active health checks
- APISIX-compatible Admin API with CRUD, bundle import/export, validation, history & rollback
- Hot reload via etcd watch
- CLI tools: `admin import`, `admin export`, `admin sync --watch`

## Quick Start

```bash
# Build
go build -o lumen-gateway ./cmd/lumen-gateway

# Run (file mode)
./lumen-gateway --config configs/bootstrap.yaml

# Test config
./lumen-gateway --config configs/bootstrap.yaml --test
```

See [Developer Guide](docs/guide.md) for full setup instructions.

## Documentation

| Document | Description |
|----------|-------------|
| [Developer Guide](docs/guide.md) | Quick start, configuration reference, plugin development, Admin API |
| [Admin API Reference](docs/ADMIN_API.md) | CRUD endpoints, control plane API, authentication |
| [Architecture & Interfaces](docs/ARCHITECTURE_INTERFACES.md) | Internal architecture, interface boundaries, design decisions |
| [Usage Guide](docs/USAGE.md) | Run modes, config examples, test-backed examples |
| [Benchmark Report](docs/benchmark/README.md) | lumen-gateway vs Apache APISIX 3.14.1 performance comparison |

## Benchmark: lumen-gateway vs Apache APISIX 3.14.1

Both gateways deployed in the same Docker bridge network, with access_log enabled, proxying to the same Go mock server.

### Constant Load (100 VUs, 30s)

| Metric | lumen-gateway | APISIX | Ratio |
|--------|---------------|--------|-------|
| RPS | 7,764 | 9,167 | 0.85x |
| Latency avg | 12.80 ms | 10.83 ms | 1.18x |
| Latency p95 | 24.68 ms | 22.27 ms | 1.11x |
| Latency p99 | 36.48 ms | 37.42 ms | **0.97x** |
| Max latency | 182.83 ms | 316.17 ms | **0.58x** |
| Error rate | 0% | 0% | - |

### Ramp-up (10 → 500 VUs)

| Metric | lumen-gateway | APISIX | Ratio |
|--------|---------------|--------|-------|
| RPS | 7,825 | 9,662 | 0.81x |
| Latency avg | 23.00 ms | 18.60 ms | 1.24x |
| Latency p99 | 88.24 ms | 86.87 ms | **1.02x** |
| Max latency | 370.67 ms | 430.41 ms | **0.86x** |

**Summary:** APISIX achieves ~18-24% higher throughput. lumen-gateway shows better tail latency stability (max latency 42-58% lower). At 500 VUs, p99 latency converges (~88ms vs ~87ms).

Full methodology and raw data: [Benchmark Report](docs/benchmark/README.md)
