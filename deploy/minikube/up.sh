#!/usr/bin/env bash
# Bring up the local demo environment on minikube:
# Prometheus + Alertmanager + Grafana + Loki + Alloy + demo-app.
# Usage: ./deploy/minikube/up.sh [relay-token]
set -euo pipefail

cd "$(dirname "$0")"
NS_OBS=observability
NS_DEMO=demo
RELAY_TOKEN="${1:-${RELAY_TOKEN:-copilot-relay-token}}"

echo "== minikube =="
minikube status >/dev/null 2>&1 || minikube start --driver=docker --cpus=4 --memory=6g

echo "== build demo-app image into minikube =="
eval "$(minikube docker-env)"
docker build -t demo-app:latest ../../demo-app
eval "$(minikube docker-env -u)"

echo "== namespaces =="
kubectl create namespace "$NS_OBS" --dry-run=client -o yaml | kubectl apply -f -
kubectl create namespace "$NS_DEMO" --dry-run=client -o yaml | kubectl apply -f -

echo "== relay token secret =="
kubectl -n "$NS_OBS" create secret generic relay-token \
  --from-literal=token="$RELAY_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "== helm repos =="
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null
helm repo add grafana https://grafana.github.io/helm-charts >/dev/null
helm repo update >/dev/null

echo "== kube-prometheus-stack =="
helm upgrade --install kps prometheus-community/kube-prometheus-stack \
  -n "$NS_OBS" -f values-kps.yaml --wait --timeout 5m

echo "== loki =="
helm upgrade --install loki grafana/loki \
  -n "$NS_OBS" -f values-loki.yaml --wait --timeout 5m

echo "== alloy (log collector) =="
helm upgrade --install alloy grafana/alloy \
  -n "$NS_OBS" -f values-alloy.yaml --wait --timeout 5m

echo "== demo-app =="
kubectl apply -f demo-app.yaml
kubectl apply -f prometheus-rules.yaml
kubectl -n "$NS_DEMO" rollout status deployment/demo-app --timeout=120s

echo
echo "Done. Access:"
echo "  Grafana:      kubectl -n $NS_OBS port-forward svc/kps-grafana 3000:80  (admin/admin)"
echo "  Prometheus:   kubectl -n $NS_OBS port-forward svc/kps-kube-prometheus-stack-prometheus 9090:9090"
echo "  Alertmanager: kubectl -n $NS_OBS port-forward svc/kps-kube-prometheus-stack-alertmanager 9093:9093"
echo "  Loki:         kubectl -n $NS_OBS port-forward svc/loki 3100:3100"
echo "  demo-app:     kubectl -n $NS_DEMO port-forward svc/demo-app 8080:8080"
echo
echo "Alertmanager webhooks -> http://host.minikube.internal:9099/alert (alert-relay on host)"
