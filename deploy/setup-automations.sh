#!/usr/bin/env bash
# Wire the OpenClaw automations for the on-call copilot.
# Run inside the NemoClaw sandbox after onboarding (Phase 2).
#
# Creates:
#   1. alert-intake: stream automation supervising alert-relay.
#      Each alert batch fires an isolated investigation turn.
#   2. (Optional) daily digest job as a sanity/heartbeat check.
set -euo pipefail

RELAY_BIN="${RELAY_BIN:-/opt/oncall-copilot/alert-relay}"
RELAY_TOKEN="${RELAY_TOKEN:?set RELAY_TOKEN (must match the k8s relay-token secret)}"
TELEGRAM_CHAT_ID="${TELEGRAM_CHAT_ID:?set TELEGRAM_CHAT_ID}"
MODEL_TRIAGE="${MODEL_TRIAGE:-nvidia/nemotron-3-nano}"
MODEL_INVESTIGATE="${MODEL_INVESTIGATE:-nvidia/nemotron-3-ultra}"

# 1. Alert intake: alert-relay prints one JSON line per alert; the stream
#    automation batches lines and fires an investigation turn per batch.
openclaw automations add \
  --name "alert-intake" \
  --stream-command "[\"$RELAY_BIN\",\"-addr\",\"0.0.0.0:9099\",\"-token\",\"$RELAY_TOKEN\"]" \
  --stream-mode line \
  --stream-batch-ms 2000 \
  --session isolated \
  --model "$MODEL_INVESTIGATE" \
  --message "You are the on-call copilot. An alert batch arrived (JSON lines appended below).
For each alert:
1. Read memory/SELF.md and the runbook for the affected service (memory/runbooks/<service>.md).
2. Check memory/incidents/ for a recent report on the same alertname; if one exists and is < 1h old, update it instead of duplicating.
3. Investigate with the promql-query and logql-query skills: confirm the alert condition, find the likely root cause, collect 1-3 evidence numbers and 1-3 verbatim log lines.
4. If an error string is unfamiliar, use web search for the exact message.
5. Write the report to memory/incidents/<YYYY-MM-DD>-<alertname>.md (what fired, root cause, evidence, suggested actions, status).
6. Reply with the incident brief for paging: ALERT NAME, severity, service, likely root cause (one sentence), key evidence, suggested first actions. Keep it under 120 words, plain text, no headers.
If the batch is a resolved notification with no active incident, reply NO_REPLY." \
  --announce \
  --channel telegram \
  --to "$TELEGRAM_CHAT_ID"

# 2. Daily digest (sanity check the agent is alive + summary of incidents).
openclaw automations create "0 9 * * *" \
  --name "daily-digest" \
  --session isolated \
  --model "$MODEL_TRIAGE" \
  --message "Summarize memory/incidents/ from the last 24h in <=5 lines: counts by severity, open items, anything recurring. If none, reply NO_REPLY." \
  --announce \
  --channel telegram \
  --to "$TELEGRAM_CHAT_ID"

echo "automations wired:"
openclaw automations list
