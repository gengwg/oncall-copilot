#!/usr/bin/env bash
# Keep kubectl port-forwards alive for the demo env.
# Usage: ./deploy/minikube/forwards.sh start|stop
set -uo pipefail

PIDFILE=/tmp/opencode/copilot-forwards.pids

start() {
  mkdir -p /tmp/opencode
  stop 2>/dev/null
  local specs=(
    "observability svc/kps-kube-prometheus-stack-prometheus 19090:9090"
    "observability svc/kps-kube-prometheus-stack-alertmanager 19093:9093"
    "observability svc/loki 13100:3100"
    "observability svc/kps-grafana 13000:80"
    "demo svc/demo-app 18080:8080"
  )
  : > "$PIDFILE"
  for s in "${specs[@]}"; do
    read -r ns svc ports <<< "$s"
    setsid nohup bash -c "while true; do kubectl -n '$ns' port-forward '$svc' '$ports' >/dev/null 2>&1; sleep 2; done" >/dev/null 2>&1 &
    echo $! >> "$PIDFILE"
  done
  disown -a 2>/dev/null || true
  sleep 3
  echo "forwards up: prometheus=19090 alertmanager=19093 loki=13100 grafana=13000 demo-app=18080"
}

stop() {
  [ -f "$PIDFILE" ] || return 0
  while read -r pid; do kill "$pid" 2>/dev/null; done < "$PIDFILE"
  rm -f "$PIDFILE"
}

case "${1:-start}" in
  start) start ;;
  stop) stop ;;
  *) echo "usage: $0 start|stop" >&2; exit 2 ;;
esac
