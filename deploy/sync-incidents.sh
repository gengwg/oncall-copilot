#!/usr/bin/env bash
# Sync incident reports from the oncall sandbox to a host directory that the
# dashboard serves, and publish each new one to the repo so the GitHub Pages
# feed updates. Runs as a lightweight host-side loop.
# Usage: ./deploy/sync-incidents.sh [host-dir]
# Env: PUBLISH_REPO (default: this checkout; empty disables publishing). Pushing
# needs git push access in this environment, e.g. SSH_AUTH_SOCK.
set -uo pipefail

SB="${SB:-oncall}"
DEST="${1:-/tmp/opencode/dashboard-incidents}"
SANDBOX_DIR=/sandbox/.openclaw/workspace/memory/incidents
REPO="${PUBLISH_REPO-$(cd "$(dirname "$0")/.." && pwd)}"
mkdir -p "$DEST"

publish() { # publish <name>: commit the report on main and push. Idempotent.
  [ -n "$REPO" ] && [ -d "$REPO/deploy/dashboard/incidents" ] || return 0
  cmp -s "$DEST/$1" "$REPO/deploy/dashboard/incidents/$1" && return 0
  if [ "$(git -C "$REPO" branch --show-current)" != "main" ]; then
    echo "not publishing $1: $REPO is not on main"
    return 0
  fi
  git -C "$REPO" pull -q --rebase origin main 2>/dev/null
  cp "$DEST/$1" "$REPO/deploy/dashboard/incidents/$1"
  git -C "$REPO" add -- "deploy/dashboard/incidents/$1"
  if git -C "$REPO" commit -q -m "incident: ${1%.md}" -- "deploy/dashboard/incidents/$1" \
     && git -C "$REPO" push -q origin main; then
    echo "published $1"
  else
    echo "publish failed for $1"
  fi
}

echo "syncing $SANDBOX_DIR -> $DEST every 20s; publishing to ${REPO:-nowhere}"
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
    publish "$n"
  done
  sleep 20
done
