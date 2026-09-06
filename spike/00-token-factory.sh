#!/usr/bin/env bash
# Phase 0 spike: verify Token Factory access and Nemotron 3 model IDs.
# Usage: NEBIUS_API_KEY=... ./spike/00-token-factory.sh
set -euo pipefail

: "${NEBIUS_API_KEY:?set NEBIUS_API_KEY}"
BASE=https://api.tokenfactory.nebius.com/v1

echo "== Models containing 'nemotron' =="
curl -sf "$BASE/models" -H "Authorization: Bearer $NEBIUS_API_KEY" \
  | jq -r '.data[].id' | grep -i nemotron || echo "(none found)"

echo
echo "== Smoke test: chat completion =="
MODEL="${1:-}"
if [ -z "$MODEL" ]; then
  echo "pass a model id as arg to run the chat completion test"; exit 0
fi
curl -sf "$BASE/chat/completions" \
  -H "Authorization: Bearer $NEBIUS_API_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with exactly: TOKEN_FACTORY_OK\"}],\"max_tokens\":20}" \
  | jq -r '.choices[0].message.content, .usage'
