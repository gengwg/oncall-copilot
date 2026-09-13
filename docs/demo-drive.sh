#!/usr/bin/env bash
# On-camera demo driver. Runs the real pipeline at a pace a viewer can follow:
# inject chaos -> alert fires -> copilot investigates -> Telegram brief.
#
# Arrange the screen first: terminal on one side, Telegram Desktop on the other.
# Start the screen recording, then run this. Usage: ./docs/demo-drive.sh
set -uo pipefail

cd "$(dirname "$0")/.."
# shellcheck disable=SC1091
source deploy/lib.sh

DEMO=http://localhost:18080
AM=http://localhost:19093
B=$'\033[1m'; DIM=$'\033[2m'; G=$'\033[32m'; Y=$'\033[33m'; C=$'\033[36m'; R=$'\033[0m'
T0=$(date +%s)
el() { printf '%3ss' "$(( $(date +%s) - T0 ))"; }
say() { printf '%s\n' "$*"; }
rule() { printf '%s\n' "${DIM}────────────────────────────────────────────────────────────${R}"; }
step() { printf '\n%s\n' "${B}${C}$*${R}"; }

trap 'curl -sf -m 2 "$DEMO/chaos/stop" >/dev/null 2>&1; [ -n "${TRAFFIC:-}" ] && kill "$TRAFFIC" 2>/dev/null; exit 0' INT TERM

clear
say "${B}  OnCall Copilot${R}  ${DIM}— investigates before it pages you${R}"
say "${DIM}  Nemotron 3 Ultra on Nebius Token Factory, in an NVIDIA OpenShell sandbox${R}"
rule

step "[1/5] System check"
printf '  %-12s' "relay"
curl -sf -m 2 -H "Authorization: Bearer $(relay_token)" http://172.18.0.1:9099/authcheck >/dev/null \
  && say "${G}ok${R}" || say "${Y}unreachable${R}"
printf '  %-12s' "cron"
timeout 30 nemoclaw oncall exec -- sh -c 'openclaw cron list --json 2>/dev/null | python3 -c "import sys,json; j=json.load(sys.stdin)[\"jobs\"][0]; print(j[\"name\"]+\", every 30s\")"' 2>/dev/null \
  | grep -v "Active gateway" | tail -1
printf '  %-12s' "demo-app"
em=$(curl -sf -m 2 "$DEMO/metrics" | rg '^demo_error_mode ' | awk '{print $2}')
[ "$em" = "0" ] && say "${G}healthy, no chaos injected${R}" || say "${Y}chaos already active${R}"
sleep 3

step "[2/5] Injecting a real failure"
say "  ${DIM}\$ curl '$DEMO/chaos?mode=error'${R}"
curl -sf "$DEMO/chaos?mode=error" >/dev/null && say "  ${Y}error chaos on${R} — upstream dial timeouts, ~50% of requests"
( while true; do curl -sf -o /dev/null -m 3 "$DEMO/"; sleep 0.3; done ) >/dev/null 2>&1 &
TRAFFIC=$!
say "  driving traffic..."
sleep 12
rt=$(curl -sf -m 2 "$DEMO/metrics" | rg '^demo_(requests|errors)_total ' | awk '{print $2}' | paste -sd/ -)
say "  requests/errors: ${B}${rt}${R}"

step "[3/5] Prometheus evaluating the alert rule"
alert=""
for i in $(seq 30); do
  sleep 5
  alert=$(curl -sf -m 3 "$AM/api/v2/alerts" 2>/dev/null | jq -r '[.[] | select(.status.state=="active") | .labels.alertname] | join(",")')
  [ -n "$alert" ] && { say "  $(el)  ${B}${Y}${alert}${R}  ${Y}FIRING${R}"; break; }
  [ $((i % 2)) -eq 0 ] && say "  $(el)  ${DIM}evaluating...${R}"
done
[ -z "$alert" ] && { say "  ${Y}alert did not fire in time${R}"; kill "$TRAFFIC" 2>/dev/null; exit 1; }

step "[4/5] Copilot investigating"
say "  ${DIM}PromQL + LogQL evidence -> Tavily -> Nemotron 3 Ultra -> Telegram${R}"
brief=""
for i in $(seq 24); do
  sleep 5
  brief=$(timeout 30 nemoclaw oncall exec -- sh -c 'openclaw cron list --json 2>/dev/null | python3 -c "
import sys,json
s=json.load(sys.stdin)[\"jobs\"][0][\"state\"]
d=str(s.get(\"lastDiagnosticSummary\",\"\"))
print(d if d.startswith(\"ALERT\") and s.get(\"lastDelivered\") else \"\")
"' 2>/dev/null | grep -v "Active gateway")
  [ -n "$brief" ] && break
  [ $((i % 2)) -eq 0 ] && say "  $(el)  ${DIM}collecting evidence, reasoning...${R}"
done

if [ -n "$brief" ]; then
  say "  $(el)  ${G}brief delivered to Telegram${R}   ${DIM}<- watch the window${R}"
  step "[5/5] What it sent"
  rule
  printf '%s\n' "$brief" | sed 's/^/  /'
  rule
else
  say "  ${Y}no brief within the window${R}"
fi

today=$(date -u +%Y-%m-%d)
say ""
say "  ${DIM}filed to persistent memory:${R} memory/incidents/${today}-${alert%%,*}.md"
sleep 4

say ""
say "  ${DIM}\$ curl '$DEMO/chaos/stop'${R}"
kill "$TRAFFIC" 2>/dev/null
curl -sf "$DEMO/chaos/stop" >/dev/null && say "  ${G}chaos cleared${R} — the alert resolves on its own"
say ""
