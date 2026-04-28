# Lumen Gateway Route Map

Lumen Gateway is a layer 7 API gateway built on top of Hertz. The first goal is not to clone every feature from Bifrost, but to build a clean gateway core that can evolve safely: routing, proxying, plugins, load balancing, health checks, circuit breaking, observability, and hot reload.

## Design Direction

- Use Hertz as the HTTP server, request context, middleware chain, and client foundation.
- Treat gateway plugins as Hertz-compatible middleware plus gateway metadata.
- Compile `route`, `service`, and `upstream` configuration into an immutable runtime snapshot.
- Swap runtime snapshots atomically for config hot reload.
- Keep extension points small and explicit: plugin factory, balancer factory, provider factory.
- Prefer in-process compile-time plugins for the first version; evaluate WASM/Lua/gRPC plugins later.

## Runtime Model

```text
client
  -> hertz server
  -> route matcher
  -> global plugins
  -> server plugins
  -> route plugins
  -> service plugins
  -> upstream plugins
  -> load balancer
  -> reverse proxy
  -> backend endpoint
```

## Core Concepts

### Route

Routes decide whether a request should enter a service.

Route matching should support:

- HTTP method matching.
- Host matching.
- Exact path matching.
- Prefix path matching.
- Regex path matching.
- Priority ordering.

Route-level plugins are suitable for:

- Authentication.
- Authorization.
- Request rewriting.
- Header manipulation.
- Rate limiting.
- CORS.

### Service

Services describe logical backend applications.

Service config should include:

- Protocol: `http`, `https`, `h2c`, later `grpc`.
- Upstream reference.
- Timeout policy.
- Retry policy.
- Host header policy.
- Service-level plugins.

Service-level plugins are suitable for:

- Shared application auth.
- Request and response transformation.
- Service-specific logging.
- Retry policy hooks.

### Upstream

Upstreams describe backend endpoint pools.

Upstream config should include:

- Endpoint list.
- Balancer type and options.
- Active health check.
- Passive health check.
- Circuit breaker config.
- Upstream-level plugins.

Upstream-level plugins are suitable for:

- Circuit breaking.
- Traffic mirroring.
- Canary traffic splitting.
- Endpoint-level metrics.
- Fault injection.

## Phase 0: Project Skeleton

Goal: create a clean Go module and package boundaries.

Deliverables:

- `cmd/lumen-gateway`: main gateway binary.
- `internal/config`: YAML config model, parser, validation.
- `internal/gateway`: runtime engine and lifecycle.
- `internal/router`: route matcher.
- `internal/plugin`: plugin registry and chain builder.
- `internal/balancer`: load balancer interfaces and built-ins.
- `internal/proxy`: Hertz reverse proxy wrapper.
- `internal/health`: active and passive health checks.
- `internal/observability`: logs and metrics.

## Phase 1: Minimal Layer 7 Proxy

Goal: receive HTTP requests and forward them to a configured upstream.

Features:

- Start one Hertz server.
- Load YAML config at startup.
- Match route by method and path prefix.
- Resolve route to service.
- Resolve service to upstream.
- Pick one endpoint with round-robin.
- Proxy request to backend.
- Preserve basic headers.
- Return backend response to client.

Acceptance tests:

- Request to `/api` reaches backend.
- Unknown route returns `404`.
- Empty upstream returns `503`.
- Backend timeout returns `504`.

## Phase 2: Config Model and Runtime Snapshot

Goal: make the runtime immutable and reload-friendly.

Features:

- Define typed config structs for server, route, service, upstream, plugin, balancer.
- Validate all references before serving traffic.
- Compile config into `RuntimeSnapshot`.
- Store snapshot with `atomic.Pointer`.
- Avoid mutating live route/service/upstream objects after publish.

Runtime shape:

```go
type RuntimeSnapshot struct {
    Router    *Router
    Services  map[string]*Service
    Upstreams map[string]*Upstream
}
```

Acceptance tests:

- Invalid service reference fails validation.
- Invalid upstream reference fails validation.
- Snapshot swap does not affect in-flight requests.

## Phase 3: Plugin System Based on Hertz

Goal: support custom plugins while staying close to Hertz.

Plugin factory:

```go
type Factory func(params any) (app.HandlerFunc, error)
```

Registry:

```go
func Register(name string, factory Factory) error
func FactoryOf(name string) Factory
```

Plugin scopes:

- `global`
- `server`
- `route`
- `service`
- `upstream`

Execution order:

```text
global -> server -> route -> service -> upstream -> proxy
```

Built-in plugins for first version:

- `request_id`
- `access_log`
- `header_transform`
- `strip_prefix`
- `add_prefix`
- `rate_limit_local`

Acceptance tests:

- Route plugin runs only for matching route.
- Service plugin runs for all routes pointing to that service.
- Upstream plugin runs before proxying.
- Plugin order is deterministic.

## Phase 4: Load Balancer Extension

Goal: make load balancing pluggable.

Balancer interface:

```go
type Balancer interface {
    Pick(ctx context.Context, req *app.RequestContext) (*Endpoint, error)
    Update(endpoints []*Endpoint) error
}
```

Factory:

```go
type Factory func(endpoints []*Endpoint, params any) (Balancer, error)
```

Built-in balancers:

- `round_robin`
- `weighted_round_robin`
- `random`
- `consistent_hash`
- `least_conn`

Acceptance tests:

- Round robin distributes traffic evenly.
- Weighted round robin respects weights.
- Unhealthy endpoints are skipped.
- Custom balancer can be registered.

## Phase 5: Health Check and Circuit Breaker

Goal: protect traffic from bad endpoints.

Passive health check:

- Count connection errors.
- Count timeout errors.
- Optionally count `5xx`.
- Mark endpoint unhealthy when failure threshold is reached.
- Recover after fail timeout.

Active health check:

- Periodically request health path.
- Support HTTP method and expected status codes.
- Use success and failure thresholds.
- Move endpoint between `healthy`, `unhealthy`, and `half_open`.

Circuit breaker:

- Per endpoint breaker first.
- Later add per upstream and per route breaker.
- Support closed, open, half-open states.

Acceptance tests:

- Failed endpoint is removed from LB candidates.
- Endpoint returns after successful active probe.
- Half-open permits limited trial requests.

## Phase 6: Observability

Goal: make the gateway diagnosable in production.

Logs:

- Structured JSON logs.
- Access logs.
- Error logs.
- Plugin error logs.

Metrics:

- Request count by route, service, upstream, status.
- Request latency histogram.
- Upstream latency histogram.
- Active connections.
- Endpoint health state.
- Circuit breaker state.

Tracing:

- OpenTelemetry propagation.
- Gateway server span.
- Upstream client span.
- Plugin events where useful.

Acceptance tests:

- `/metrics` exposes Prometheus metrics.
- Access logs include route, service, upstream, status, latency.
- Trace context is propagated upstream.

## Phase 7: Config Hot Reload

Goal: update dynamic config without restarting the process.

Reload flow:

```text
watch config file
  -> parse new config
  -> validate references
  -> build new runtime snapshot
  -> warm up upstream clients
  -> atomic swap
  -> gracefully close old snapshot
```

Dynamic config:

- Routes.
- Services.
- Upstreams.
- Plugins.
- Balancer options.
- Health check options.

Static config:

- Listener bind address.
- TLS listener config.
- Process-level logging output.

Acceptance tests:

- New route works after config change.
- Removed route stops matching after reload.
- Bad config does not replace current snapshot.
- In-flight requests finish on old snapshot.

## Phase 8: Service Discovery

Goal: support dynamic endpoint sources.

Provider interface:

```go
type Provider interface {
    List(ctx context.Context, ref DiscoveryRef) ([]Endpoint, error)
    Watch(ctx context.Context, ref DiscoveryRef) (<-chan []Endpoint, error)
}
```

Providers:

- Static config.
- DNS.
- Kubernetes.
- Nacos or Consul later.

Acceptance tests:

- DNS provider refreshes endpoint list.
- Watch update rebuilds upstream balancer.
- Removed endpoint is drained and closed.

## Phase 9: Production Runtime

Goal: support graceful lifecycle management.

Features:

- Graceful shutdown.
- Signal handling.
- Config test command.
- Optional master-worker hot restart.
- Log reopen signal for log rotation.
- Pprof debug server.

Acceptance tests:

- `SIGTERM` drains active requests.
- `SIGHUP` reloads dynamic config.
- Config test exits non-zero on invalid config.

## First MVP Config Example

```yaml
servers:
  main:
    listen: ":8080"

routes:
  user-api:
    methods: ["GET", "POST"]
    paths:
      - "/api/users"
    service: user-service
    plugins:
      - name: request_id
      - name: strip_prefix
        params:
          prefix: "/api"

services:
  user-service:
    protocol: http
    upstream: user-upstream
    timeout:
      connect: 1s
      read: 3s
      write: 3s

upstreams:
  user-upstream:
    balancer:
      type: round_robin
    health_check:
      passive:
        max_fails: 3
        fail_timeout: 30s
    endpoints:
      - address: "127.0.0.1:9001"
        weight: 1
      - address: "127.0.0.1:9002"
        weight: 1
```

## Near-Term Implementation Order

1. Initialize Go module and basic CLI.
2. Add Hertz server with one hard-coded proxy route.
3. Add YAML config parsing.
4. Add route, service, upstream config compilation.
5. Add round-robin balancer.
6. Add plugin registry using Hertz `app.HandlerFunc`.
7. Add passive health check.
8. Add config hot reload with atomic snapshot swap.
9. Add metrics and access logs.
