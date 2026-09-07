#!/usr/bin/env bash
# Deploy repo skills, memory, and the investigation scripts into the oncall
# sandbox. Small text files go via base64-over-exec; the alert-relay binary via
# `openshell sandbox upload`.
# Usage: ./deploy/deploy-to-sandbox.sh [sandbox-name]
set -euo pipefail

cd "$(dirname "$0")/.."
SB="${1:-oncall}"
WORKSPACE=/sandbox/.openclaw/workspace

put() { # <local-file> <sandbox-abs-path>
  local src="$1" dst="$2"
  local b64; b64=$(base64 -w0 "$src")
  nemoclaw "$SB" exec -- sh -c "mkdir -p '$(dirname "$dst")' && echo '$b64' | base64 -d > '$dst'" >/dev/null
  echo "  $dst"
}

echo "== skills =="
for f in $(find skills -type f); do
  put "$f" "$WORKSPACE/$f"
done

echo "== memory =="
for f in $(find memory -type f); do
  put "$f" "$WORKSPACE/$f"
done

echo "== investigation scripts =="
put alert-relay/investigate.sh "$WORKSPACE/bin/investigate.sh"
nemoclaw "$SB" exec -- chmod +x "$WORKSPACE/bin/investigate.sh" >/dev/null

echo "== alert-relay binary =="
mkdir -p /tmp/opencode
(cd alert-relay && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/opencode/alert-relay-sandbox .)
openshell sandbox upload "$SB" /tmp/opencode/alert-relay-sandbox "$WORKSPACE/bin/alert-relay" >/dev/null
nemoclaw "$SB" exec -- chmod +x "$WORKSPACE/bin/alert-relay" >/dev/null

echo "done"
