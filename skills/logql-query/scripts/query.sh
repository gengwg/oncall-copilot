#!/usr/bin/env bash
# Read-only LogQL helper. Usage:
#   query.sh tail '<logql>' <minutes> <limit>
#   query.sh count '<metric_logql>' <minutes>
set -euo pipefail

: "${LOKI_URL:?set LOKI_URL, e.g. http://localhost:3100}"
BASE="${LOKI_URL%/}"

cmd="${1:?tail|count}"
now_ns=$(($(date +%s) * 1000000000))

case "$cmd" in
  tail)
    q="${2:?logql required}"
    minutes="${3:-30}"
    limit="${4:-50}"
    start_ns=$((now_ns - minutes * 60 * 1000000000))
    curl -sfG "$BASE/loki/api/v1/query_range" \
      --data-urlencode "query=$q" \
      --data-urlencode "start=$start_ns" \
      --data-urlencode "end=$now_ns" \
      --data-urlencode "limit=$limit" \
      --data-urlencode "direction=backward" \
      | jq -r '.data.result[].values[] | [(.[0] | tonumber / 1e9 | todate), .[1]] | @tsv' \
      | head -n "$limit"
    ;;
  count)
    q="${2:?metric logql required}"
    minutes="${3:-30}"
    start_ns=$((now_ns - minutes * 60 * 1000000000))
    curl -sfG "$BASE/loki/api/v1/query_range" \
      --data-urlencode "query=$q" \
      --data-urlencode "start=$start_ns" \
      --data-urlencode "end=$now_ns" \
      --data-urlencode "step=60" \
      | jq -c '.data.result[] | {metric: .metric, points: [.values[] | [.[0], .[1]]]}'
    ;;
  *)
    echo "unknown command: $cmd" >&2; exit 2
    ;;
esac
