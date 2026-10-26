#!/usr/bin/env bash
# Tests for alert-relay/irm-poll.sh against a stub IRM alert-groups API and a
# stub investigate.sh that records what it was asked to investigate.
#
# Usage: ./tests/irm_poll_test.sh
set -uo pipefail

cd "$(dirname "$0")/.." || exit
ROOT=$PWD
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
lacks() { # lacks <name> <unexpected-substring> <actual>
  case "$3" in
    *"$2"*) bad "$1" "should not contain: $2"$'\n     got: '"$3" ;;
    *) ok "$1" ;;
  esac
}

# Serves $TMP/groups.json as the alert-groups list; 401 without the token or
# the X-Grafana-URL header IRM requires.
cat > "$TMP/stub.py" <<'EOF'
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
port, token, groups, beats = int(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4]
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        # IRM heartbeat URLs carry their secret in the path, no bearer token.
        if self.path.startswith("/heartbeat/"):
            with open(beats, "a") as f:
                f.write(self.path + "\n")
            self.send_response(200); self.end_headers(); return
        if self.headers.get("Authorization") != f"Bearer {token}" or not self.headers.get("X-Grafana-URL"):
            self.send_response(401); self.end_headers(); return
        if not self.path.startswith("/api/v1/alert_groups/"):
            self.send_response(404); self.end_headers(); return
        with open(groups) as f:
            body = {"count": 0, "next": None, "results": json.load(f)}
        self.send_response(200); self.end_headers()
        self.wfile.write(json.dumps(body).encode())
    def log_message(self, *a): pass
HTTPServer(("127.0.0.1", port), H).serve_forever()
EOF
echo '[]' > "$TMP/groups.json"
: > "$TMP/heartbeats"
python3 "$TMP/stub.py" "$PORT" "$TOKEN" "$TMP/groups.json" "$TMP/heartbeats" &
STUB_PID=$!
for _ in $(seq 50); do
  curl -s -o /dev/null "http://127.0.0.1:$PORT/" && break
  sleep 0.1
done

# Stub investigator: records the alert line, prints a one-line brief. An
# alertname starting with ModelDown adds a Degraded line, NoBrief prints nothing.
cat > "$TMP/investigate.sh" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$1" >> "$TMP/investigated"
a=\$(printf '%s' "\$1" | jq -r .alertname)
case "\$a" in NoBrief*) exit 0 ;; esac
printf 'BRIEF %s on %s\n' "\$a" "\$(printf '%s' "\$1" | jq -r .labels.cluster)"
case "\$a" in ModelDown*) echo "Degraded: model call failed" ;; esac
EOF
chmod +x "$TMP/investigate.sh"
: > "$TMP/investigated"

group() { # group <id> <number> <state> <alertname> <cluster> <severity> [node]: one IRM alert group
  jq -n --arg id "$1" --argjson n "$2" --arg st "$3" --arg a "$4" --arg c "$5" --arg sev "$6" --arg node "${7:-gpu-node-1}" '{
    id:$id, alert_group_number:$n, state:$st, created_at:"2026-10-25T09:04:00Z",
    permalinks:{slack:("https://slack.example/archives/C1/p" + ($n|tostring))},
    last_alert:{payload:{commonLabels:{severity:$sev}, alerts:[{status:"firing",
      labels:{alertname:$a, cluster:$c, node:$node, severity:$sev, condition:"GpuXidCritical", customer:"acme"},
      annotations:{summary:("summary " + $a), description:("description " + $a)},
      startsAt:"2026-10-25T09:04:00Z"}]}}}'
}
set_groups() { # set_groups <group-json>...: newest first, as IRM lists them
  printf '%s\n' "$@" | jq -s . > "$TMP/groups.json"
}

run_poll() { # run_poll [env assignments...]; sets OUT/ERR/RC
  local out err
  out=$(mktemp); err=$(mktemp)
  env IRM_API_URL="http://127.0.0.1:$PORT" GRAFANA_URL=https://stack.example GRAFANA_TOKEN="$TOKEN" \
      STATE_DIR="$TMP/state" INVESTIGATE="$TMP/investigate.sh" "$@" \
      bash "$ROOT/alert-relay/irm-poll.sh" > "$out" 2> "$err"
  RC=$?
  OUT=$(cat "$out"); ERR=$(cat "$err")
  rm -f "$out" "$err"
}
LOG="$TMP/state/briefs.log"

echo "irm-poll.sh tests"

# 1. First run records what is already there instead of investigating a backlog.
set_groups "$(group G2 102 new XidCritical c1 critical)" "$(group G1 101 acknowledged NvlinkDown c1 warning)"
run_poll
[ "$RC" -eq 0 ] && ok "bootstrap -> exit 0" || bad "bootstrap -> exit 0" "rc=$RC err=$ERR"
check "bootstrap -> says how many it recorded" "bootstrapped 2" "$OUT$ERR"
[ -s "$TMP/investigated" ] && bad "bootstrap -> investigates nothing" "$(cat "$TMP/investigated")" || ok "bootstrap -> investigates nothing"

# 2. A new group is investigated once and its brief logged with the IRM number.
set_groups "$(group G3 103 new GpuFellOff c2 critical)" "$(group G2 102 new XidCritical c1 critical)" "$(group G1 101 acknowledged NvlinkDown c1 warning)"
run_poll
check "new group -> investigated" '"alertname":"GpuFellOff"' "$(cat "$TMP/investigated")"
lacks "new group -> already-seen groups not re-investigated" '"XidCritical"' "$(cat "$TMP/investigated")"
check "new group -> brief in the shadow log" "BRIEF GpuFellOff on c2" "$(cat "$LOG")"
check "new group -> log entry carries the IRM number" "IRM #103" "$(cat "$LOG")"
check "new group -> line carries the node condition" '"condition":"GpuXidCritical"' "$(cat "$TMP/investigated")"
lacks "new group -> customer label not passed on" '"customer"' "$(cat "$TMP/investigated")"
check "new group -> line carries the IRM number" '"irm_number":103' "$(cat "$TMP/investigated")"
check "new group -> line carries the Slack permalink" '"irm_url":"https://slack.example/archives/C1/p103"' "$(cat "$TMP/investigated")"

# 3. Polling again changes nothing.
: > "$TMP/investigated"
run_poll
[ -s "$TMP/investigated" ] && bad "repeat poll -> nothing re-investigated" "$(cat "$TMP/investigated")" || ok "repeat poll -> nothing re-investigated"

# 4. Resolved-before-poll and info-severity groups are recorded but not investigated.
set_groups "$(group G5 105 new DiskInfo c3 info)" "$(group G4 104 resolved ProbeLoss c3 warning)" "$(group G3 103 new GpuFellOff c2 critical)"
run_poll
[ -s "$TMP/investigated" ] && bad "resolved and info -> not investigated" "$(cat "$TMP/investigated")" || ok "resolved and info -> not investigated"
check "resolved -> skip is logged" "IRM #104 skipped: resolved before investigation" "$(cat "$LOG")"

# 5. The same alert on the same node inside the window is one problem: one brief.
set_groups "$(group G7 107 new NodeTelemetryLost c4 warning n1)" "$(group G6 106 new NodeTelemetryLost c4 warning n1)" "$(group G5 105 new DiskInfo c3 info)"
run_poll
n=$(grep -c NodeTelemetryLost "$TMP/investigated")
[ "$n" -eq 1 ] && ok "same node repeat -> investigated once" || bad "same node repeat -> investigated once" "investigated $n times"
check "same node repeat -> logged as a repeat" "skipped: repeat on the same node" "$(cat "$LOG")"

# 6. The same alert on different nodes is separate hardware: each gets a brief,
#    up to STORM_MAX per alert and cluster, then the rest are a storm.
: > "$TMP/investigated"
args=()
for i in 6 5 4 3 2 1; do args+=("$(group "H$i" $((200 + i)) new GpuNvlinkDown c5 warning "node-$i")"); done
set_groups "${args[@]}"
run_poll STORM_MAX=3
n=$(grep -c GpuNvlinkDown "$TMP/investigated")
[ "$n" -eq 3 ] && ok "different nodes -> each investigated up to STORM_MAX" || bad "different nodes -> each investigated up to STORM_MAX" "investigated $n times"
check "different nodes -> past the cap is a storm skip" "skipped: storm" "$(cat "$LOG")"

# 7. IRM refusing the token is an error, not an empty poll, and loses no state.
cp "$TMP/state/seen" "$TMP/seen.before"
run_poll GRAFANA_TOKEN=wrong
[ "$RC" -ne 0 ] && ok "IRM 401 -> nonzero exit" || bad "IRM 401 -> nonzero exit" "rc=$RC"
check "IRM 401 -> says so on stderr" "HTTP 401" "$ERR"
cmp -s "$TMP/state/seen" "$TMP/seen.before" && ok "IRM 401 -> seen state unchanged" || bad "IRM 401 -> seen state unchanged" "seen file changed"

# 8. Every successful poll pings the heartbeat, so IRM alerts when they stop.
#    A refused token is not a success and must stop the pings too.
HB="http://127.0.0.1:$PORT/heartbeat/s3cret"
beats() { wc -l < "$TMP/heartbeats" | tr -d ' '; }
run_poll HEARTBEAT_URL="$HB"
[ "$(beats)" = 1 ] && ok "successful poll -> one heartbeat" || bad "successful poll -> one heartbeat" "beats=$(beats) rc=$RC"
run_poll HEARTBEAT_URL="$HB" STATE_DIR="$TMP/state-fresh"
[ "$(beats)" = 2 ] && ok "bootstrap poll -> heartbeat" || bad "bootstrap poll -> heartbeat" "beats=$(beats) rc=$RC"
run_poll HEARTBEAT_URL="$HB" GRAFANA_TOKEN=wrong
[ "$(beats)" = 2 ] && ok "IRM 401 -> no heartbeat" || bad "IRM 401 -> no heartbeat" "beats=$(beats)"

# 9. An unreachable heartbeat endpoint is a warning, not a failed poll.
run_poll HEARTBEAT_URL="http://127.0.0.1:9/heartbeat/x"
[ "$RC" -eq 0 ] && ok "heartbeat unreachable -> poll still succeeds" || bad "heartbeat unreachable -> poll still succeeds" "rc=$RC"
check "heartbeat unreachable -> warns" "heartbeat ping failed" "$ERR"
lacks "heartbeat unreachable -> secret not echoed" "/heartbeat/x" "$ERR"

# 10. A degraded brief (model or evidence failure) stops the heartbeat, and it
#     stays stopped on quiet polls until a later brief comes out healthy.
: > "$TMP/heartbeats"
set_groups "$(group D1 301 new ModelDownA c10 critical)"
run_poll HEARTBEAT_URL="$HB"
[ "$(beats)" = 0 ] && ok "degraded brief -> no heartbeat" || bad "degraded brief -> no heartbeat" "beats=$(beats)"
[ "$RC" -eq 0 ] && ok "degraded brief -> poll still succeeds" || bad "degraded brief -> poll still succeeds" "rc=$RC"
check "degraded brief -> says why the heartbeat is held" "model call failed" "$ERR"
run_poll HEARTBEAT_URL="$HB"
[ "$(beats)" = 0 ] && ok "degraded, quiet poll -> still no heartbeat" || bad "degraded, quiet poll -> still no heartbeat" "beats=$(beats)"
set_groups "$(group D2 302 new HealthyAgain c11 critical)" "$(group D1 301 new ModelDownA c10 critical)"
run_poll HEARTBEAT_URL="$HB"
[ "$(beats)" = 1 ] && ok "healthy brief -> heartbeat resumes" || bad "healthy brief -> heartbeat resumes" "beats=$(beats)"

# 11. An investigation that prints no brief at all counts as degraded.
set_groups "$(group D3 303 new NoBriefX c12 critical)" "$(group D2 302 new HealthyAgain c11 critical)"
run_poll HEARTBEAT_URL="$HB"
[ "$(beats)" = 1 ] && ok "no brief -> no heartbeat" || bad "no brief -> no heartbeat" "beats=$(beats)"
check "no brief -> reason given" "produced no brief" "$ERR"

echo
echo "$PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
