# Lumen Gateway Usage Guide

This guide follows the same spirit as Bifrost's "get started" documentation:

- start with a small local setup
- show the minimum config you need
- keep examples close to real gateway behavior
- make examples executable through tests

Every example in this document is backed by a Go test in this repository.

## 1. Run modes

`lumen-gateway` supports two main control-plane modes:

- `file`
  - load native Lumen YAML from disk
  - best for local development and simple deployments
- `etcd_apisix`
  - load APISIX-compatible resources from etcd
  - best when you want APISIX-style Admin API, preview/apply/export, history, and rollback

Bootstrap config chooses the mode:

```yaml
gateway:
  listen: ":18080"
  source: file

file:
  path: configs/lumen.yaml

etcd:
  endpoints:
    - "127.0.0.1:2379"
  prefix: "/apisix"
  dial_timeout: 3s

admin:
  key: local-dev-admin-key
```

Default bootstrap file:

- `/Users/joey/api-gateway/lumen-gateway/configs/bootstrap.yaml`

Run locally:

```bash
cd /Users/joey/api-gateway/lumen-gateway
go run ./cmd/lumen-gateway --config configs/bootstrap.yaml
```

Validate bootstrap only:

```bash
go run ./cmd/lumen-gateway --config configs/bootstrap.yaml --test
```

## 2. Example A: file-mode reverse proxy

Goal:

- match `GET /api/users`
- strip `/api`
- proxy to `127.0.0.1:9001`
- rewrite upstream `Host`
- attach response headers from global and service plugins

Reference config fixture:

- `/Users/joey/api-gateway/lumen-gateway/internal/gateway/testdata/usage/quickstart.yaml`

Core shape:

```yaml
routes:
  users:
    methods: ["GET"]
    paths: ["/api/users"]
    service: users-service
    plugins:
      - name: strip_prefix
        params:
          prefix: "/api"

services:
  users-service:
    protocol: http
    upstream: users-upstream

upstreams:
  users-upstream:
    pass_host: rewrite
    upstream_host: users.internal
    endpoints:
      - address: "127.0.0.1:9001"
```

What this example demonstrates:

- route -> service -> upstream composition
- request rewrite before proxy
- `pass_host: rewrite`
- reusable plugin definitions
- response shaping after upstream call

Verified by:

- `/Users/joey/api-gateway/lumen-gateway/internal/gateway/usage_examples_test.go`
  - `TestUsageGuideFileModeQuickstart`

## 3. Example B: request ID + rate limiting

Goal:

- auto-generate request IDs when missing
- preserve incoming request IDs when present
- return the request ID in the response
- rate limit requests per route

Reference config fixture:

- `/Users/joey/api-gateway/lumen-gateway/internal/gateway/testdata/usage/request_id_limit.yaml`

Core shape:

```yaml
routes:
  limited-users:
    methods: ["GET"]
    paths: ["/limited/users"]
    service: users-service
    plugins:
      - name: request_id
        params:
          header_name: X-Request-Id
      - name: limit_count
        params:
          count: 2
          time_window: 60
          key_type: constant
          key: usage-doc
          rejected_code: 429
          rejected_msg: rate limited
```

What this example demonstrates:

- plugin execution on the request path
- generated vs forwarded request IDs
- route-scoped rate limiting
- gateway-generated `429` responses without hitting upstream

Verified by:

- `/Users/joey/api-gateway/lumen-gateway/internal/gateway/usage_examples_test.go`
  - `TestUsageGuideRequestIDAndLimitCount`

## 4. Example C: APISIX-style preview, apply, export, history, rollback

Goal:

- preview a bundle before writing
- apply a bundle through Admin API
- export current resources
- list history
- roll back to a previous snapshot

Reference bundle fixtures:

- `/Users/joey/api-gateway/lumen-gateway/internal/adminapi/testdata/usage/bundle_v1.yaml`
- `/Users/joey/api-gateway/lumen-gateway/internal/adminapi/testdata/usage/bundle_v2.yaml`

Preview request:

```http
POST /apisix/admin/control/imports/preview
X-API-KEY: local-dev-admin-key
Content-Type: application/json
```

```json
{
  "content": "_meta:\n  format: lumen.apisix.bundle/v1\nroutes:\n  demo-route:\n    id: demo-route\n    uri: /demo\n    service_id: demo-service\n...",
  "prune": true
}
```

Apply request:

```http
POST /apisix/admin/control/imports/apply
X-API-KEY: local-dev-admin-key
```

History list:

```http
GET /apisix/admin/control/history?limit=10
X-API-KEY: local-dev-admin-key
```

Rollback:

```http
POST /apisix/admin/control/history/{id}/rollback
X-API-KEY: local-dev-admin-key
```

What this example demonstrates:

- UI-friendly preview/apply workflow
- automatic history snapshots
- one-click rollback behavior at the API layer

Verified by:

- `/Users/joey/api-gateway/lumen-gateway/internal/adminapi/usage_examples_test.go`
  - `TestUsageGuideAdminControlWorkflow`

## 5. Operational notes

### File mode

Use file mode when:

- you are iterating locally
- you want simple checked-in YAML
- you do not need Admin API backed by etcd

### `etcd_apisix` mode

Use `etcd_apisix` mode when:

- you want APISIX-compatible resources
- you want Admin API preview/apply/export/history/rollback
- you plan to attach Web UI or MCP control-plane tools

### Metrics

Runtime metrics are exposed at:

- `GET /metrics`

Current metrics include:

- plugin execution counts and durations
- upstream request counts
- upstream phase timings
- upstream error type breakdown

## 6. Suggested starting path

If you are onboarding a new environment, the easiest order is:

1. start with file mode
2. verify one reverse-proxy route
3. add `request_id`
4. add `limit_count`
5. move to `etcd_apisix` mode when you need UI or Admin API workflows
