# Lumen Gateway Architecture and Interface Boundaries

## 1. Goal

This document explains where `lumen-gateway` already follows a Bifrost-style abstraction approach, and where we should keep tightening boundaries for testing, mocking, and future extension.

The short answer is:

- **yes, we already introduced the right core interfaces in several important places**
- **but we are not yet as fully abstracted as Bifrost in every layer**

## 2. Current interface boundaries

### 2.1 Config source

File:

- `internal/provider/provider.go:22`

Interface:

```go
type Source interface {
    Load(ctx context.Context) (config.Options, error)
    Watch(ctx context.Context, onUpdate func(Update)) error
    Close() error
}
```

Why it matters:

- lets us swap `file` and `etcd_apisix`
- keeps bootstrap and runtime reload logic independent from etcd details
- makes watch behavior unit-testable

This is very much in the same spirit as Bifrost's provider boundary.

### 2.2 Control-plane persistence

File:

- `internal/controlplane/controlplane.go:55`

Interface:

```go
type Store interface {
    List(ctx context.Context, kind ResourceKind) ([]Envelope, error)
    Get(ctx context.Context, kind ResourceKind, id string) (Envelope, error)
    Put(ctx context.Context, kind ResourceKind, id string, body json.RawMessage) (Envelope, error)
    Delete(ctx context.Context, kind ResourceKind, id string) (DeleteResult, error)
    Close() error
}
```

Companion history boundary:

- `internal/controlplane/history.go:14`

Why it matters:

- `controlplane.Service` is decoupled from etcd
- Admin API, preview/apply, export, history, rollback are all testable without real etcd
- future storage backends stay possible

This is one of the cleanest abstraction layers in the project today.

### 2.3 Admin API service seam

File:

- `internal/adminapi/adminapi.go:20`

The Admin API depends on a local `service` interface rather than directly on `controlplane.Service`.

Why it matters:

- makes Admin API handlers easy to mock in tests
- keeps handler tests focused on HTTP contract instead of store behavior

This is a good "port" style seam even though it is local and unexported.

### 2.4 Proxy boundary

File:

- `internal/proxy/proxy.go:35`

Interface:

```go
type Proxy interface {
    ServeHTTP(ctx context.Context, c *app.RequestContext, target Target) error
}
```

Why it matters:

- gateway runtime depends on proxy behavior, not on one concrete transport
- makes upstream handling mockable in gateway tests
- keeps room for future `grpc`, websocket, h2c, or retry-aware implementations

This matches the same kind of separation Bifrost has between gateway orchestration and concrete proxy implementations.

### 2.5 Balancer boundary

File:

- `internal/balancer/balancer.go:16`

Why it matters:

- upstream selection stays separate from proxying
- round-robin is not hard-coded into service execution
- future consistent-hash or weighted strategies can plug in cleanly

### 2.6 Plugin context boundary

File:

- `internal/plugin/context.go:13`

Interface:

- `PluginContext`

Why it matters:

- plugins no longer have to depend directly on raw Hertz APIs
- request/response metadata access is standardized
- route/service/upstream/runtime metadata is mockable in plugin tests
- makes future plugin phases and observability hooks much easier to evolve

This is a strong extension seam and one of the most important evolutions beyond the earliest skeleton.

### 2.7 Observability boundary

File:

- `internal/observability/observability.go:60`

Interface:

- `Recorder`

Why it matters:

- plugin timing and upstream timing can be tested without coupling to one metrics backend
- Prometheus output is currently one renderer, not the only possible sink

## 3. Internal test seams created for mocking

There are also narrower internal interfaces used mainly to keep unit tests cheap and focused.

Examples:

- `internal/controlplane/etcdstore.go:17`
  - `etcdKVClient`
- `internal/provider/provider.go:62`
  - `etcdKVClient`

Why these are useful:

- avoid needing a real etcd client in unit tests
- allow table-driven tests around error mapping and list/watch behavior

These are not architecture-level public interfaces, but they are still good engineering seams.

## 4. Where we already look like Bifrost

The following design choices are clearly aligned with the useful parts of Bifrost:

- `provider.Source` for config loading and watch
- `controlplane.Store` / `HistoryStore` for storage abstraction
- `balancer.Balancer` for endpoint selection
- `proxy.Proxy` for transport/runtime execution
- plugin registry and `PluginContext` for extensibility
- Admin API isolated from store details through a service boundary

So the answer to your question is:

- **yes, the code does already use interfaces in the right places for mockability, unit tests, and extension**

## 5. Where we are still not fully abstracted yet

There are still a few areas where we are more concrete than ideal.

### 5.1 Runtime compiler is now a distinct seam

Files:

- `internal/gateway/compiler.go:1`
- `internal/gateway/gateway.go:81`

New boundary:

```go
type RuntimeCompiler interface {
    Compile(options config.Options) (*RuntimeSnapshot, error)
}
```

Why this helps:

- `Gateway` no longer hard-depends on one concrete snapshot builder
- tests can inject compile failures or custom runtime snapshots cheaply
- future alternate compilers can swap plugin packs, balancers, or proxy factories

This is exactly the kind of seam that moves the project a step closer to Bifrost's orchestration/runtime split.

### 5.2 Snapshot compilation is still one concrete implementation

Current state:

- `Compiler` is separated, but the default implementation still assembles all phases in one place

Potential improvement:

- split the default compiler into smaller compile stages:
  - plugin compilation
  - upstream compilation
  - service compilation
  - route compilation

Benefit:

- easier to unit-test compilation in isolation
- cleaner future support for alternate plugin packs or runtime pipelines

### 5.3 Router is still used concretely

Current state:

- runtime snapshot stores `*router.Router`

Potential improvement:

- define a small matcher interface

Benefit:

- easier routing-focused tests and alternate matcher experimentation

### 5.4 Admin handler is singular

File:

- `internal/gateway/gateway.go:32`

Current state:

- gateway accepts one `AdminHandler`

Potential improvement:

- evolve toward a lightweight HTTP subrouter or handler chain

Benefit:

- future admin API, debug endpoints, health endpoints, and UI assets compose more cleanly

### 5.5 HTTP proxy transport options are still one implementation path

Current state:

- `HTTPProxy` is well-isolated, but still one concrete implementation

Potential improvement:

- define explicit transport factory and retry policy seams

Benefit:

- easier support for richer upstream behavior without bloating one struct

## 6. Practical recommendation

We should keep the current direction.

Recommended rule of thumb:

- keep **architecture-level interfaces** only at true module boundaries
- keep **small local interfaces** when they materially improve tests
- do **not** add interfaces for every struct just for style

That is also the sensible lesson to take from Bifrost:

- abstract the boundaries that change
- keep the internal happy-path code concrete where it improves readability

## 7. Recommended next abstraction steps

If we want to tighten this further, the next best refactors are:

1. split the default `RuntimeCompiler` into smaller compile phases
2. add a small route matcher interface
3. separate admin/debug/metrics HTTP dispatch from gateway root handler
4. extract proxy transport/retry policy seams

These would improve:

- mockability
- unit-test granularity
- future MCP/UI/control-plane growth
- alternate runtime behaviors
