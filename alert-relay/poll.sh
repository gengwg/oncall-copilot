#!/usr/bin/env bash
# Poll the host alert-relay from inside the sandbox and print any new alerts
# as JSONL. Designed to be supervised by an OpenClaw stream automation, which
# fires an agent turn whenever this script emits a line.
#
# Alertmanager (k8s) POSTs firing/resolved alerts to the host relay;
# this script drains them via GET /alerts.
set -uo pipefail

RELAY="${ALERT_RELAY_URL:-http://172.18.0.1:9099}"
INTERVAL="${POLL_INTERVAL:-10}"

while true; do
  batch=$(curl -sf -m 5 "$RELAY/alerts" 2>/dev/null || echo "[]")
  n=$(printf '%s' "$batch" | jq 'length' 2>/dev/null || echo 0)
  if [ "$n" != "0" ] && [ -n "$n" ]; then
    printf '%s\n' "$batch" | jq -c '.[]' 2>/dev/null
  fi
  sleep "$INTERVAL"
done
