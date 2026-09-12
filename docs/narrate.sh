#!/usr/bin/env bash
# Generate narration segments (piper TTS) and concatenate to a single track.
# Usage: ./docs/narrate.sh
set -euo pipefail

cd "$(dirname "$0")/.."
export PATH="$HOME/.local/bin:$PATH"
OUT=/tmp/opencode/video/narration
mkdir -p "$OUT"

seg() {
  local id="$1"; shift
  piper --data-dir /tmp/opencode/voice --model en_US-lessac-medium \
    --output_file "$OUT/$id.wav" "$*" 2>/dev/null
}

echo "== generating narration =="
seg 01-hook "On call is brutal. A bare alert at 3 AM, and the first five minutes is always the same scramble: dashboards, logs, and a runbook hunt. I built an AI on-call copilot that investigates before it pages me, on open infrastructure, Nebius and NVIDIA open models, with my telemetry under my control."
seg 02-arch "Here is the system. A demo service on Kubernetes with Prometheus, Alertmanager, Loki, and Grafana. Alerts go to the copilot running inside an NVIDIA OpenShell sandbox via NemoClaw, with deny-by-default network policy. Reasoning is NVIDIA Nemotron 3 Ultra, an open model, served on Nebius Token Factory."
seg 03-demo "I inject a failure, error chaos on the service. Within about a minute the alert fires. Instead of a raw page, the copilot pulls the real error rate from Prometheus, reads the error lines from Loki, searches the web with Tavily, then asks Nemotron 3 Ultra to reason about root cause. And here is the page: likely cause, evidence, and a suggested first action. It writes an incident report to persistent memory, so next time it already knows the history."
seg 04-close "Same for latency and memory. Tested end to end: three scenarios, verified root-cause reports. Open source, sandboxed, private. On call should not mean waking up to a mystery."

echo "== aligning + concatenating =="
FILES=""
for id in 01-hook 02-arch 03-demo 04-close; do FILES="$FILES -i $OUT/$id.wav"; done
rm -f "$OUT/list.txt"
ffmpeg -y $FILES \
  -filter_complex "[0:a][1:a][2:a][3:a]concat=n=4:v=0:a=1[out]" -map "[out]" "$OUT/full.wav"

echo "narration: $(ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1 "$OUT/full.wav")"
