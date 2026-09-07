#!/usr/bin/env bash
# Wire the OpenClaw cron automation for the on-call copilot.
# Run from the HOST; it drives the sandbox via `nemoclaw <sb> exec`.
#
# alert-intake: every 30s, bin/investigate.sh (command payload) drains the
#   alert-relay queue, collects PromQL/LogQL evidence, calls Nemotron for RCA,
#   writes memory/incidents/<date>-<alert>.md, and pages Telegram.
#
# Required env: TELEGRAM_CHAT_ID, NEBIUS_API_KEY, TAVILY_API_KEY, RELAY_TOKEN.
set -euo pipefail

SB="${SB:-oncall}"
TELEGRAM_CHAT_ID="${TELEGRAM_CHAT_ID:?set TELEGRAM_CHAT_ID}"
NEBIUS_API_KEY="${NEBIUS_API_KEY:?set NEBIUS_API_KEY}"
TAVILY_API_KEY="${TAVILY_API_KEY:?set TAVILY_API_KEY}"

# shellcheck disable=SC1091
source "$(dirname "$0")/lib.sh"

# Derive the relay token from the cluster (same secret Alertmanager uses), so
# the cron's drain auth always matches the running relay. Exported RELAY_TOKEN
# overrides.
if [ -z "${RELAY_TOKEN:-}" ]; then
  RELAY_TOKEN=$(relay_token || true)
fi
RELAY_TOKEN="${RELAY_TOKEN:?could not read relay-token secret from the observability namespace and RELAY_TOKEN is unset}"
WORKSPACE=/sandbox/.openclaw/workspace

# Idempotent re-runs: drop any prior alert-intake job (agentTurn or command).
nemoclaw "$SB" exec -- sh -c "openclaw cron list --json 2>/dev/null | python3 -c 'import sys,json; [print(x[\"id\"]) for x in json.load(sys.stdin)[\"jobs\"] if x[\"name\"]==\"alert-intake\"]' | while read -r id; do openclaw cron rm \"\$id\" 2>/dev/null; done" >/dev/null 2>&1 || true

# Note: --command-env stores secrets in the job spec (see docs/feedback.md #7).
# Acceptable for a local demo; a production deployment should use the OpenClaw
# secrets store instead.
nemoclaw "$SB" exec -- openclaw cron add \
  --name "alert-intake" \
  --every 30s \
  --command "$WORKSPACE/bin/investigate.sh" \
  --command-env "NEBIUS_API_KEY=$NEBIUS_API_KEY" \
  --command-env "TAVILY_API_KEY=$TAVILY_API_KEY" \
  --command-env "RELAY_TOKEN=$RELAY_TOKEN" \
  --timeout-seconds 180 \
  --session isolated \
  --announce \
  --channel telegram \
  --to "$TELEGRAM_CHAT_ID"

nemoclaw "$SB" exec -- openclaw cron list