#!/usr/bin/env bash
# Deploy the incident dashboard to Nebius Serverless Endpoints.
# Prereqs: nebius CLI profile configured (nebius profile create) with a default
# project, and docker logged into the Nebius registry
# (nebius registry configure-helper).
#
# Usage: ./deploy/dashboard/deploy.sh
set -euo pipefail

cd "$(dirname "$0")"
NEBIUS="$HOME/.nebius/bin/nebius"
NAME=oncall-copilot-dashboard
IMAGE_TAG="${IMAGE_TAG:-v1}"

echo "== project =="
PROJECT_ID=$($NEBIUS config get parent-id 2>/dev/null || $NEBIUS profile show --format json 2>/dev/null | jq -r '.parentId // .parent_id // empty')
[ -z "$PROJECT_ID" ] && { echo "no default project; run: nebius profile create"; exit 1; }
echo "project: $PROJECT_ID"

echo "== registry =="
REGISTRY_ID=$($NEBIUS registry list --format json 2>/dev/null | jq -r '.items[0].metadata.id // empty')
if [ -z "$REGISTRY_ID" ]; then
  echo "creating registry..."
  REGISTRY_ID=$($NEBIUS registry create --name oncall-copilot --format json | jq -r '.metadata.id')
fi
REGISTRY=$($NEBIUS registry get "$REGISTRY_ID" --format json | jq -r '.status.url // .spec.url // empty')
[ -z "$REGISTRY" ] && REGISTRY="cr.ai.cloud.nebius.dev/$REGISTRY_ID"
echo "registry: $REGISTRY"

echo "== build + push image =="
IMAGE="$REGISTRY/dashboard:$IMAGE_TAG"
docker build -t "$IMAGE" .
docker push "$IMAGE"

echo "== subnet =="
SUBNET_ID=$($NEBIUS vpc subnet list --format json 2>/dev/null | jq -r '.items[0].metadata.id // empty')
[ -z "$SUBNET_ID" ] && { echo "no subnet found; create one in the console (VPC -> Subnets)"; exit 1; }
echo "subnet: $SUBNET_ID"

echo "== endpoint =="
if $NEBIUS ai endpoint get-by-name --name "$NAME" >/dev/null 2>&1; then
  echo "endpoint exists; deleting to recreate with fresh image"
  ENDPOINT_ID=$($NEBIUS ai endpoint get-by-name --name "$NAME" --format json | jq -r '.metadata.id')
  $NEBIUS ai endpoint delete "$ENDPOINT_ID"
  sleep 10
fi

$NEBIUS ai endpoint create \
  --name "$NAME" \
  --image "$IMAGE" \
  --platform cpu-d3 \
  --preset 1vcpu-4gb \
  --container-port 8080 \
  --public \
  --subnet-id "$SUBNET_ID"

sleep 5
ENDPOINT_ID=$($NEBIUS ai endpoint get-by-name --name "$NAME" --format json | jq -r '.metadata.id')
echo
echo "== waiting for managed URL =="
for i in $(seq 1 30); do
  URL=$($NEBIUS ai endpoint get "$ENDPOINT_ID" --format json 2>/dev/null | jq -r '.status.public_endpoints[]? // empty' | head -1)
  [ -n "$URL" ] && break
  sleep 10
done
echo
echo "Dashboard URL: $URL"
echo "Add this to the Devpost submission as the working demo URL."
