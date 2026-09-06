#!/usr/bin/env bash
# Read-only PromQL helper. Usage:
#   query.sh instant '<promql>'
#   query.sh range '<promql>' <minutes> <step_seconds>
#   query.sh labels <label_name>
set -euo pipefail

: "${PROMETHEUS_URL:?set PROMETHEUS_URL, e.g. http://localhost:9090}"
BASE="${PROMETHEUS_URL%/}"

cmd="${1:?instant|range|labels}"
case "$cmd" in
  instant)
    q="${2:?promql required}"
    curl -sfG "$BASE/api/v1/query" --data-urlencode "query=$q" \
      | jq -c '.data.result[] | {metric: .metric, value: .value[1]}'
    ;;
  range)
    q="${2:?promql required}"
    minutes="${3:-30}"
    step="${4:-15}"
    end=$(date +%s)
    start=$((end - minutes * 60))
    curl -sfG "$BASE/api/v1/query_range" \
      --data-urlencode "query=$q" \
      --data-urlencode "start=$start" \
      --data-urlencode "end=$end" \
      --data-urlencode "step=$step" \
      | jq -c '.data.result[] | {metric: .metric, points: [.values[] | [.[0], .[1]]]}'
    ;;
  labels)
    label="${3:-}"
    if [ -n "$label" ]; then
      curl -sfG "$BASE/api/v1/label/$label/values" | jq -c '.data'
    else
      curl -sfG "$BASE/api/v1/labels" | jq -c '.data'
    fi
    ;;
  *)
    echo "unknown command: $cmd" >&2; exit 2
    ;;
esac
