#!/usr/bin/env bash
# Generate the demo-video narration with piper TTS.
# Segment names are the anchors docs/build-video.sh places in the timeline.
# Usage: ./docs/narrate-demo.sh [outdir]
set -euo pipefail

OUT="${1:-/tmp/opencode/video/v3/narr}"
VOICE_DIR="${VOICE_DIR:-/tmp/opencode/voice}"
MODEL=en_US-lessac-medium
PIPER="$HOME/.local/bin/piper"
mkdir -p "$OUT" "$VOICE_DIR"

# piper is a uv tool; its own interpreter owns the download module.
if [ ! -f "$VOICE_DIR/$MODEL.onnx" ]; then
  PY=$(head -1 "$PIPER" | sed 's|^#!||')
  "$PY" -m piper.download_voices "$MODEL" --data-dir "$VOICE_DIR"
fi

say() {
  "$PIPER" --data-dir "$VOICE_DIR" --model "$MODEL" --output_file "$OUT/$1.wav" "$2" 2>/dev/null
}

say 1-hook "On-call is brutal. A bare alert at three A M, and the first five minutes is always the same scramble: dashboards, logs, a runbook hunt. This is an AI on-call copilot that does that investigation before it pages you."
say 2-setup "Here it is running. A demo service on Kubernetes with Prometheus, Alertmanager, and Loki. The copilot lives inside an NVIDIA OpenShell sandbox, deny-by-default, reachable only to my own telemetry and the inference route."
say 3-chaos "I inject a real failure: upstream dial timeouts on half of all requests."
say 4-alert "Prometheus fires the alert. This is the moment you would normally get woken up by a one-line page."
say 5-invest "Instead, the copilot investigates. Prometheus for the live error rate, Loki for the failing log lines, Tavily for the unfamiliar error, then NVIDIA Nemotron 3 Ultra on Nebius Token Factory to reason about root cause."
say 6-page "And this is the page. Likely cause, the evidence behind it, and a suggested first action, fourteen seconds after the alert fired. The report is filed to persistent memory."
say 7-close "Clear the chaos and it resolves itself. Open models, sandboxed, private. On-call should not mean waking up to a mystery."

echo "narration in $OUT"
