#!/usr/bin/env bash
set -euo pipefail

GATEWAY="${1:?Usage: collect-resources.sh <lumen|apisix> <output.csv>}"
OUTPUT="${2:?Usage: collect-resources.sh <lumen|apisix> <output.csv>}"

echo "timestamp,cpu_percent,mem_mb" > "$OUTPUT"

while true; do
    ts=$(date +%s)
    cpu="0"
    mem="0"

    if [ "$GATEWAY" = "apisix" ]; then
        stats=$(docker stats apisix-local --no-stream --format '{{.CPUPerc}},{{.MemUsage}}' 2>/dev/null || echo "0%,0MiB")
        cpu=$(echo "$stats" | head -1 | cut -d',' -f1 | tr -d '%')
        raw_mem=$(echo "$stats" | head -1 | cut -d',' -f2 | cut -d'/' -f1 | tr -d ' ')
        if echo "$raw_mem" | grep -qi 'gib'; then
            mem=$(echo "$raw_mem" | tr -d 'GiB' | awk '{printf "%.1f", $1 * 1024}')
        else
            mem=$(echo "$raw_mem" | tr -d 'MiB' | awk '{printf "%.1f", $1}')
        fi
    else
        pid=$(pgrep -f "lumen-gateway" 2>/dev/null | head -1 || true)
        if [ -n "$pid" ]; then
            cpu=$(ps -p "$pid" -o %cpu= 2>/dev/null | tr -d ' ' || echo "0")
            rss=$(ps -p "$pid" -o rss= 2>/dev/null | tr -d ' ' || echo "0")
            mem=$(awk "BEGIN {printf \"%.1f\", $rss / 1024}")
        fi
    fi

    echo "$ts,$cpu,$mem" >> "$OUTPUT"
    sleep 1
done
