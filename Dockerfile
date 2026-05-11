# ── Stage 1: build ──────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /lumen-gateway ./cmd/lumen-gateway

# ── Stage 2: runtime ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S lumen && adduser -S lumen -G lumen

WORKDIR /app
COPY --from=builder /lumen-gateway .
COPY configs/ configs/

USER lumen

EXPOSE 18080
ENTRYPOINT ["./lumen-gateway"]
CMD ["--config", "configs/bootstrap.yaml"]
