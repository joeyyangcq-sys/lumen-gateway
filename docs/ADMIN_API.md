# Lumen Gateway Admin API

## 1. Overview

`lumen-gateway` exposes two control-plane API groups:

- APISIX-style single-resource CRUD:
  - `/apisix/admin/routes`
  - `/apisix/admin/services`
  - `/apisix/admin/upstreams`
  - `/apisix/admin/plugin_configs`
  - `/apisix/admin/global_rules`
- UI-oriented bundle/control APIs:
  - `/apisix/admin/control/schema`
  - `/apisix/admin/control/validate`
  - `/apisix/admin/control/imports/preview`
  - `/apisix/admin/control/imports/apply`
  - `/apisix/admin/control/exports`
  - `/apisix/admin/control/history`
  - `/apisix/admin/control/history/{id}/rollback`

All Admin API requests require:

- Header: `X-API-KEY: <admin key>`

Default local development key:

- `local-dev-admin-key`

## 2. Authentication

### Request

```http
X-API-KEY: local-dev-admin-key
```

### Unauthorized response

```json
{
  "error_msg": "missing or invalid X-API-KEY"
}
```

Status:

- `401 Unauthorized`

## 3. Resource CRUD APIs

Supported resource kinds:

- `routes`
- `services`
- `upstreams`
- `plugin_configs`
- `global_rules`

### 3.1 List resources

```http
GET /apisix/admin/routes
```

Optional query parameters:

- `page` — 1-based page number, default `1`
- `page_size` — items per page, default `50`, max `200`
- `keyword` — case-insensitive substring match against the resource key, id, and stored JSON body

Example:

```http
GET /apisix/admin/routes?page=1&page_size=20&keyword=users
```

Response:

```json
{
  "list": [
    {
      "key": "/apisix/routes/1001",
      "value": {
        "id": "1001",
        "uri": "/demo",
        "upstream_id": "2001"
      },
      "createdIndex": 12,
      "modifiedIndex": 15
    }
  ],
  "total": 1,
  "page": 1,
  "page_size": 20,
  "keyword": "users"
}
```

### 3.2 Get one resource

```http
GET /apisix/admin/routes/1001
```

Response:

```json
{
  "key": "/apisix/routes/1001",
  "value": {
    "id": "1001",
    "uri": "/demo",
    "upstream_id": "2001",
    "create_time": 1710000000,
    "update_time": 1710000001
  },
  "createdIndex": 12,
  "modifiedIndex": 15
}
```

### 3.3 Create with POST

```http
POST /apisix/admin/routes
Content-Type: application/json
```

```json
{
  "id": "1001",
  "uri": "/demo",
  "upstream_id": "2001"
}
```

Response:

- `200 OK`

Body:

```json
{
  "key": "/apisix/routes/1001",
  "value": {
    "id": "1001",
    "uri": "/demo",
    "upstream_id": "2001",
    "create_time": 1710000000,
    "update_time": 1710000000
  },
  "createdIndex": 12,
  "modifiedIndex": 12
}
```

### 3.4 Upsert with PUT

```http
PUT /apisix/admin/routes/1001
Content-Type: application/json
```

```json
{
  "uri": "/demo-v2",
  "upstream_id": "2001"
}
```

Responses:

- `201 Created` when the resource did not exist
- `200 OK` when the resource already existed

### 3.5 Partial update with PATCH

```http
PATCH /apisix/admin/routes/1001
Content-Type: application/json
```

```json
{
  "plugins": {
    "request-id": {
      "header_name": "X-Request-Id"
    }
  }
}
```

Response:

- `200 OK`

`PATCH` merges the incoming object into the current stored JSON body before normalization.

### 3.6 Delete

```http
DELETE /apisix/admin/routes/1001
```

Response:

```json
{
  "key": "/apisix/routes/1001",
  "deleted": "1"
}
```

## 4. Bundle preview/apply/export APIs

These APIs are intended for Web UI, file import workflows, MCP tools, and future GitOps-style integrations.

### 4.0 Fetch control schema / capabilities

```http
GET /apisix/admin/control/schema
```

Response:

```json
{
  "resources": [
    {
      "kind": "routes",
      "label": "Route",
      "description": "Matches incoming traffic and attaches a service or upstream plus plugins.",
      "methods": ["GET", "POST", "PUT", "PATCH", "DELETE"],
      "key_fields": [
        {
          "name": "uri",
          "type": "string",
          "required": false,
          "description": "Single APISIX URI matcher."
        },
        {
          "name": "service_id",
          "type": "string",
          "required": false,
          "description": "References a service resource."
        }
      ]
    }
  ],
  "plugins": [
    {
      "name": "proxy-rewrite",
      "label": "Proxy Rewrite",
      "scopes": ["route", "service", "plugin_config", "global_rule"],
      "translated_to": ["rewrite_path_regex", "request_transformer", "replace_path"]
    },
    {
      "name": "request-id",
      "label": "Request ID",
      "scopes": ["route", "service", "plugin_config", "global_rule"],
      "translated_to": ["request_id"]
    }
  ],
  "capabilities": {
    "bundle_formats": ["json", "yaml"],
    "export_formats": ["json", "yaml"],
    "history_limit": 10,
    "supports": {
      "validate": true,
      "preview": true,
      "apply": true,
      "history": true,
      "rollback": true
    },
    "preview_actions": ["create", "update", "delete", "unchanged"]
  }
}
```

Notes:

- This endpoint is intended as a stable capability source for Web UI.
- It is intentionally smaller than a full OpenAPI schema; the goal is to reduce UI hard-coding for supported resources and plugins.

### 4.1 Validate resource or bundle

```http
POST /apisix/admin/control/validate
Content-Type: application/json
```

Single-resource request:

```json
{
  "kind": "routes",
  "id": "route-1",
  "resource": {
    "uri": "/demo",
    "service_id": "svc-1"
  }
}
```

Bundle request:

```json
{
  "bundle": {
    "routes": {
      "route-1": {
        "id": "route-1",
        "uri": "/demo",
        "service_id": "svc-1"
      }
    }
  },
  "prune": true,
  "prune_kinds": ["routes"]
}
```

Response:

```json
{
  "valid": false,
  "issues": [
    {
      "resource": "routes",
      "resource_id": "route-1",
      "field": "service_id",
      "message": "references unknown service \"svc-1\""
    }
  ]
}
```

Notes:

- syntactically valid requests always return `200`
- semantic problems are reported in `issues`
- malformed requests use the unified control error model

### 4.1 Bundle request body

```json
{
  "bundle": {
    "_meta": {
      "format": "lumen.apisix.bundle/v1",
      "managed_kinds": ["routes", "services", "upstreams"]
    },
    "routes": {
      "1001": {
        "id": "1001",
        "uri": "/demo",
        "upstream_id": "2001"
      }
    }
  },
  "prune": true,
  "prune_kinds": ["routes"],
  "include_unchanged": false
}
```

Notes:

- `bundle` may also be passed as raw YAML/JSON text using `content`
- `prune` controls deletion of resources missing from the bundle
- `prune_kinds` narrows pruning to selected kinds
- `include_unchanged` only affects preview output

### 4.2 Preview import

```http
POST /apisix/admin/control/imports/preview
Content-Type: application/json
```

Response:

```json
{
  "summary": [
    {
      "kind": "routes",
      "create": 1,
      "update": 0,
      "delete": 0,
      "unchanged": 0
    }
  ],
  "changes": [
    {
      "kind": "routes",
      "id": "1001",
      "action": "create",
      "title": "/demo",
      "summary": {
        "id": "1001",
        "uri": "/demo",
        "service_id": "2001"
      },
      "after": {
        "id": "1001",
        "uri": "/demo",
        "upstream_id": "2001"
      },
      "managed": true
    }
  ]
}
```

Change actions:

- `create`
- `update`
- `delete`
- `unchanged`

Preview change metadata:

- `title`: short human-readable label for the UI
- `summary`: key fields for list rendering without parsing the full body
- `warnings`: destructive or risky behavior hints
- `prune_source`: why a delete is happening during prune

Delete previews may also include:

- `prune_source = explicit_prune_kinds`
- `prune_source = managed_kinds`
- `prune_source = bundle_omitted`

### 4.3 Apply import

```http
POST /apisix/admin/control/imports/apply
Content-Type: application/json
```

Response:

```json
{
  "result": {
    "counts": {
      "routes": 1,
      "services": 1,
      "upstreams": 1
    }
  },
  "history": {
    "id": "01HX...",
    "created_at": "2026-05-10T03:00:00Z",
    "source": "control_import_apply",
    "summary": {
      "counts": {
        "routes": 1,
        "services": 1,
        "upstreams": 1
      }
    }
  },
  "operation": {
    "operation_id": "01HX...",
    "created_at": "2026-05-10T03:00:00Z",
    "source": "control_import_apply",
    "summary": {
      "counts": {
        "routes": 1,
        "services": 1,
        "upstreams": 1
      }
    }
  }
}
```

Behavior:

- writes normalized resources into etcd
- optionally prunes missing resources
- saves a history snapshot after successful apply

### 4.4 Export bundle

```http
GET /apisix/admin/control/exports?kind=routes&kind=services&format=json
```

Formats:

- `format=json` returns bundle JSON
- `format=yaml` returns:

```json
{
  "format": "yaml",
  "content": "_meta:\n  format: lumen.apisix.bundle/v1\n..."
}
```

Bundle meta fields:

- `_meta.format`
- `_meta.exported_at`
- `_meta.etcd_prefix`
- `_meta.managed_kinds`

## 5. History and rollback APIs

History is enabled by default for the latest `10` successful apply snapshots.

### 5.1 List history

```http
GET /apisix/admin/control/history?limit=10
```

Response:

```json
{
  "list": [
    {
      "id": "01HX...",
      "created_at": "2026-05-10T03:00:00Z",
      "source": "control_import_apply",
      "summary": {
        "counts": {
          "routes": 1,
          "services": 1,
          "upstreams": 1
        },
        "managed_kinds": ["routes", "services", "upstreams"]
      },
      "bundle": {
        "_meta": {
          "format": "lumen.apisix.bundle/v1"
        }
      }
    }
  ],
  "total": 1
}
```

### 5.2 Roll back one version

```http
POST /apisix/admin/control/history/01HX.../rollback
```

Response:

```json
{
  "result": {
    "counts": {
      "routes": 1,
      "services": 1,
      "upstreams": 1
    }
  },
  "history": {
    "id": "01HY...",
    "created_at": "2026-05-10T03:10:00Z",
    "source": "history_rollback",
    "rollback_of": "01HX..."
  },
  "operation": {
    "operation_id": "01HY...",
    "created_at": "2026-05-10T03:10:00Z",
    "source": "history_rollback",
    "rollback_of": "01HX..."
  }
}
```

Behavior:

- re-applies the saved bundle
- prunes within the bundle's managed kinds
- creates a new history snapshot for the rollback action

## 6. Error body conventions

Current behavior is intentionally close to APISIX-style Admin API responses.

### Not found

Status:

- `404 Not Found`

Body:

```json
{
  "message": "Key not found"
}
```

### Validation / request errors

Status examples:

- `400 Bad Request`
- `405 Method Not Allowed`

Body:

```json
{
  "error_msg": "resource id is required"
}
```

### Control API errors

The newer `/apisix/admin/control/*` endpoints use a UI-friendly error model:

```json
{
  "code": "invalid_request",
  "message": "unsupported resource kind"
}
```

Common codes:

- `unauthorized`
- `invalid_request`
- `not_found`
- `controlplane_error`

### Unsupported path

```json
{
  "error_msg": "unsupported admin path"
}
```

## 7. Current gaps vs full APISIX compatibility

The API is intentionally close to APISIX, but not yet a perfect drop-in replacement.

Known gaps:

- response envelope is APISIX-like, not byte-for-byte identical
- no pagination for large list endpoints yet
- no per-resource optimistic concurrency fields exposed in write APIs
- no RBAC / OAuth layer yet
- no OpenAPI spec generated yet

## 8. Suggested next backend/API steps

- add generated OpenAPI or checked-in static schema
- add pagination/filtering for list endpoints
- add audit trail fields for UI and MCP usage
- add resource-level validation detail payloads for form UIs
