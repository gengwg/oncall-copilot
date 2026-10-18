#!/usr/bin/env bash
# Generate the demo-video narration with ElevenLabs v4 via OpenRouter.
# Segment names are the anchors docs/build-video.sh places in the timeline.
# Usage: ./docs/narrate-demo.sh [outdir]   (VOICE=george by default)
set -euo pipefail

cd "$(dirname "$0")/.."
if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi
: "${OPENROUTER_API_KEY:?set OPENROUTER_API_KEY in .env}"

OUT="${1:-/tmp/opencode/video/v3/narr}"
VOICE="${VOICE:-george}"
mkdir -p "$OUT"

say() {
  jq -n --arg t "$2" --arg v "$VOICE" \
    '{model: "elevenlabs/eleven-v4", input: $t, voice: $v, response_format: "mp3"}' \
    | curl -sf https://openrouter.ai/api/v1/audio/speech \
        -H "Authorization: Bearer $OPENROUTER_API_KEY" -H "Content-Type: application/json" \
        -d @- -o "$OUT/$1.mp3"
  ffmpeg -v error -y -i "$OUT/$1.mp3" "$OUT/$1.wav"
}

say 1-hook "On-call is brutal. A bare alert at three A M, and the first five minutes is always the same scramble: dashboards, logs, a runbook hunt. This is an AI on-call copilot that does that investigation before it pages you."
say 2-setup "Here it is running. A demo service on Kubernetes with Prometheus, Alertmanager, and Loki. The copilot lives inside an NVIDIA OpenShell sandbox, deny-by-default, reachable only to my own telemetry and the inference route."
say 3-chaos "I inject a real failure: upstream dial timeouts on half of all requests."
say 3b-wait "Prometheus has to see the error rate stay above ten percent for thirty seconds before it fires, so a single blip never pages anyone. This is real time, nothing is sped up."
say 4-alert "Prometheus fires the alert. This is the moment you would normally get woken up by a one-line page."
say 5-invest "Instead, the copilot investigates. Prometheus for the live error rate, Loki for the failing log lines, Tavily for the unfamiliar error, then NVIDIA Nemotron 3 Ultra on Nebius Token Factory to reason about root cause."
say 6-page "And this is the page. Likely cause, how confident it is and why, whether it has seen this before, and a suggested first action, about a minute after the failure began. The report is filed to persistent memory."
say 7-close "Clear the chaos and it resolves itself. Open models, sandboxed, private. On-call should not mean waking up to a mystery."

echo "narration in $OUT"
