#!/usr/bin/env bash
# Phase 0: onboard the OpenClaw sandbox against Nebius Token Factory.
# Requires .env with NEBIUS_API_KEY (and TAVILY_API_KEY for web search).
# Usage: ./spike/01-onboard.sh [sandbox-name]
set -euo pipefail

cd "$(dirname "$0")/.."
# shellcheck disable=SC1091
source .env

NAME="${1:-oncall}"
: "${NEBIUS_API_KEY:?set NEBIUS_API_KEY in .env}"
# Keep Tavily out of the environment during onboarding (see below).
unset TAVILY_API_KEY

# Nemotron 3 Ultra as the primary model; Nano per-job for triage.
MODEL="${NEMOCLAW_MODEL:-nvidia/Nemotron-3-Ultra-550b-a55b}"

export NEMOCLAW_PROVIDER=custom
export NEMOCLAW_ENDPOINT_URL=https://api.tokenfactory.nebius.com/v1
export NEMOCLAW_MODEL="$MODEL"
export COMPATIBLE_API_KEY="$NEBIUS_API_KEY"
export NEMOCLAW_PREFERRED_API=openai-completions
export NEMOCLAW_SANDBOX_NAME="$NAME"

# Do NOT export TAVILY_API_KEY here: v0.0.109 auto-detects it and the Tavily
# plugin install fails during the sandbox image build (GH bug). Tavily is added
# to the running sandbox afterwards instead.

echo "Onboarding sandbox '$NAME' with Token Factory ($MODEL)..."
nemoclaw onboard --non-interactive --yes --yes-i-accept-third-party-software --name "$NAME" 2>&1 | tail -40

echo
echo "== verify =="
nemoclaw "$NAME" status || true
