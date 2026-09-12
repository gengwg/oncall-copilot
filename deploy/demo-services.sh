#!/usr/bin/env bash
# Start/stop the host-side services for the on-call copilot demo:
#   - kubectl port-forwards (Prometheus/Loki/Alertmanager/Grafana/demo-app)
#   - alert-relay (Alertmanager webhook target + queue for the sandbox)
#   - incident sync (sandbox -> dashboard dir)
# Usage: ./deploy/demo-services.sh start|stop|status
set -uo pipefail

cd "$(dirname "$0")/.."
RUN=/tmp/opencode/oncall-demo
RELAY_LOG=/tmp/opencode/relay-host.out
INCIDENT_DIR=/tmp/opencode/dashboard-incidents
DASH_PORT="${DASH_PORT:-18099}"
mkdir -p "$RUN" "$INCIDENT_DIR"

# shellcheck disable=SC1091
source "$(dirname "$0")/lib.sh"

start() {
  echo "== port-forwards =="
  deploy/minikube/forwards.sh start

  echo "== alert-relay =="
  if ! curl -sf -m 2 http://172.18.0.1:9099/healthz >/dev/null 2>&1; then
    (cd alert-relay && CGO_ENABLED=0 go build -ldflags="-s -w" -o "$RUN/alert-relay" .)
    setsid nohup "$RUN/alert-relay" -addr 0.0.0.0:9099 -token "$(relay_token)" \
      > "$RELAY_LOG" 2>&1 < /dev/null &
    echo $! > "$RUN/relay.pid"
    sleep 2
  fi
  curl -sf -m 2 http://172.18.0.1:9099/healthz >/dev/null && echo "relay: ok" || echo "relay: FAILED"

  echo "== incident sync =="
  if [ ! -f "$RUN/sync.pid" ] || ! kill -0 "$(cat "$RUN/sync.pid" 2>/dev/null)" 2>/dev/null; then
    setsid nohup bash deploy/sync-incidents.sh "$INCIDENT_DIR" > "$RUN/sync.log" 2>&1 < /dev/null &
    echo $! > "$RUN/sync.pid"
  fi
  echo "sync: ok"

  echo "== dashboard =="
  if ! curl -sf -m 2 "localhost:$DASH_PORT/healthz" >/dev/null 2>&1; then
    (cd deploy/dashboard && CGO_ENABLED=0 go build -o "$RUN/dashboard" .)
    setsid nohup "$RUN/dashboard" -addr ":$DASH_PORT" -incidents "$INCIDENT_DIR" \
      > "$RUN/dash.log" 2>&1 < /dev/null &
    echo $! > "$RUN/dash.pid"
    sleep 1
  fi
  curl -sf -m 2 "localhost:$DASH_PORT/healthz" >/dev/null && echo "dashboard: http://localhost:$DASH_PORT" || echo "dashboard: FAILED"

  echo "== public tunnel (cloudflare) =="
  CLOUDFLARED="${CLOUDFLARED:-/tmp/opencode/cloudflared}"
  if command -v "$CLOUDFLARED" >/dev/null 2>&1; then
    if [ ! -f "$RUN/tunnel.pid" ] || ! kill -0 "$(cat "$RUN/tunnel.pid" 2>/dev/null)" 2>/dev/null; then
      setsid nohup "$CLOUDFLARED" tunnel --url "http://localhost:$DASH_PORT" --no-autoupdate \
        > "$RUN/tunnel.log" 2>&1 < /dev/null &
      echo $! > "$RUN/tunnel.pid"
      sleep 8
    fi
    URL=$(grep -oE "https://[a-z0-9-]+\.trycloudflare\.com" "$RUN/tunnel.log" | head -1)
    echo "tunnel: ${URL:-pending (see $RUN/tunnel.log)}"
  else
    echo "cloudflared not found; dashboard is local-only"
  fi
}

stop() {
  deploy/minikube/forwards.sh stop 2>/dev/null
  for p in relay sync dash tunnel; do
    [ -f "$RUN/$p.pid" ] && kill "$(cat "$RUN/$p.pid")" 2>/dev/null && rm -f "$RUN/$p.pid"
  done
  echo "stopped"
}

status() {
  echo "forwards:"; curl -sf -m 2 localhost:19090/-/healthy >/dev/null && echo "  prom ok" || echo "  prom DOWN"
  echo "relay:"; curl -sf -m 2 http://172.18.0.1:9099/healthz >/dev/null && echo "  ok" || echo "  DOWN"
  echo "dashboard:"; curl -sf -m 2 "localhost:$DASH_PORT/healthz" >/dev/null && echo "  http://localhost:$DASH_PORT" || echo "  DOWN"
  [ -f "$RUN/tunnel.log" ] && echo "tunnel: $(grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' "$RUN/tunnel.log" | head -1)"
  echo "cron:"; nemoclaw oncall exec -- sh -c 'openclaw cron list --json 2>/dev/null | python3 -c "import sys,json; j=json.load(sys.stdin)[\"jobs\"][0]; print(\"  \", j[\"name\"], j[\"state\"].get(\"lastRunStatus\"))"' 2>/dev/null | grep -v "Active gateway"
  echo "cron:"; nemoclaw oncall exec -- sh -c 'openclaw cron list --json 2>/dev/null | python3 -c "import sys,json; j=json.load(sys.stdin)[\"jobs\"][0]; print(\"  \", j[\"name\"], j[\"state\"].get(\"lastRunStatus\"))"' 2>/dev/null | grep -v "Active gateway"
}

case "${1:-status}" in
  start) start ;;
  stop) stop ;;
  status) status ;;
  *) echo "usage: $0 start|stop|status" >&2; exit 2 ;;
esac
