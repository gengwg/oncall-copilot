#!/usr/bin/env bash
# Tests for the promql/logql skill scripts against a stub server that, like a
# Grafana datasource proxy, answers only with the right bearer token.
#
# Usage: ./tests/query_scripts_test.sh
set -uo pipefail

cd "$(dirname "$0")/.." || exit
TMP=$(mktemp -d)
PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
TOKEN=test-grafana-token

PASS=0; FAIL=0

cleanup() {
  [ -n "${STUB_PID:-}" ] && kill "$STUB_PID" 2>/dev/null
  rm -rf "$TMP"
}
trap cleanup EXIT

ok()   { PASS=$((PASS+1)); printf '  ok   %s\n' "$1"; }
bad()  { FAIL=$((FAIL+1)); printf '  FAIL %s\n     %s\n' "$1" "$2"; }
check() { # check <name> <expected-substring> <actual>
  case "$3" in
    *"$2"*) ok "$1" ;;
    *) bad "$1" "expected to contain: $2"$'\n     got: '"$3" ;;
  esac
}

# Records each request's Authorization header (or "none") to $TMP/auth.log.
cat > "$TMP/stub.py" <<'EOF'
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

port, token, log = int(sys.argv[1]), sys.argv[2], sys.argv[3]

class H(BaseHTTPRequestHandler):
    def do_GET(self):
        auth = self.headers.get("Authorization")
        with open(log, "a") as f:
            f.write((auth or "none") + "\n")
        if auth != f"Bearer {token}":
            self.send_response(401); self.end_headers(); return
        if self.path.startswith("/api/v1/query"):
            body = {"data": {"result": [{"metric": {"job": "kubelet"}, "value": [0, "15"]}]}}
        elif self.path.startswith("/loki/api/v1/query_range"):
            body = {"data": {"result": [{"values": [["1700000000000000000", "xid 79 on gpu 3"]]}]}}
        else:
            self.send_response(404); self.end_headers(); return
        self.send_response(200); self.end_headers()
        self.wfile.write(json.dumps(body).encode())
    def log_message(self, *a): pass

HTTPServer(("127.0.0.1", port), H).serve_forever()
EOF
python3 "$TMP/stub.py" "$PORT" "$TOKEN" "$TMP/auth.log" &
STUB_PID=$!
for _ in $(seq 50); do
  curl -s -o /dev/null "http://127.0.0.1:$PORT/" && break
  sleep 0.1
done

URL="http://127.0.0.1:$PORT"
PROMQL=skills/promql-query/scripts/query.sh
LOGQL=skills/logql-query/scripts/query.sh

echo "== promql-query =="
out=$(PROMETHEUS_URL=$URL PROMETHEUS_TOKEN=$TOKEN $PROMQL instant 'up' 2>&1)
check "instant query with PROMETHEUS_TOKEN returns the series" '"job":"kubelet"' "$out"

: > "$TMP/auth.log"
if PROMETHEUS_URL=$URL $PROMQL instant 'up' >/dev/null 2>&1; then
  bad "no token against an auth-only server fails" "query.sh exited 0"
else
  ok "no token against an auth-only server fails"
fi
check "no token sends no Authorization header" "none" "$(cat "$TMP/auth.log")"

echo "== logql-query =="
out=$(LOKI_URL=$URL LOKI_TOKEN=$TOKEN $LOGQL tail '{job="x"}' 5 5 2>&1)
check "tail with LOKI_TOKEN returns the log line" "xid 79 on gpu 3" "$out"

: > "$TMP/auth.log"
LOKI_URL=$URL $LOGQL tail '{job="x"}' 5 5 >/dev/null 2>&1
check "no LOKI_TOKEN sends no Authorization header" "none" "$(cat "$TMP/auth.log")"

echo
echo "$PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
