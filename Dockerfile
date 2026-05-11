# ── Stage 1: build ──────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

# 国内网络使用 goproxy.cn 加速，-mod=mod 允许在没有网络时降级
ENV GOPROXY=https://goproxy.cn,direct
ENV GONOSUMDB=*
ENV CGO_ENABLED=0
ENV GOOS=linux

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -buildvcs=false -ldflags="-s -w" -o /lumen-gateway ./cmd/lumen-gateway

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
