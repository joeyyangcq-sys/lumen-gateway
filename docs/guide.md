# lumen-gateway Developer Guide

lumen-gateway is a high-performance API gateway built on [Hertz](https://github.com/cloudwego/hertz) (ByteDance) with [Netpoll](https://github.com/cloudwego/netpoll) for non-blocking I/O. It supports static file-based configuration and dynamic etcd-backed configuration with an APISIX-compatible Admin API.

---

## Table of Contents

- [Quick Start](#quick-start)
- [Configuration Reference](#configuration-reference)
  - [Bootstrap Config](#bootstrap-config)
  - [Gateway Config](#gateway-config)
- [Plugin Development](#plugin-development)
  - [Plugin Architecture](#plugin-architecture)
  - [Writing a Plugin](#writing-a-plugin)
  - [Template Variables](#template-variables)
  - [Built-in Plugins](#built-in-plugins)
- [Admin API](#admin-api)
  - [Starting the Admin API](#starting-the-admin-api)
  - [CRUD Endpoints](#crud-endpoints)
  - [Control Plane Endpoints](#control-plane-endpoints)
  - [CLI Commands](#cli-commands)
- [Routing](#routing)

---

## Quick Start

### Prerequisites

- Go 1.25+
- (Optional) etcd 3.5+ for dynamic configuration mode

### Build

```bash
cd lumen-gateway
go build -o lumen-gateway ./cmd/lumen-gateway
```

### Run with Static Config (File Mode)

Create a bootstrap config `configs/bootstrap.yaml`:

```yaml
gateway:
  listen: ":18080"
  source: file

file:
  path: configs/lumen.yaml
```

Create the gateway config `configs/lumen.yaml`:

```yaml
servers:
  main:
    listen: ":18080"

routes:
  my-api:
    methods: ["GET", "POST"]
    paths:
      - "/api/v1/*"
    service: my-service

services:
  my-service:
    protocol: http
    upstream: my-upstream
    timeout:
      connect: 3s
      read: 5s
      write: 5s

upstreams:
  my-upstream:
    balancer:
      type: round_robin
    endpoints:
      - address: "127.0.0.1:8080"
        weight: 1
```

Start the gateway:

```bash
./lumen-gateway --config configs/bootstrap.yaml
```

Test the config without starting:

```bash
./lumen-gateway --config configs/bootstrap.yaml --test
```

### Run with etcd (Dynamic Mode)

```yaml
# configs/bootstrap.yaml
gateway:
  listen: ":18080"
  source: etcd_apisix

etcd:
  endpoints:
    - "127.0.0.1:2379"
  prefix: "/apisix"
  dial_timeout: 3s

admin:
  key: "your-admin-api-key"
```

In this mode, routes/services/upstreams are managed via the Admin API or etcd directly. The gateway watches etcd for changes and hot-reloads automatically.

### Docker

```bash
docker build -t lumen-gateway .
docker run -p 18080:18080 -v $(pwd)/configs:/app/configs lumen-gateway --config configs/bootstrap.yaml
```

---

## Configuration Reference

lumen-gateway uses a two-layer config system: a **bootstrap config** (how the gateway starts) and a **gateway config** (routes, services, upstreams, plugins).

### Bootstrap Config

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `gateway.listen` | string | `":18080"` | Gateway listen address |
| `gateway.source` | string | `"file"` | Config source: `file` or `etcd_apisix` |
| `file.path` | string | `"configs/lumen.yaml"` | Path to gateway config (file mode) |
| `etcd.endpoints` | []string | - | etcd endpoints (etcd mode, required) |
| `etcd.prefix` | string | `"/apisix"` | etcd key prefix |
| `etcd.dial_timeout` | duration | `3s` | etcd connection timeout |
| `etcd.username` | string | - | etcd auth username |
| `etcd.password` | string | - | etcd auth password |
| `admin.key` | string | `"local-dev-admin-key"` | Admin API authentication key (X-API-KEY header) |

### Gateway Config

The gateway config defines the data plane: servers, routes, services, upstreams, and plugins.

#### Servers

```yaml
servers:
  main:
    listen: ":18080"
    plugins:                    # server-scoped plugins
      - name: request_transformer
        params:
          add:
            headers:
              X-Server: main
```

#### Routes

```yaml
routes:
  my-route:
    hosts: ["api.example.com"]  # optional, wildcard: *.example.com
    methods: ["GET", "POST"]    # optional, matches all if omitted
    paths:                      # required, at least one
      - "/api/v1/users"         # prefix match (default)
      - "= /health"             # exact match
      - "~ ^/api/v[0-9]+"       # regex match
      - "~* ^/API/V[0-9]+"     # case-insensitive regex
      - "/api/v1/*"             # explicit prefix wildcard
    priority: 10                # higher wins when multiple routes match
    service: my-service         # required, references a service ID
    plugins:                    # route-scoped plugins
      - name: limit_count
        params:
          count: 100
          time_window: 60
```

#### Services

```yaml
services:
  my-service:
    protocol: http              # http or https
    upstream: my-upstream       # required, references an upstream ID
    timeout:
      connect: 3s
      read: 5s
      write: 5s
    plugins:                    # service-scoped plugins
      - use: shared-plugin-id   # reference a named plugin from `plugins` section
```

#### Upstreams

```yaml
upstreams:
  my-upstream:
    scheme: http                # http (default) or https
    pass_host: pass             # pass | node | rewrite
    upstream_host: "internal"   # required when pass_host=rewrite
    balancer:
      type: round_robin         # built-in, extensible via WithBalancerType
      params: {}                # balancer-specific params
    health_check:
      passive:
        max_fails: 3
        fail_timeout: 30s
      active:
        path: /health
        method: GET
        interval: 10s
    endpoints:
      - address: "127.0.0.1:8080"
        weight: 1
        tags:
          zone: us-east-1
      - address: "127.0.0.1:8081"
        weight: 2
    plugins:                    # upstream-scoped plugins
      - name: request_transformer
        params:
          add:
            query:
              upstream: my-upstream
```

#### Global Plugins

```yaml
global_plugins:
  - name: request_id
    params:
      header_name: X-Request-Id
      algorithm: uuid
  - name: access_log
    params:
      path: "logs/access.log"
      format: '$remote_addr - [$time_local] "$request_method $request_uri" $status $body_bytes_sent $request_time'
      buffer_size: 16384
      flush_interval: "1s"
```

#### Named Plugins (Reusable)

Define a plugin once and reference it by ID with `use`:

```yaml
plugins:
  my-rate-limiter:
    name: limit_count
    params:
      count: 1000
      time_window: 60

routes:
  api-route:
    paths: ["/api/*"]
    service: api-service
    plugins:
      - use: my-rate-limiter    # references plugins.my-rate-limiter
```

#### Logging

```yaml
logging:
  level: info                   # debug, info, warn, error
  format: json                  # json or text
```

---

## Plugin Development

### Plugin Architecture

Plugins are registered in a `Registry` and execute as middleware in the Hertz handler chain. Each plugin has:

- **Name** - unique identifier
- **Priority** - execution order (higher priority executes first; negative values run after the proxy)
- **Scopes** - where the plugin can be applied: `global`, `server`, `route`, `service`, `upstream`

The execution order follows this chain:

```
Request → Global Plugins → Server Plugins → Route Plugins → Service Plugins
        → Upstream Plugins → Proxy → (response flows back through the chain)
```

Within each scope, plugins are sorted by priority (descending). A plugin with priority `1200` runs before one with priority `100`. Plugins with negative priority (like `access_log` at `-100`) typically run after `pc.Next(ctx)` returns.

### Writing a Plugin

#### Step 1: Define the Config Struct

```go
package builtin

type myPluginConfig struct {
    Enabled bool   `yaml:"enabled"`
    Message string `yaml:"message"`
}
```

#### Step 2: Write the Registration Function

Use `plugin.RegisterTypedContext` for type-safe config decoding:

```go
func registerMyPlugin(registry *plugin.Registry) error {
    return plugin.RegisterTypedContext(registry, plugin.Metadata{
        Name:     "my_plugin",
        Priority: 50,
        Scopes:   plugin.AllScopes(),
    }, func(cfg myPluginConfig) (plugin.ContextHandler, error) {
        // Validate config at registration time
        if cfg.Message == "" {
            return nil, errors.New("my_plugin requires message")
        }

        // Return the handler — this closure runs per-request
        return func(ctx context.Context, pc plugin.PluginContext) {
            // Pre-proxy logic
            pc.SetRequestHeader("X-My-Plugin", cfg.Message)

            // Call next handler in chain (eventually hits the proxy)
            pc.Next(ctx)

            // Post-proxy logic (response is available here)
            pc.SetResponseHeader("X-Processed-By", "my_plugin")
        }, nil
    })
}
```

#### Step 3: Register in builtin.go

Add your registration function to the `registers` slice in `builtin.go`:

```go
func Register(registry *plugin.Registry) error {
    registers := []func(*plugin.Registry) error{
        registerRequestID,
        registerLimitCount,
        // ... other plugins ...
        registerMyPlugin,       // add here
        registerAccessLog,
    }
    // ...
}
```

#### Step 4: Use in Config

```yaml
routes:
  my-route:
    paths: ["/api/*"]
    service: my-service
    plugins:
      - name: my_plugin
        params:
          enabled: true
          message: "hello from my plugin"
```

### External Plugin Registration

Plugins can also be registered externally without modifying the builtin package:

```go
package main

import (
    lumen "github.com/joey/lumen-gateway"
    "github.com/joey/lumen-gateway/plugin"
)

func main() {
    lumen.Run(
        lumen.WithPlugins(registerCustomPlugins),
    )
}

func registerCustomPlugins(r *plugin.Registry) error {
    return plugin.RegisterTypedContext(r, plugin.Metadata{
        Name:     "custom_auth",
        Priority: 2000,
        Scopes:   []plugin.Scope{plugin.ScopeGlobal, plugin.ScopeRoute},
    }, func(cfg AuthConfig) (plugin.ContextHandler, error) {
        return func(ctx context.Context, pc plugin.PluginContext) {
            token := pc.RequestHeader("Authorization")
            if token == "" {
                pc.SetResponseStatus(401)
                pc.SetResponseBody([]byte(`{"error":"unauthorized"}`))
                pc.Abort()
                return
            }
            pc.Next(ctx)
        }, nil
    })
}
```

### PluginContext API

The `PluginContext` interface provides access to request/response data:

**Request:**

| Method | Description |
|--------|-------------|
| `RequestMethod() string` | HTTP method (GET, POST, etc.) |
| `SetRequestMethod(string)` | Override HTTP method |
| `RequestHost() string` | Request Host header |
| `SetRequestHost(string)` | Override Host header |
| `RequestPath() string` | URL path |
| `SetRequestPath(string)` | Override URL path |
| `RequestURI() string` | Full URI (path + query) |
| `RequestQuery(key) string` | Get query parameter |
| `AddRequestQuery(key, value)` | Add query parameter |
| `SetRequestQuery(key, value)` | Set query parameter |
| `DelRequestQuery(key)` | Delete query parameter |
| `RequestHeader(key) string` | Get request header |
| `AddRequestHeader(key, value)` | Add request header |
| `SetRequestHeader(key, value)` | Set request header |
| `DelRequestHeader(key)` | Delete request header |
| `RequestBody() []byte` | Request body |
| `SetRequestBody([]byte)` | Override request body |
| `ClientIP() string` | Client IP address |

**Response (available after `pc.Next(ctx)`):**

| Method | Description |
|--------|-------------|
| `ResponseStatus() int` | Response status code |
| `SetResponseStatus(int)` | Override status code |
| `ResponseHeader(key) string` | Get response header |
| `AddResponseHeader(key, value)` | Add response header |
| `SetResponseHeader(key, value)` | Set response header |
| `DelResponseHeader(key)` | Delete response header |
| `ResponseBody() []byte` | Response body |
| `SetResponseBody([]byte)` | Override response body |

**Context & Metadata:**

| Method | Description |
|--------|-------------|
| `Raw() *app.RequestContext` | Underlying Hertz request context |
| `Next(context.Context)` | Call next handler in chain |
| `Abort()` | Stop handler chain, return immediately |
| `RouteID() string` | Matched route ID |
| `ServiceID() string` | Associated service ID |
| `UpstreamID() string` | Associated upstream ID |
| `EndpointAddress() string` | Selected upstream endpoint |
| `RequestID() string` | Request ID (set by request_id plugin) |
| `ProxyInfo() ProxyInfo` | Proxy timing info (TotalTime, etc.) |
| `UpstreamStatusCode() int` | Upstream response status |
| `GatewayError() error` | Gateway-level error, if any |
| `Value(key) (any, bool)` | Get custom context value |
| `SetValue(key, any)` | Set custom context value |
| `RegexCaptures() []string` | Regex path captures ($1, $2, etc.) |

### Template Variables

Many plugins support template variables using `$variable` syntax (resolved via `renderRequestTemplate`). Variables that don't exist resolve to an empty string.

**Request Phase:**

| Variable | Description |
|----------|-------------|
| `$host` | Request Host header |
| `$uri` | Request path |
| `$request_uri` | Full URI (path + query) |
| `$remote_addr` | Client IP |
| `$request_id` | Request ID |
| `$request_method` | HTTP method |
| `$request_length` | Request body length (bytes) |
| `$route_id` | Matched route ID |
| `$service_id` | Service ID |
| `$upstream_id` | Upstream ID |
| `$upstream_host` | Upstream host |
| `$endpoint_addr` / `$upstream_addr` | Selected endpoint address |
| `$arg_NAME` | Query parameter by name |
| `$http_NAME` | Request header by name (underscores become hyphens) |
| `$1`, `$2`, ... | Regex path captures |

**Response Phase (after `pc.Next`):**

| Variable | Description |
|----------|-------------|
| `$status` | Response status code |
| `$body_bytes_sent` | Response body length (bytes) |
| `$request_time` | Total request time (seconds, 3 decimals) |
| `$upstream_status` | Upstream response status code |
| `$upstream_response_time` | Upstream response time (seconds) |
| `$time_local` | Local timestamp (02/Jan/2006:15:04:05 -0700) |
| `$server_port` | Server port (parsed from Host) |

### Built-in Plugins

| Plugin | Priority | Description |
|--------|----------|-------------|
| `request_id` | 1200 | Generate/propagate request IDs (uuid, nanoid, range_id) |
| `limit_count` | 1100 | Rate limiting (fixed window, per-key) |
| `request_transformer` | 100 | Modify request method, host, headers, query params |
| `rewrite_path_regex` | 0 | Regex-based path rewriting with captures |
| `replace_path` | 0 | Replace request path (supports template variables) |
| `strip_prefix` | 0 | Strip URL path prefix |
| `add_prefix` | 0 | Add URL path prefix |
| `response_transformer` | 0 | Modify response status, headers, body |
| `access_log` | -100 | Buffered access log with template variables |

#### request_id

```yaml
- name: request_id
  params:
    header_name: X-Request-Id       # default: X-Request-Id
    include_in_response: true       # default: true
    algorithm: uuid                 # uuid | nanoid | range_id
    range_id:                       # only for algorithm=range_id
      char_set: "abc...XYZ0-9"
      length: 16
```

#### limit_count

```yaml
- name: limit_count
  params:
    count: 100                      # max requests per window
    time_window: 60                 # window size in seconds
    key_type: var                   # var | var_combination | constant
    key: remote_addr                # variable or template string
    rejected_code: 503              # status code when limited
    rejected_msg: "rate limited"    # response body when limited
    policy: local                   # only "local" supported
    show_limit_quota_header: true   # X-RateLimit-* headers
    group: ""                       # shared counter group (default: route ID)
```

#### request_transformer

```yaml
- name: request_transformer
  params:
    method: POST                    # override HTTP method
    host: "internal.svc"            # override Host header
    add:
      headers:
        X-Custom: "$request_id"     # supports template variables
      query:
        source: gateway
    set:
      headers:
        Content-Type: application/json
      query:
        version: v2
    remove:
      headers: ["X-Debug"]
      query: ["debug"]
```

#### rewrite_path_regex

```yaml
- name: rewrite_path_regex
  params:
    rules:
      - pattern: "^/api/v1/(.*)"
        replacement: "/internal/$1"
```

#### strip_prefix

```yaml
- name: strip_prefix
  params:
    prefix: "/api"                  # single prefix
    # or
    prefixes: ["/api", "/v1"]       # multiple prefixes
```

#### access_log

```yaml
- name: access_log
  params:
    path: "logs/access.log"
    format: '$remote_addr - [$time_local] "$request_method $request_uri" $status $body_bytes_sent $request_time'
    buffer_size: 16384              # bytes, default 16KB
    flush_interval: "1s"            # default 1s
```

---

## Admin API

The Admin API provides APISIX-compatible REST endpoints for managing gateway resources dynamically. It is only available when `gateway.source = etcd_apisix`.

### Starting the Admin API

1. Configure bootstrap with etcd source:

```yaml
gateway:
  listen: ":18080"
  source: etcd_apisix

etcd:
  endpoints:
    - "127.0.0.1:2379"
  prefix: "/apisix"

admin:
  key: "your-secret-key"
```

2. Start the gateway:

```bash
./lumen-gateway --config configs/bootstrap.yaml
```

The Admin API is served on the same port as the gateway, under the `/apisix/admin/` path prefix. All requests require the `X-API-KEY` header.

### Authentication

Every Admin API request must include:

```
X-API-KEY: your-secret-key
```

### CRUD Endpoints

Base path: `/apisix/admin/`

| Method | Path | Description |
|--------|------|-------------|
| GET | `/{resource}` | List resources (paginated) |
| GET | `/{resource}/{id}` | Get a single resource |
| POST | `/{resource}` | Create resource (ID auto-generated or from body) |
| PUT | `/{resource}/{id}` | Create or update resource |
| PATCH | `/{resource}/{id}` | Partial update resource |
| DELETE | `/{resource}/{id}` | Delete resource |

Supported resources: `routes`, `services`, `upstreams`, `plugin_configs`, `global_rules`

#### List Query Parameters

| Param | Description |
|-------|-------------|
| `page` | Page number (default: 1) |
| `page_size` | Items per page (default: 50, max: 200) |
| `keyword` | Search filter (matches ID, key, or value) |

#### Examples

```bash
# List all routes
curl http://localhost:18080/apisix/admin/routes \
  -H "X-API-KEY: your-secret-key"

# Create a route
curl -X PUT http://localhost:18080/apisix/admin/routes/my-route \
  -H "X-API-KEY: your-secret-key" \
  -H "Content-Type: application/json" \
  -d '{
    "uri": "/api/v1/users",
    "methods": ["GET"],
    "upstream": {
      "type": "roundrobin",
      "nodes": {"127.0.0.1:8080": 1}
    }
  }'

# Delete a route
curl -X DELETE http://localhost:18080/apisix/admin/routes/my-route \
  -H "X-API-KEY: your-secret-key"
```

### Control Plane Endpoints

Base path: `/apisix/admin/control/`

| Method | Path | Description |
|--------|------|-------------|
| GET | `/schema` | Get resource JSON schemas |
| GET | `/plugins` | List registered plugins with scopes |
| GET | `/stats` | Get gateway runtime statistics |
| POST | `/validate` | Validate a resource or bundle |
| POST | `/imports/preview` | Preview bundle import (dry run) |
| POST | `/imports/apply` | Apply bundle import |
| GET | `/exports` | Export all resources as bundle |
| GET | `/history` | List configuration history snapshots |
| POST | `/history/{id}/rollback` | Rollback to a history snapshot |

#### Bundle Import/Export

Export current configuration:

```bash
curl http://localhost:18080/apisix/admin/control/exports \
  -H "X-API-KEY: your-secret-key"

# YAML format
curl "http://localhost:18080/apisix/admin/control/exports?format=yaml" \
  -H "X-API-KEY: your-secret-key"

# Filter by kind
curl "http://localhost:18080/apisix/admin/control/exports?kind=routes&kind=upstreams" \
  -H "X-API-KEY: your-secret-key"
```

Preview an import (dry run):

```bash
curl -X POST http://localhost:18080/apisix/admin/control/imports/preview \
  -H "X-API-KEY: your-secret-key" \
  -H "Content-Type: application/json" \
  -d '{"content": "routes:\n  my-route:\n    uri: /test\n    upstream_id: my-upstream"}'
```

Apply an import:

```bash
curl -X POST http://localhost:18080/apisix/admin/control/imports/apply \
  -H "X-API-KEY: your-secret-key" \
  -H "Content-Type: application/json" \
  -d '{"content": "...", "prune": true}'
```

#### Validate

Validate a single resource:

```bash
curl -X POST http://localhost:18080/apisix/admin/control/validate \
  -H "X-API-KEY: your-secret-key" \
  -H "Content-Type: application/json" \
  -d '{"kind": "routes", "id": "test", "resource": {"uri": "/test"}}'
```

#### History & Rollback

```bash
# List history
curl http://localhost:18080/apisix/admin/control/history \
  -H "X-API-KEY: your-secret-key"

# Rollback to a snapshot
curl -X POST http://localhost:18080/apisix/admin/control/history/{snapshot-id}/rollback \
  -H "X-API-KEY: your-secret-key"
```

### CLI Commands

lumen-gateway provides CLI commands for bulk operations:

```bash
# Import a bundle file into etcd
./lumen-gateway admin import -f bundle.yaml --prune

# Export current config from etcd
./lumen-gateway admin export -o backup.yaml

# Sync a bundle file (one-shot)
./lumen-gateway admin sync -f bundle.yaml --prune

# Sync with file watching (continuous)
./lumen-gateway admin sync -f bundle.yaml --watch --interval 1s --prune
```

The `--prune` flag deletes resources in etcd that are missing from the bundle. Use `--prune-kind` to restrict pruning to specific resource types:

```bash
./lumen-gateway admin import -f bundle.yaml --prune --prune-kind routes --prune-kind upstreams
```

---

## Routing

### Path Matching

lumen-gateway supports four path matching modes, evaluated in priority order:

| Syntax | Type | Score | Example |
|--------|------|-------|---------|
| `= /path` | Exact match | 300,000 + length | `= /health` |
| `~ pattern` | Regex match | 200,000 + length | `~ ^/api/v[0-9]+` |
| `~* pattern` | Case-insensitive regex | 200,000 + length | `~* ^/API` |
| `/path` | Prefix match | 100,000 + length | `/api/v1` |
| `/path/*` | Explicit prefix wildcard | 100,000 + length | `/api/*` |

### Route Priority

When multiple routes match a request, the winner is determined by:

1. `route.priority` field (higher wins, multiplied by 1,000,000)
2. Path match type (exact > regex > prefix)
3. Path specificity (longer patterns score higher)

### Host Matching

Routes can filter by Host header with wildcard support:

```yaml
routes:
  wildcard-route:
    hosts:
      - "api.example.com"       # exact
      - "*.example.com"         # suffix wildcard
      - "api.*"                 # prefix wildcard
    paths: ["/"]
    service: my-service
```

### Custom Load Balancers

Register custom balancer types via the Go API:

```go
lumen.Run(
    lumen.WithBalancerType("least_conn", func(
        endpoints []balancer.Endpoint,
        params any,
    ) (balancer.Balancer, error) {
        return NewLeastConnBalancer(endpoints, params)
    }),
)
```

Then reference in config:

```yaml
upstreams:
  my-upstream:
    balancer:
      type: least_conn
      params:
        some_option: true
    endpoints:
      - address: "127.0.0.1:8080"
```
