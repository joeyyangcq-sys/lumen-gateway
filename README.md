# Lumen Gateway

Lumen Gateway is a layer 7 API gateway built on Hertz.

The project starts with a small gateway core inspired by Bifrost:

- Route, service, and upstream config layers.
- Hertz-compatible plugin registry.
- Pluggable load balancer interface.
- Runtime snapshot model for future hot reload.
- Health check and observability package boundaries.

## Development

```bash
go run ./cmd/lumen-gateway -config configs/lumen.yaml
```

```bash
go run ./cmd/lumen-gateway -config configs/lumen.yaml -test
```

