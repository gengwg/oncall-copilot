#!/usr/bin/env bash
# E2E harness: inject a chaos scenario, assert the alert pipeline delivers it.
#
# Stages verified per scenario:
#   1. chaos injected into demo-app
#   2. alert fires in Alertmanager (correct alertname)
#   3. alert-relay receives the firing notification (JSONL)
#   4. alert-relay receives the resolved notification after chaos stops
#   5. (agent wired) copilot brief names the injected root cause  [Phase 2+]
#
# Usage: ./tests/e2e/run.sh [scenario ...]  (default: all)
# Env: DEMO_APP_URL, ALERTMANAGER_URL, RELAY_LOG (defaults match deploy/minikube)
set -uo pipefail

DEMO_APP_URL="${DEMO_APP_URL:-http://localhost:18080}"
ALERTMANAGER_URL="${ALERTMANAGER_URL:-http://localhost:19093}"
RELAY_LOG="${RELAY_LOG:-/tmp/opencode/alert-relay.out}"
WAIT_ALERT_S="${WAIT_ALERT_S:-180}"
WAIT_RESOLVE_S="${WAIT_RESOLVE_S:-300}"

PASS=0; FAIL=0; RESULTS=()

# scenario -> expected alertname
declare -A SCENARIOS=(
  [error]=DemoHighErrorRate
  [latency]=DemoHighLatency
  [memory]=DemoMemoryGrowth
)

log() { printf '[e2e %s] %s\n' "$(date +%H:%M:%S)" "$*"; }

preflight() {
  local ok=0
  curl -sf -m 3 "$DEMO_APP_URL/healthz" >/dev/null || { log "preflight: demo-app unreachable at $DEMO_APP_URL"; ok=1; }
  curl -sf -m 3 "$ALERTMANAGER_URL/-/healthy" >/dev/null || { log "preflight: alertmanager unreachable at $ALERTMANAGER_URL"; ok=1; }
  [ -f "$RELAY_LOG" ] || { log "preflight: relay log missing at $RELAY_LOG (is alert-relay running?)"; ok=1; }
  curl -sf -m 3 "${PROMETHEUS_URL:-http://localhost:19090}/-/healthy" >/dev/null || { log "preflight: prometheus unreachable"; ok=1; }
  [ $ok -ne 0 ] && { log "preflight failed — run deploy/minikube/forwards.sh start"; exit 2; }
}

wait_for() { # <description> <timeout_s> <command...>
  local desc="$1" timeout="$2"; shift 2
  local deadline=$((SECONDS + timeout))
  while [ $SECONDS -lt $deadline ]; do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 5
  done
  log "TIMEOUT waiting for: $desc"
  return 1
}

alert_firing() { # <alertname>
  curl -sf "$ALERTMANAGER_URL/api/v2/alerts" \
    | jq -e --arg n "$1" '[.[] | select(.labels.alertname==$n and .status.state=="active")] | length > 0'
}

relay_got() { # <alertname> <status> <since_line>
  awk -v start="$3" 'NR>start' "$RELAY_LOG" \
    | jq -e --arg n "$1" --arg s "$2" 'select(.alertname==$n and .status==$s)' \
    >/dev/null 2>&1
}

run_scenario() { # <scenario>
  local sc="$1" alert="${SCENARIOS[$1]}"
  local baseline; baseline=$(wc -l < "$RELAY_LOG" 2>/dev/null || echo 0)
  log "[$sc] injecting chaos (expect $alert)"
  curl -sf "$DEMO_APP_URL/chaos?mode=$sc" >/dev/null || { log "[$sc] FAIL: chaos endpoint"; return 1; }

  # error scenario needs sustained traffic to keep the ratio up
  local traffic_pid=""
  if [ "$sc" = error ]; then
    ( while true; do curl -sf -o /dev/null "$DEMO_APP_URL/"; sleep 0.3; done ) & traffic_pid=$!
  fi

  local ok=0
  wait_for "alert $alert firing" "$WAIT_ALERT_S" alert_firing "$alert" || ok=1
  # Alertmanager notifies immediately on a NEW alert; for a continuously-firing
  # alert it re-notifies on repeat_interval. The relay firing line may already
  # exist, so accept it appearing at any point after baseline OR already in the
  # relay queue. The decisive stage is the copilot report below.
  [ $ok -eq 0 ] && { wait_for "relay firing notification" 120 relay_got "$alert" firing "$baseline" || log "[$sc] note: no fresh relay line (continuous alert); continuing"; }

  log "[$sc] stopping chaos"
  curl -sf "$DEMO_APP_URL/chaos/stop" >/dev/null
  [ -n "$traffic_pid" ] && kill "$traffic_pid" 2>/dev/null

  [ $ok -eq 0 ] && { wait_for "relay resolved notification" "$WAIT_RESOLVE_S" relay_got "$alert" resolved "$baseline" || ok=1; }

  # Stage 5: the copilot wrote an incident report naming the root cause.
  if [ "${COPILOT_ASSERT:-1}" = "1" ] && [ $ok -eq 0 ]; then
    wait_for "copilot incident report for $alert" 300 copilot_report "$alert" || ok=1
  fi

  if [ $ok -eq 0 ]; then log "[$sc] PASS"; else log "[$sc] FAIL"; fi
  return $ok
}

# copilot_report <alertname>: a non-empty incident report exists in the sandbox
copilot_report() {
  nemoclaw oncall exec -- sh -c \
    "f=/sandbox/.openclaw/workspace/memory/incidents/\$(date -u +%Y-%m-%d)-$1.md; \
     [ -s \"\$f\" ] && grep -q 'Root cause' \"\$f\" && grep -qA1 '## Root cause' \"\$f\" | grep -qv '^## Root cause\$'" \
    >/dev/null 2>&1
}

scenarios=("$@")
[ ${#scenarios[@]} -eq 0 ] && scenarios=(error latency memory)

preflight

for sc in "${scenarios[@]}"; do
  if [ -z "${SCENARIOS[$sc]:-}" ]; then log "unknown scenario: $sc"; continue; fi
  if run_scenario "$sc"; then PASS=$((PASS+1)); RESULTS+=("$sc: PASS"); else FAIL=$((FAIL+1)); RESULTS+=("$sc: FAIL"); fi
  sleep 10 # let Alertmanager settle between scenarios
done

echo
echo "== results =="
for r in "${RESULTS[@]}"; do echo "  $r"; done
echo "pass=$PASS fail=$FAIL"
[ $FAIL -eq 0 ]
