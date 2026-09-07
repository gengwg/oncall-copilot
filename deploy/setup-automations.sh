#!/usr/bin/env bash
# Wire the OpenClaw cron automations for the on-call copilot.
# Run from the HOST; it drives the sandbox via `nemoclaw <sb> exec`.
#
# alert-intake: every 15s, trigger.js drains the host alert-relay queue; when
#   it returns alerts, an isolated agent turn investigates and pages Telegram.
set -euo pipefail

SB="${SB:-oncall}"
TELEGRAM_CHAT_ID="${TELEGRAM_CHAT_ID:?set TELEGRAM_CHAT_ID}"
WORKSPACE=/sandbox/.openclaw/workspace
# The cron --model flag takes the full provider/model path as registered in
# OpenClaw (inference/<model-id>), not a bare model id.
MODEL_INVESTIGATE="${MODEL_INVESTIGATE:-inference/nvidia/Nemotron-3-Ultra-550b-a55b}"

PROMPT=$(cat "$(dirname "$0")/agent-investigate-prompt.md")

# Remove any prior copy for idempotent re-runs.
nemoclaw "$SB" exec -- sh -c "openclaw cron list --json 2>/dev/null | jq -r '.[] | select(.name==\"alert-intake\") | .id' | while read -r id; do openclaw cron rm \"\$id\" 2>/dev/null; done" >/dev/null 2>&1 || true

nemoclaw "$SB" exec -- openclaw cron add \
  --name "alert-intake" \
  --every 30s \
  --trigger-script "$WORKSPACE/bin/trigger.js" \
  --session isolated \
  --model "$MODEL_INVESTIGATE" \
  --message "$PROMPT" \
  --tools exec,read,write \
  --announce \
  --channel telegram \
  --to "$TELEGRAM_CHAT_ID"

nemoclaw "$SB" exec -- openclaw cron list