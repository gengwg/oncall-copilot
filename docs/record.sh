#!/usr/bin/env bash
# Record the demo video: screen capture + timed narration, mixed to <=3:00.
# Usage: ./docs/record.sh [scenario]
set -euo pipefail

cd "$(dirname "$0")/.."
SC="${1:-error}"
OUT=/tmp/opencode/video
NARR=/tmp/opencode/video/narration/full.wav
mkdir -p "$OUT"
DISPLAY="${DISPLAY:-:0}"

echo "== stage: start services =="
bash deploy/demo-services.sh start >/dev/null 2>&1

echo "== start screen capture =="
# left half of the 3840x2160 screen -> 1920x2160 (terminal region)
ffmpeg -y -f x11grab -video_size 1920x2160 -framerate 15 -i "$DISPLAY"+0 \
  -c:v libx264 -preset ultrafast -pix_fmt yuv420p "$OUT/screen.mp4" &
REC_PID=$!
sleep 2
echo "recording..."
# run the demo on camera while we capture
sleep 5
./tests/e2e/run.sh "$SC" > "$OUT/e2e.log" 2>&1 &
E2E_PID=$!
# let it run until narration length (78s) is up, then stop capture
wait "$E2E_PID" || true
sleep 3
kill "$REC_PID" 2>/dev/null || true
sleep 2

echo "== mix =="
ffmpeg -y -i "$OUT/screen.mp4" -i "$NARR" \
  -c:v libx264 -preset medium -pix_fmt yuv420p -c:a aac -shortest \
  "$OUT/final.mp4"
echo "final: $(ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1 "$OUT/final.mp4")"
