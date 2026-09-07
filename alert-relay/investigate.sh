#!/usr/bin/env bash
# Deterministic incident investigator. Runs as an OpenClaw command payload.
#   1. Drains the alert-relay queue, takes the first firing alert
#   2. Runs real PromQL/LogQL evidence collection (scenario-appropriate)
#   3. For unfamiliar error strings, enriches with a Tavily web search
#   4. Asks Nemotron (via the sandbox inference route) to analyze
#   5. Writes an incident report to memory/incidents/
#   6. Prints a Telegram brief on stdout (delivered by the cron announce)
#
# Usage: investigate.sh ['<alert-json-line>']
set -uo pipefail

WORKSPACE=/sandbox/.openclaw/workspace
export PROMETHEUS_URL="${PROMETHEUS_URL:-http://172.18.0.1:19090}"
export LOKI_URL="${LOKI_URL:-http://172.18.0.1:13100}"
RELAY="${ALERT_RELAY_URL:-http://172.18.0.1:9099}"
PROM="$WORKSPACE/skills/promql-query/scripts/query.sh"
LOGQ="$WORKSPACE/skills/logql-query/scripts/query.sh"
INCIDENTS="$WORKSPACE/memory/incidents"
RUNBOOKS="$WORKSPACE/memory/runbooks"
MODEL="${MODEL:-nvidia/Nemotron-3-Ultra-550b-a55b}"
INFERENCE_URL="${INFERENCE_URL:-https://inference.local/v1/chat/completions}"

if [ $# -eq 0 ]; then
  batch=$(curl -sf -m 8 "$RELAY/alerts" 2>/dev/null || echo "[]")
  alert_json=$(printf '%s' "$batch" | jq -c '[.[] | select(.status=="firing")][0] // empty' 2>/dev/null)
  [ -z "$alert_json" ] && { echo "NO_REPLY"; exit 0; }
else
  alert_json="$1"
fi

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

# --- evidence collection (scenario-appropriate, read-only, bounded) ---
err_ratio=$($PROM instant 'sum(rate(demo_errors_total[5m])) / sum(rate(demo_requests_total[5m]))' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
heap=$($PROM instant 'demo_heap_alloc_bytes' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
lat_mode=$($PROM instant 'demo_latency_mode' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
up=$($PROM instant "up{job=\"$service\"}" 2>/dev/null | jq -r '.value // "n/a"' | head -1)
err_logs=$($LOGQ tail "{service=\"$service\"} |= \"ERROR\"" 15 3 2>/dev/null | head -3)
err_string=$(printf '%s' "$err_logs" | jq -r 'try (fromjson | .err // .msg // .) catch .' 2>/dev/null | head -1)

# --- Tavily enrichment for unfamiliar errors ---
web_context="not queried"
if [ -n "${TAVILY_API_KEY:-}" ] && [ -n "$err_string" ] && [ "$err_string" != "null" ]; then
  web_context=$(jq -n --arg q "$err_string $service" '{query:$q, max_results:2, api_key:env.TAVILY_API_KEY}' \
    | curl -sf -m 20 https://api.tavily.com/search -H "Content-Type: application/json" -d @- 2>/dev/null \
    | jq -r '[.results[]? | "- " + .title + ": " + (.content|tostring|.[0:180])] | join("\n")' 2>/dev/null)
  [ -z "$web_context" ] && web_context="no results"
fi

# --- Nemotron analysis ---
prompt="You are an SRE analyzing a live alert. Given the alert, live evidence, and web context, write a 2-sentence root-cause assessment and 1 suggested first action. Be specific; use the numbers.

ALERT: $alertname ($severity) on $service — $summary

LIVE EVIDENCE:
- up: $up
- error_ratio_5m: $err_ratio
- heap_alloc_bytes: $heap
- latency_mode: $lat_mode
- recent ERROR logs:
$err_logs

WEB CONTEXT (for the error string):
$web_context

RUNBOOK EXCERPT:
$(printf '%s' "$runbook" | head -20)

Reply format (plain text, two lines):
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

# --- incident report ---
cat > "$report" <<EOF
# $alertname — $date

- **Service**: $service
- **Severity**: $severity
- **Status**: firing
- **Summary**: $summary

## Root cause
$rootcause

## Evidence
- up: $up
- error_ratio_5m: $err_ratio
- heap_alloc_bytes: $heap
- latency_mode: $lat_mode
- recent ERROR logs:
\`\`\`
$err_logs
\`\`\`

## Web context
$web_context

## Suggested first action
$action

## Full Nemotron analysis
$analysis
EOF

# --- Telegram brief on stdout (cron announce delivers it) ---
printf 'ALERT: %s (%s)\nService: %s\nLikely cause: %s\nEvidence: err_ratio=%s heap=%s latency_mode=%s\nSuggested: %s\n' \
  "$alertname" "$severity" "$service" "$(printf '%s' "$rootcause" | head -c 200)" \
  "$err_ratio" "$heap" "$lat_mode" "$(printf '%s' "$action" | head -c 120)"
