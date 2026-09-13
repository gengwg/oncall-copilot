#!/usr/bin/env bash
# Integration tests for alert-relay/investigate.sh.
#
# Runs the real alert-relay binary and the real investigate.sh against stub
# Prometheus/Loki query scripts and a stub inference endpoint, so the failure
# modes that matter (auth drift, lost alerts, silent NO_REPLY) are exercised
# end to end rather than mocked away.
#
# Usage: ./tests/investigate_test.sh
set -uo pipefail

cd "$(dirname "$0")/.."
ROOT=$PWD
TMP=$(mktemp -d)
RELAY_PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
INFER_PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
TOKEN=test-relay-token

PASS=0; FAIL=0

cleanup() {
  [ -n "${RELAY_PID:-}" ] && kill "$RELAY_PID" 2>/dev/null
  [ -n "${INFER_PID:-}" ] && kill "$INFER_PID" 2>/dev/null
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

# --- stub workspace: query scripts investigate.sh shells out to ---
mkdir -p "$TMP/ws/skills/promql-query/scripts" "$TMP/ws/skills/logql-query/scripts" "$TMP/ws/memory/runbooks"
cat > "$TMP/ws/skills/promql-query/scripts/query.sh" <<'EOF'
#!/usr/bin/env bash
# Stub: emits one result object per query, or nothing when STUB_PROM_EMPTY=1
# (which is what a real instant query returns when the target is gone).
[ "${STUB_PROM_EMPTY:-0}" = "1" ] && exit 0
# 0/0 over a window with no traffic: Prometheus returns a real NaN sample.
[ "${STUB_PROM_NAN:-0}" = "1" ] && { echo '{"value":"NaN"}'; exit 0; }
case "$2" in
  *heap*) echo '{"value":"104857600"}' ;;
  *latency_mode*) echo '{"value":"1"}' ;;
  *) echo '{"value":"1"}' ;;
esac
EOF
cat > "$TMP/ws/skills/logql-query/scripts/query.sh" <<'EOF'
#!/usr/bin/env bash
printf '%s\t%s\n' "2026-09-12T00:00:00Z" '{"msg":"request failed","err":"upstream dial timeout"}'
EOF
chmod +x "$TMP/ws/skills"/*/scripts/query.sh
echo "# demo-app runbook" > "$TMP/ws/memory/runbooks/demo-app.md"

# --- stub inference endpoint (stands in for inference.local) ---
cat > "$TMP/infer.py" <<'EOF'
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get('Content-Length', 0)))
        body = json.dumps({"choices": [{"message": {"content":
            "ROOT CAUSE: Injected upstream dial timeouts are failing roughly half of all requests reaching the service, "
            "while the process itself remains entirely healthy, which rules out local resource exhaustion and points "
            "squarely at an unreachable downstream dependency somewhere beyond the pod boundary.\n"
            "ACTION: Run /chaos/stop on demo-app, then verify the Kubernetes service endpoints and NetworkPolicy rules "
            "for every downstream dependency it calls."}}]}).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a): pass
HTTPServer(('127.0.0.1', int(sys.argv[1])), H).serve_forever()
EOF
python3 "$TMP/infer.py" "$INFER_PORT" & INFER_PID=$!

# --- real relay ---
(cd alert-relay && CGO_ENABLED=0 go build -o "$TMP/alert-relay" .) || { echo "relay build failed"; exit 1; }
"$TMP/alert-relay" -addr "127.0.0.1:$RELAY_PORT" -token "$TOKEN" > "$TMP/relay.out" 2>&1 & RELAY_PID=$!

for _ in $(seq 50); do
  curl -sf -m 1 "http://127.0.0.1:$RELAY_PORT/healthz" >/dev/null && break
  sleep 0.1
done

post_alert() { # post_alert <alertname> [status]
  curl -sf -m 2 -X POST "http://127.0.0.1:$RELAY_PORT/alert" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d "$(jq -n --arg n "$1" --arg s "${2:-firing}" \
      '{status:$s, alerts:[{status:$s, labels:{alertname:$n, severity:"critical", service:"demo-app"},
        annotations:{summary:("summary for " + $n)}, startsAt:"2026-09-12T00:00:00Z"}]}')" >/dev/null
}

run_investigate() { # run_investigate [env assignments...] ; sets OUT/ERR/RC
  local out err rc
  out=$(mktemp); err=$(mktemp)
  env WORKSPACE="$TMP/ws" \
      ALERT_RELAY_URL="http://127.0.0.1:$RELAY_PORT" \
      RELAY_TOKEN="$TOKEN" \
      NEBIUS_API_KEY=stub-key \
      INFERENCE_URL="http://127.0.0.1:$INFER_PORT/v1/chat/completions" \
      TAVILY_API_KEY= \
      "$@" \
      bash "$ROOT/alert-relay/investigate.sh" > "$out" 2> "$err"
  rc=$?
  OUT=$(cat "$out"); ERR=$(cat "$err"); RC=$rc
  rm -f "$out" "$err"
}

echo "investigate.sh integration tests"

# 1. Empty queue is a normal quiet tick, not an error.
run_investigate
check "empty queue -> NO_REPLY" "NO_REPLY" "$OUT"
[ "$RC" -eq 0 ] && ok "empty queue -> exit 0" || bad "empty queue -> exit 0" "got rc=$RC"

# 2. A firing alert produces a brief and a report.
post_alert DemoHighErrorRate
run_investigate
check "firing alert -> brief names the alert" "ALERT: DemoHighErrorRate" "$OUT"
check "firing alert -> brief carries the cause" "Likely cause: Injected upstream dial timeouts" "$OUT"
if [ -f "$TMP/ws/memory/incidents/$(date -u +%Y-%m-%d)-DemoHighErrorRate.md" ]; then
  ok "firing alert -> incident report written"
else
  bad "firing alert -> incident report written" "no report in $TMP/ws/memory/incidents: $(ls "$TMP/ws/memory/incidents" 2>&1)"
fi

# 3. Token drift must be loud. Before the fix this was indistinguishable from
#    an empty queue and alerts silently piled up in the relay forever.
post_alert DemoServiceDown
run_investigate RELAY_TOKEN=wrong-token
check "bad token -> names the 401" "401" "$ERR"
[ "$RC" -ne 0 ] && ok "bad token -> non-zero exit" || bad "bad token -> non-zero exit" "got rc=0, which looks healthy to the cron"

# 4. The alert from test 3 must still be in the queue: a rejected drain must
#    not consume it.
run_investigate
check "alert survives a rejected drain" "ALERT: DemoServiceDown" "$OUT"

# 5. Unreachable relay is also loud.
run_investigate ALERT_RELAY_URL="http://127.0.0.1:1"
check "relay down -> diagnostic on stderr" "unreachable" "$ERR"
[ "$RC" -ne 0 ] && ok "relay down -> non-zero exit" || bad "relay down -> non-zero exit" "got rc=0"

# 6. Missing inference credentials must not silently produce a blank report.
post_alert DemoHighErrorRate
run_investigate NEBIUS_API_KEY=
check "missing NEBIUS_API_KEY -> diagnostic" "NEBIUS_API_KEY is unset" "$ERR"
[ "$RC" -ne 0 ] && ok "missing NEBIUS_API_KEY -> non-zero exit" || bad "missing NEBIUS_API_KEY -> non-zero exit" "got rc=0"

# 7. Every firing alert in one drained batch gets investigated. The drain is
#    destructive, so anything skipped here is lost for good.
curl -sf -m 2 -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$RELAY_PORT/alerts" >/dev/null  # clear
post_alert DemoHighErrorRate
post_alert DemoHighLatency
post_alert DemoMemoryLeak
run_investigate
for a in DemoHighErrorRate DemoHighLatency DemoMemoryLeak; do
  check "batch of 3 -> $a briefed" "ALERT: $a" "$OUT"
done

# 8. Resolved alerts are skipped without emitting a brief.
curl -sf -m 2 -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$RELAY_PORT/alerts" >/dev/null
post_alert DemoHighErrorRate resolved
run_investigate
check "resolved-only batch -> NO_REPLY" "NO_REPLY" "$OUT"

# 9. alertname is an untrusted label that lands in a path.
curl -sf -m 2 -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$RELAY_PORT/alerts" >/dev/null
post_alert '../../escaped'
run_investigate
if [ -e "$TMP/ws/escaped.md" ] || [ -e "$TMP/escaped.md" ]; then
  bad "path traversal in alertname contained" "report escaped \$INCIDENTS"
else
  ok "path traversal in alertname contained"
fi

# 10. An unwritable lock must degrade to running unlocked, not to a silent
#     NO_REPLY on every tick.
curl -sf -m 2 -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$RELAY_PORT/alerts" >/dev/null
post_alert DemoHighErrorRate
rm -f "$TMP/ws/.investigate.lock"   # an existing file stays writable in a ro dir
chmod 500 "$TMP/ws"
run_investigate
chmod 700 "$TMP/ws"
check "unwritable lock -> warns" "running unlocked" "$ERR"
check "unwritable lock -> still investigates" "ALERT: DemoHighErrorRate" "$OUT"

# 11. A query returning no series must read as n/a, not as a blank field.
curl -sf -m 2 -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$RELAY_PORT/alerts" >/dev/null
post_alert DemoServiceDown
run_investigate STUB_PROM_EMPTY=1
check "no series -> evidence reads n/a" "err_ratio=n/a" "$OUT"

# 12. A ratio over a window with no traffic is NaN, which is a real sample and
#     not an empty result, so it used to reach the Telegram brief verbatim.
curl -sf -m 2 -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$RELAY_PORT/alerts" >/dev/null
post_alert DemoHighErrorRate
run_investigate STUB_PROM_NAN=1
case "$OUT" in
  *NaN*) bad "NaN folded to n/a in the brief" "brief still carries NaN: $OUT" ;;
  *) ok "NaN folded to n/a in the brief" ;;
esac
check "NaN -> err_ratio reads n/a" "err_ratio=n/a" "$OUT"

# 13. Long analysis must be clipped at a word boundary, not mid-word.
curl -sf -m 2 -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$RELAY_PORT/alerts" >/dev/null
post_alert DemoHighErrorRate
run_investigate
cause_line=$(printf '%s' "$OUT" | rg '^Likely cause:' | head -1)
sugg_line=$(printf '%s' "$OUT" | rg '^Suggested:' | head -1)
# The stub analysis exceeds both caps, so each field MUST be elided. head -c
# cuts mid-word and adds no marker, so requiring the marker catches it.
for pair in "cause:$cause_line" "action:$sugg_line"; do
  name=${pair%%:*}; line=${pair#*:}
  case "$line" in
    *...)
      stem=${line%...}
      case "$stem" in
        *" ") bad "$name clipped at a word boundary" "trailing space before elision: $line" ;;
        *) ok "$name clipped at a word boundary" ;;
      esac ;;
    *) bad "$name clipped at a word boundary" "no elision marker; field was cut mid-word or not at all: $line" ;;
  esac
done

echo
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
