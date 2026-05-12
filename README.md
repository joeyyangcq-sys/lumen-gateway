# Lumen Gateway

High-performance Layer 7 API gateway built on [Hertz](https://github.com/cloudwego/hertz) (ByteDance) + [Netpoll](https://github.com/cloudwego/netpoll). Supports static YAML config and dynamic etcd-backed config with APISIX-compatible Admin API.

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
