#!/usr/bin/env bash
# Deterministic incident investigator. Runs as an OpenClaw command payload.
#   1. Runs real PromQL/LogQL against the demo stack
#   2. Asks Nemotron (via Token Factory) to analyze the evidence
#   3. Writes an incident report to memory/incidents/
#   4. Prints a Telegram brief on stdout (delivered by the cron announce)
#
# Usage: investigate.sh '<alert-json-line>'   (or no arg to drain the relay queue)
set -uo pipefail

RELAY="${ALERT_RELAY_URL:-http://172.18.0.1:9099}"
# No arg: drain the relay queue and take the first firing alert.
if [ $# -eq 0 ]; then
  batch=$(curl -sf -m 8 "$RELAY/alerts" 2>/dev/null || echo "[]")
  alert_json=$(printf '%s' "$batch" | jq -c '[.[] | select(.status=="firing")][0] // empty' 2>/dev/null)
  [ -z "$alert_json" ] && { echo "NO_REPLY"; exit 0; }
  set -- "$alert_json"
fi

WORKSPACE=/sandbox/.openclaw/workspace
export PROMETHEUS_URL="${PROMETHEUS_URL:-http://172.18.0.1:19090}"
export LOKI_URL="${LOKI_URL:-http://172.18.0.1:13100}"
PROM="$WORKSPACE/skills/promql-query/scripts/query.sh"
LOGQ="$WORKSPACE/skills/logql-query/scripts/query.sh"
INCIDENTS="$WORKSPACE/memory/incidents"
RUNBOOKS="$WORKSPACE/memory/runbooks"
MODEL="${MODEL:-nvidia/Nemotron-3-Ultra-550b-a55b}"
# Token Factory is reached via the sandbox's managed inference route
# (direct api.tokenfactory.nebius.com egress is policy-blocked).
INFERENCE_URL="${INFERENCE_URL:-https://inference.local/v1/chat/completions}"

alert_json="$1"
alertname=$(printf '%s' "$alert_json" | jq -r '.alertname // "Unknown"')
service=$(printf '%s' "$alert_json" | jq -r '.service // "demo-app"')
severity=$(printf '%s' "$alert_json" | jq -r '.severity // "warning"')
summary=$(printf '%s' "$alert_json" | jq -r '.summary // ""')
status=$(printf '%s' "$alert_json" | jq -r '.status // "firing"')
date=$(date -u +%Y-%m-%d)
report="$INCIDENTS/$date-$alertname.md"
mkdir -p "$INCIDENTS"

[ "$status" != "firing" ] && { echo "NO_REPLY"; exit 0; }

runbook=$(cat "$RUNBOOKS/$service.md" 2>/dev/null || echo "no runbook")

# --- collect evidence (read-only, bounded) ---
err_ratio=$($PROM instant 'sum(rate(demo_errors_total[5m])) / sum(rate(demo_requests_total[5m]))' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
heap=$($PROM instant 'demo_heap_alloc_bytes' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
lat=$($PROM instant 'demo_latency_mode' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
err_logs=$($LOGQ tail "{service=\"$service\"} |= \"ERROR\"" 15 3 2>/dev/null | head -3)

# --- Nemotron analysis ---
prompt="You are an SRE. Given this alert and live evidence, write a 2-sentence root-cause assessment and 1 suggested first action. Be specific; use the numbers.

ALERT: $alertname ($severity) on $service — $summary

EVIDENCE:
- error_ratio_5m: $err_ratio
- heap_alloc_bytes: $heap
- latency_mode: $lat
- recent ERROR logs:
$err_logs

RUNBOOK EXCERPT:
$(printf '%s' "$runbook" | head -20)

Reply format (plain text):
ROOT CAUSE: <2 sentences>
ACTION: <1 sentence>"

analysis=$(jq -n --arg m "$MODEL" --arg p "$prompt" \
  '{model:$m, messages:[{role:"user",content:$p}], max_tokens:600}' \
  | curl -sf -m 150 "$INFERENCE_URL" \
      -H "Authorization: Bearer $NEBIUS_API_KEY" -H "Content-Type: application/json" \
      -d @- 2>/dev/null | jq -r '.choices[0].message.content // "analysis unavailable"')

rootcause=$(printf '%s' "$analysis" | sed -n 's/^ROOT CAUSE: *//p' | head -1)
action=$(printf '%s' "$analysis" | sed -n 's/^ACTION: *//p' | head -1)
[ -z "$rootcause" ] && rootcause="$analysis"
[ -z "$action" ] && action="See runbook."

# --- write incident report ---
cat > "$report" <<EOF
# $alertname — $date

- **Service**: $service
- **Severity**: $severity
- **Status**: firing
- **Summary**: $summary

## Root cause
$rootcause

## Evidence
- error_ratio_5m: $err_ratio
- heap_alloc_bytes: $heap
- latency_mode: $lat
- recent ERROR logs:
\`\`\`
$err_logs
\`\`\`

## Suggested first action
$action

## Full Nemotron analysis
$analysis
EOF

# --- Telegram brief on stdout (cron announce delivers it) ---
printf 'ALERT: %s (%s)\nService: %s\nLikely cause: %s\nEvidence: err_ratio=%s heap=%s\nSuggested: %s\n' \
  "$alertname" "$severity" "$service" "$(printf '%s' "$rootcause" | head -c 200)" "$err_ratio" "$heap" "$(printf '%s' "$action" | head -c 120)"
