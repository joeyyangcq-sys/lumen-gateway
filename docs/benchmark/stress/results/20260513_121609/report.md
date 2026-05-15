# Lumen Gateway — Scale Stress Test Report

**Date:** 2026-05-13 13:04:47
**VUs:** 100  **Duration:** 30s

## Proxy Latency vs Route Count

| Routes | Seed Time | RPS | p50 (ms) | p95 (ms) | p99 (ms) | Error Rate | Memory |
|--------|-----------|-----|----------|----------|----------|------------|--------|
| 1000 | 31s | 0.0 | 0.00 | 0.00 | 0.00 | 0.00% | 1.534GiB / 7.654GiB |
| 3000 | 271s | 0.0 | 0.00 | 0.00 | 0.00 | 0.00% | 1.938GiB / 7.654GiB |
| 5000 | 636s | 0.0 | 0.00 | 0.00 | 0.00 | 0.00% | 2.086GiB / 7.654GiB |
| 10000 | 981s | 0.0 | 0.00 | 0.00 | 0.00 | 0.00% | 4.612GiB / 7.654GiB |

## Admin API Throughput

| Metric | Value |
|--------|-------|
| RPS (total HTTP) | 0.0 |
| Create p50 | 0.00 ms |
| Create p99 | 0.00 ms |

---
*Raw data: `/Users/joey/api-gateway/lumen-gateway/docs/benchmark/stress/results/20260513_121609/`*
