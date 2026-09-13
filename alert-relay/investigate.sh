#!/usr/bin/env bash
# Deterministic incident investigator. Runs as an OpenClaw command payload.
#   1. Drains the alert-relay queue, investigates every firing alert in it
#   2. Runs real PromQL/LogQL evidence collection (scenario-appropriate)
#   3. For unfamiliar error strings, enriches with a Tavily web search
#   4. Asks Nemotron (via the sandbox inference route) to analyze
#   5. Writes an incident report to memory/incidents/
#   6. Prints a Telegram brief on stdout (delivered by the cron announce)
#
# Usage: investigate.sh ['<alert-json-line>']
set -uo pipefail

# The 30s cadence can overlap a slow run (Nemotron -m 150 + Tavily -m 20).
# Serialize with a lock so two runs never double-drain or clobber one report.
# If the lock is unavailable, proceed unlocked with a visible warning rather
# than silently NO_REPLY-ing every tick.
WORKSPACE="${WORKSPACE:-/sandbox/.openclaw/workspace}"
LOCKFILE="$WORKSPACE/.investigate.lock"
mkdir -p "$(dirname "$LOCKFILE")" 2>/dev/null || true
# Open the lock file before flock: a failed `exec 9>` does NOT abort the script,
# so an unguarded redirect leaves fd 9 closed and flock then fails with "Bad
# file descriptor" -> NO_REPLY on every tick, i.e. a silent outage.
if command -v flock >/dev/null 2>&1 && : >>"$LOCKFILE" 2>/dev/null && exec 9>>"$LOCKFILE"; then
  flock -n 9 || { echo "NO_REPLY"; exit 0; }
else
  echo "warn: cannot lock $LOCKFILE (flock missing or unwritable); running unlocked" >&2
fi

export PROMETHEUS_URL="${PROMETHEUS_URL:-http://172.18.0.1:19090}"
export LOKI_URL="${LOKI_URL:-http://172.18.0.1:13100}"
RELAY="${ALERT_RELAY_URL:-http://172.18.0.1:9099}"
# Bearer token for the relay's /alerts drain (same token Alertmanager uses to POST).
RELAY_TOKEN="${RELAY_TOKEN:-}"
PROM="$WORKSPACE/skills/promql-query/scripts/query.sh"
LOGQ="$WORKSPACE/skills/logql-query/scripts/query.sh"
INCIDENTS="$WORKSPACE/memory/incidents"
RUNBOOKS="$WORKSPACE/memory/runbooks"
MODEL="${MODEL:-nvidia/Nemotron-3-Ultra-550b-a55b}"
INFERENCE_URL="${INFERENCE_URL:-https://inference.local/v1/chat/completions}"

# Prometheus returns NaN for 0/0 -- a ratio computed over a window with no
# traffic -- and +Inf/-Inf for division by zero. Those are real result values,
# not empty results, so neither the jq fallback nor ${x:-n/a} catches them and
# "err_ratio=NaN" reaches the brief, where the model remarks on it instead of
# the incident.
# Telegram briefs are capped per field. Cutting at a byte boundary lops words in
# half ("and verify K"), which reads as a broken page rather than a short one.
clip() { # clip <max-chars>: flatten newlines, trim at a word boundary, mark elision
  tr '\n' ' ' | awk -v n="$1" '{
    gsub(/  +/, " ");
    if (length($0) <= n) { print; next }
    s = substr($0, 1, n);
    sub(/[^ ]*$/, "", s);
    sub(/[ ,;:.]+$/, "", s);
    print s "..."
  }'
}

norm() {
  case "$1" in
    ""|NaN|nan|+Inf|-Inf|Inf|inf) echo "n/a" ;;
    *) echo "$1" ;;
  esac
}

if [ -z "${NEBIUS_API_KEY:-}" ]; then
  echo "error: NEBIUS_API_KEY is unset; cannot reach $INFERENCE_URL" >&2
  echo "NO_REPLY"
  exit 1
fi

if [ $# -eq 0 ]; then
  auth_header=()
  [ -n "$RELAY_TOKEN" ] && auth_header=(-H "Authorization: Bearer $RELAY_TOKEN")
  body=$(mktemp)
  # Distinguish "relay said no alerts" from "relay refused us". Without the
  # status check a 401 (token drift) looks exactly like an empty queue, so
  # alerts pile up in the relay and nothing is ever investigated or paged.
  code=$(curl -s -m 8 -o "$body" -w '%{http_code}' "${auth_header[@]}" "$RELAY/alerts" 2>/dev/null) || true
  code="${code:-000}"
  batch=$(cat "$body"); rm -f "$body"
  if [ "$code" != "200" ]; then
    case "$code" in
      000) echo "error: alert-relay unreachable at $RELAY/alerts" >&2 ;;
      401) echo "error: alert-relay rejected our RELAY_TOKEN (HTTP 401) — token drift between the relay and this cron" >&2 ;;
      *)   echo "error: alert-relay drain failed (HTTP $code) at $RELAY/alerts" >&2 ;;
    esac
    echo "NO_REPLY"
    exit 1
  fi
  # The drain is destructive: the relay clears its queue on GET. Investigate
  # every firing alert in the batch or the rest are lost.
  mapfile -t alerts < <(printf '%s' "$batch" | jq -c '.[] | select(.status=="firing")' 2>/dev/null)
  [ "${#alerts[@]}" -eq 0 ] && { echo "NO_REPLY"; exit 0; }
else
  alerts=("$1")
fi

investigate_one() {
alert_json="$1"
alertname=$(printf '%s' "$alert_json" | jq -r '.alertname // "Unknown"')
service=$(printf '%s' "$alert_json" | jq -r '.service // "demo-app"')
severity=$(printf '%s' "$alert_json" | jq -r '.severity // "warning"')
summary=$(printf '%s' "$alert_json" | jq -r '.summary // ""')
status=$(printf '%s' "$alert_json" | jq -r '.status // "firing"')
date=$(date -u +%Y-%m-%d)
# Sanitize: alertname is an untrusted label and lands in a path. A '/' or '..'
# would write outside $INCIDENTS.
alert_slug=$(printf '%s' "$alertname" | tr -c 'A-Za-z0-9_-' '_')
report="$INCIDENTS/$date-$alert_slug.md"
mkdir -p "$INCIDENTS"

[ "$status" != "firing" ] && return 1

runbook=$(cat "$RUNBOOKS/$service.md" 2>/dev/null || echo "no runbook")

# --- evidence collection (scenario-aware, read-only, bounded) ---
# Anchor the log window to the alert's start so repeated runs don't re-read the
# same stale lines.
starts_at=$(printf '%s' "$alert_json" | jq -r '.starts_at // empty')
lookback=15
if [ -n "$starts_at" ]; then
  start_s=$(date -d "$starts_at" +%s 2>/dev/null || echo 0)
  now_s=$(date +%s)
  [ "$start_s" -gt 0 ] && lookback=$(( (now_s - start_s) / 60 + 2 ))
  [ "$lookback" -gt 60 ] && lookback=60
  [ "$lookback" -lt 5 ] && lookback=5
fi

# Reset per-alert so evidence from the previous alert in the batch can't leak in.
heap_trend=""
# Always-collected baselines.
up=$($PROM instant "up{job=\"$service\"}" 2>/dev/null | jq -r '.value // "n/a"' | head -1)
err_ratio=$($PROM instant 'sum(rate(demo_errors_total[5m])) / sum(rate(demo_requests_total[5m]))' 2>/dev/null | jq -r '.value // "n/a"' | head -1)

# Scenario-specific signals. The alert name maps to the failure class.
case "$alertname" in
  *Latency*|*latency*)
    lat_mode=$($PROM instant 'demo_latency_mode' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
    heap=$($PROM instant 'demo_heap_alloc_bytes' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
    # latency chaos logs "slow request" WARNs; fall back to all logs if none in window
    logs=$($LOGQ tail "{service=\"$service\"} |~ \"slow request|WARN\"" "$lookback" 3 2>/dev/null | head -3)
    [ -z "$logs" ] && logs=$($LOGQ tail "{service=\"$service\"}" 15 3 2>/dev/null | head -3)
    ;;
  *Memory*|*memory*)
    lat_mode=$($PROM instant 'demo_latency_mode' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
    heap=$($PROM instant 'demo_heap_alloc_bytes' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
    # slope matters for a leak
    heap_trend=$($PROM range 'demo_heap_alloc_bytes' "$lookback" 15 2>/dev/null | jq -c '.points' | head -1)
    logs=$($LOGQ tail "{service=\"$service\"}" "$lookback" 3 2>/dev/null | head -3)
    ;;
  *Down*|*down*|*)
    lat_mode=$($PROM instant 'demo_latency_mode' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
    heap=$($PROM instant 'demo_heap_alloc_bytes' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
    logs=$($LOGQ tail "{service=\"$service\"} |= \"ERROR\"" "$lookback" 3 2>/dev/null | head -3)
    ;;
esac
# `jq -r '.value // "n/a"'` yields an empty string, not "n/a", when the query
# returns no series at all (zero jq inputs -> zero output lines) — which is
# exactly the ServiceDown case. norm() also folds NaN/Inf to n/a.
up=$(norm "$up")
err_ratio=$(norm "$err_ratio")
heap=$(norm "$heap")
lat_mode=$(norm "$lat_mode")
err_logs="${logs:-}"
err_string=$(printf '%s' "$err_logs" | head -1 | sed 's/^[^\t]*\t//' | jq -r 'try (.err // .msg // empty) catch empty' 2>/dev/null | head -1)

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
- heap_trend: ${heap_trend:-n/a}
- latency_mode: $lat_mode
- recent logs (${lookback}m window from alert start):
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
# Build with printf '%s' per field — never expand untrusted values (Tavily web
# results, Nemotron output, log lines, alert annotations) into a heredoc body,
# which would re-parse and execute $(...) / backticks.
{
  printf '# %s — %s\n\n' "$alertname" "$date"
  printf -- '- **Service**: %s\n' "$service"
  printf -- '- **Severity**: %s\n' "$severity"
  printf -- '- **Status**: firing\n'
  printf -- '- **Summary**: %s\n\n' "$summary"
  printf '## Root cause\n%s\n\n' "$rootcause"
  printf '## Evidence\n'
  printf -- '- up: %s\n' "$up"
  printf -- '- error_ratio_5m: %s\n' "$err_ratio"
  printf -- '- heap_alloc_bytes: %s\n' "$heap"
  printf -- '- heap_trend: %s\n' "${heap_trend:-n/a}"
  printf -- '- latency_mode: %s\n' "$lat_mode"
  printf -- '- recent logs (%sm window from alert start):\n```\n%s\n```\n\n' "$lookback" "$err_logs"
  printf '## Web context\n%s\n\n' "$web_context"
  printf '## Suggested first action\n%s\n\n' "$action"
  printf '## Full Nemotron analysis\n%s\n' "$analysis"
} > "$report"

# --- Telegram brief on stdout (cron announce delivers it) ---
printf 'ALERT: %s (%s)\nService: %s\nLikely cause: %s\nEvidence: err_ratio=%s heap=%s latency_mode=%s\nSuggested: %s\n' \
  "$alertname" "$severity" "$service" "$(printf '%s' "$rootcause" | clip 200)" \
  "$err_ratio" "$heap" "$lat_mode" "$(printf '%s' "$action" | clip 120)"
}

briefed=0
for a in "${alerts[@]}"; do
  [ -n "$a" ] || continue
  investigate_one "$a" && briefed=$((briefed + 1))
done
[ "$briefed" -eq 0 ] && echo "NO_REPLY"
exit 0
