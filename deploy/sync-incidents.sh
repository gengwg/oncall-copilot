#!/usr/bin/env bash
# Sync incident reports from the oncall sandbox to a host directory that the
# dashboard serves. Runs as a lightweight host-side loop.
# Usage: ./deploy/sync-incidents.sh [host-dir]
set -uo pipefail

SB="${SB:-oncall}"
DEST="${1:-/tmp/opencode/dashboard-incidents}"
SANDBOX_DIR=/sandbox/.openclaw/workspace/memory/incidents
mkdir -p "$DEST"

echo "syncing $SANDBOX_DIR -> $DEST every 20s"
while true; do
  # list remote reports, pull any newer than local via base64
  names=$(nemoclaw "$SB" exec -- sh -c "ls $SANDBOX_DIR/*.md 2>/dev/null | xargs -n1 basename 2>/dev/null" 2>/dev/null | grep -v "Active gateway" | tr -d '\r' || true)
  for n in $names; do
    remote_ts=$(nemoclaw "$SB" exec -- sh -c "stat -c %Y $SANDBOX_DIR/$n 2>/dev/null" 2>/dev/null | grep -oE '[0-9]+' | head -1 || echo 0)
    local_ts=$(stat -c %Y "$DEST/$n" 2>/dev/null || echo 0)
    if [ "${remote_ts:-0}" -gt "${local_ts:-0}" ]; then
      nemoclaw "$SB" exec -- sh -c "base64 -w0 $SANDBOX_DIR/$n" 2>/dev/null | grep -v "Active gateway" | tr -d '\r' | base64 -d > "$DEST/$n" 2>/dev/null && \
        touch -d "@${remote_ts}" "$DEST/$n" 2>/dev/null
      echo "synced $n"
    fi
  done
  sleep 20
done
