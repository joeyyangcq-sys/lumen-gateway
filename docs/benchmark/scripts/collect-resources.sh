#!/usr/bin/env bash
set -euo pipefail

GATEWAY="${1:?Usage: collect-resources.sh <lumen|apisix> <output.csv>}"
OUTPUT="${2:?Usage: collect-resources.sh <lumen|apisix> <output.csv>}"

if [ "$GATEWAY" = "lumen" ]; then
    CONTAINER="bench-lumen"
elif [ "$GATEWAY" = "apisix" ]; then
    CONTAINER="bench-apisix"
else
    echo "Unknown gateway: $GATEWAY" >&2
    exit 1
fi

echo "timestamp,cpu_percent,mem_usage,mem_percent" > "$OUTPUT"

while true; do
    ts=$(date +%s)
    stats=$(docker stats "$CONTAINER" --no-stream --format '{{.CPUPerc}},{{.MemUsage}},{{.MemPerc}}' 2>/dev/null || echo "0%,0MiB/0MiB,0%")
    cpu=$(echo "$stats" | head -1 | cut -d',' -f1 | tr -d '%')
    raw_mem=$(echo "$stats" | head -1 | cut -d',' -f2 | cut -d'/' -f1 | tr -d ' ')
    mem_pct=$(echo "$stats" | head -1 | cut -d',' -f3 | tr -d '%')

    if echo "$raw_mem" | grep -qi 'gib'; then
        mem=$(echo "$raw_mem" | tr -d 'GiB' | awk '{printf "%.1f", $1 * 1024}')
    else
        mem=$(echo "$raw_mem" | tr -d 'MiB' | awk '{printf "%.1f", $1}')
    fi

    echo "$ts,$cpu,${mem}MiB,$mem_pct" >> "$OUTPUT"
    sleep 1
done
