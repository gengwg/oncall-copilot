#!/usr/bin/env bash
# Tests for alert-relay/poll.sh (and its pagers/ adapters) against a stub IRM alert-groups API and a
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
case "\$a" in DcDraft*) printf 'Verdict: hold - node is cordoned\nFault: real - Xid 79 in dmesg\nFailure mode: hardware (related: none)\nDC draft: $TMP/drafts/x.md\n' ;; esac
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
      bash "$ROOT/alert-relay/${POLL_SCRIPT:-poll.sh}" > "$out" 2> "$err"
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
check "new group -> line carries the IRM number" '"ref":"103"' "$(cat "$TMP/investigated")"
check "new group -> line carries the Slack permalink" '"url":"https://slack.example/archives/C1/p103"' "$(cat "$TMP/investigated")"

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

# 10. One degraded brief can be an upstream blip and still pings. Two in a row
#     (model or evidence failure) stop the heartbeat, which stays stopped on
#     quiet polls until a later brief comes out healthy.
: > "$TMP/heartbeats"
set_groups "$(group D1 301 new ModelDownA c10 critical)"
run_poll HEARTBEAT_URL="$HB"
[ "$(beats)" = 1 ] && ok "one degraded brief -> still pings" || bad "one degraded brief -> still pings" "beats=$(beats)"
check "one degraded brief -> noted" "1 degraded brief in a row" "$ERR"
set_groups "$(group D2 302 new ModelDownB c11 critical)" "$(group D1 301 new ModelDownA c10 critical)"
run_poll HEARTBEAT_URL="$HB"
[ "$(beats)" = 1 ] && ok "two degraded in a row -> no heartbeat" || bad "two degraded in a row -> no heartbeat" "beats=$(beats)"
[ "$RC" -eq 0 ] && ok "two degraded in a row -> poll still succeeds" || bad "two degraded in a row -> poll still succeeds" "rc=$RC"
check "two degraded in a row -> says why the heartbeat is held" "model call failed" "$ERR"
run_poll HEARTBEAT_URL="$HB"
[ "$(beats)" = 1 ] && ok "held, quiet poll -> still no heartbeat" || bad "held, quiet poll -> still no heartbeat" "beats=$(beats)"
set_groups "$(group D3 303 new HealthyAgain c12 critical)" "$(group D2 302 new ModelDownB c11 critical)"
run_poll HEARTBEAT_URL="$HB"
[ "$(beats)" = 2 ] && ok "healthy brief -> heartbeat resumes" || bad "healthy brief -> heartbeat resumes" "beats=$(beats)"

# A healthy brief resets the count: one later failure does not hold.
set_groups "$(group D4 304 new ModelDownC c13 critical)" "$(group D3 303 new HealthyAgain c12 critical)"
run_poll HEARTBEAT_URL="$HB"
[ "$(beats)" = 3 ] && ok "isolated failure after recovery -> still pings" || bad "isolated failure after recovery -> still pings" "beats=$(beats)"

# 11. An investigation that prints no brief at all counts as degraded.
set_groups "$(group D5 305 new NoBriefX c14 critical)" "$(group D4 304 new ModelDownC c13 critical)"
run_poll HEARTBEAT_URL="$HB"
[ "$(beats)" = 3 ] && ok "no brief after a degraded one -> no heartbeat" || bad "no brief after a degraded one -> no heartbeat" "beats=$(beats)"
check "no brief -> reason given" "produced no brief" "$ERR"

# 12. With OUTBOX_DIR set, every brief becomes one JSON event the Slack agent
#     can read; skipped groups get none, and no half-written file is left.
OB="$TMP/outbox"
[ -e "$TMP/state/outbox" ] && bad "OUTBOX_DIR unset -> no outbox written" "found $TMP/state/outbox" || ok "OUTBOX_DIR unset -> no outbox written"
set_groups "$(group E2 402 resolved ProbeLoss c20 warning)" "$(group E1 401 new DcDraftXid c20 critical gpu-7)" "$(group D5 305 new NoBriefX c14 critical)"
run_poll OUTBOX_DIR="$OB"
[ -f "$OB/E1.json" ] && ok "brief -> event file named by IRM id" || bad "brief -> event file named by IRM id" "$(ls "$OB" 2>&1)"
[ -e "$OB/E2.json" ] && bad "skipped group -> no event" "E2.json exists" || ok "skipped group -> no event"
ev=$(cat "$OB/E1.json" 2>/dev/null)
jq -e '.irm_id == "E1" and .irm_number == 401 and .alertname == "DcDraftXid" and .cluster == "c20" and .node == "gpu-7" and .severity == "critical"' <<< "$ev" >/dev/null && ok "event -> identity fields" || bad "event -> identity fields" "$ev"
jq -e '.fault == "real" and .verdict == "hold" and .failure_mode == "hardware" and .degraded == null' <<< "$ev" >/dev/null && ok "event -> verdict fields from the brief" || bad "event -> verdict fields from the brief" "$ev"
jq -e --arg d "$TMP/drafts/x.md" '.dc_draft == $d and (.brief | contains("BRIEF DcDraftXid")) and .starts_at == "2026-10-25T09:04:00Z" and (.irm_url | startswith("https://slack.example"))' <<< "$ev" >/dev/null && ok "event -> draft path, brief, start time, link" || bad "event -> draft path, brief, start time, link" "$ev"
[ -z "$(ls "$OB"/*.tmp 2>/dev/null)" ] && ok "event -> no temp file left" || bad "event -> no temp file left" "$(ls "$OB")"
set_groups "$(group E3 403 new ModelDownE c21 critical)" "$(group E1 401 new DcDraftXid c20 critical gpu-7)"
run_poll OUTBOX_DIR="$OB"
jq -e '.degraded == "model call failed" and .fault == "unknown" and .dc_draft == null' "$OB/E3.json" >/dev/null && ok "degraded brief -> event says why, verdicts unknown" || bad "degraded brief -> event says why, verdicts unknown" "$(cat "$OB/E3.json" 2>&1)"
set_groups "$(group E4 404 new NoBriefY c22 critical)" "$(group E3 403 new ModelDownE c21 critical)"
run_poll OUTBOX_DIR="$OB"
jq -e '.degraded == "investigation produced no brief" and .brief == ""' "$OB/E4.json" >/dev/null && ok "no brief -> event still written, marked degraded" || bad "no brief -> event still written, marked degraded" "$(cat "$OB/E4.json" 2>&1)"

# 13. Pin the exact line handed to the investigator, the outbox event and the
#     briefs log header for one firing group, so a provider refactor can prove
#     it changed none of them. Fresh state so the group counts as new.
SNAP="$TMP/state-snap"; OB13="$TMP/outbox-snap"
set_groups "$(group S0 500 resolved SnapOld c30 warning)"
run_poll STATE_DIR="$SNAP" OUTBOX_DIR="$OB13"
set_groups "$(group S1 501 firing SnapX c30 critical gpu-9)" "$(group S0 500 resolved SnapOld c30 warning)"
: > "$TMP/investigated"
run_poll STATE_DIR="$SNAP" OUTBOX_DIR="$OB13"
want_line='{"status":"firing","alertname":"SnapX","severity":"critical","summary":"summary SnapX","description":"description SnapX","labels":{"alertname":"SnapX","cluster":"c30","node":"gpu-9","severity":"critical","condition":"GpuXidCritical"},"starts_at":"2026-10-25T09:04:00Z","pager":"irm","label":"IRM","ref":"501","url":"https://slack.example/archives/C1/p501"}'
got_line=$(tail -n 1 "$TMP/investigated" | jq -S . 2>&1)
[ "$got_line" = "$(jq -S . <<< "$want_line")" ] && ok "snapshot -> investigated line" || bad "snapshot -> investigated line" "got: $got_line"
want_ev='{"pager":"irm","id":"S1","ref":"501","url":"https://slack.example/archives/C1/p501","irm_id":"S1","irm_number":501,"irm_url":"https://slack.example/archives/C1/p501","alertname":"SnapX","severity":"critical","cluster":"c30","node":"gpu-9","starts_at":"2026-10-25T09:04:00Z","fault":"unknown","verdict":"unknown","failure_mode":"unknown","degraded":null,"dc_draft":null}'
got_ev=$(jq -S 'del(.ts, .brief)' "$OB13/S1.json" 2>&1)
[ "$got_ev" = "$(jq -S . <<< "$want_ev")" ] && ok "snapshot -> outbox event" || bad "snapshot -> outbox event" "got: $got_ev"
jq -e '.brief == "BRIEF SnapX on c30" and (.ts | test("^[0-9T:-]+Z$"))' "$OB13/S1.json" >/dev/null && ok "snapshot -> event brief and ts" || bad "snapshot -> event brief and ts" "$(cat "$OB13/S1.json" 2>&1)"
hdr=$(grep '^=== ' "$SNAP/briefs.log" 2>/dev/null | tail -n 1)
[[ "$hdr" =~ ^===\ [0-9T:-]+Z\ IRM\ \#501\ S1$ ]] && ok "snapshot -> briefs log header" || bad "snapshot -> briefs log header" "got: $hdr"

# 14. The old entry point still works: irm-poll.sh is a shim for poll.sh, so a
#     unit file and a clone that get out of step keep polling.
SA="$TMP/state-poll"; SB="$TMP/state-shim"
set_groups "$(group G1 101 acknowledged NvlinkDown c1 warning)"
run_poll STATE_DIR="$SA"; run_poll STATE_DIR="$SB" POLL_SCRIPT=irm-poll.sh
set_groups "$(group G3 103 new GpuFellOff c2 critical)" "$(group G1 101 acknowledged NvlinkDown c1 warning)"
run_poll STATE_DIR="$SA"; run_poll STATE_DIR="$SB" POLL_SCRIPT=irm-poll.sh
strip() { sed -E 's/^=== [0-9T:-]+Z /=== /' "$1"; }
[ -s "$SB/briefs.log" ] && [ "$(strip "$SA/briefs.log")" = "$(strip "$SB/briefs.log")" ] && ok "shim -> same briefs log as poll.sh" || bad "shim -> same briefs log as poll.sh" "poll.sh: $(cat "$SA/briefs.log" 2>&1) shim: $(cat "$SB/briefs.log" 2>&1)"

# 15. State written by the pre-split bootstrap (one IRM id per line) is still
#     honoured: nothing is re-bootstrapped or re-investigated.
S15="$TMP/state-old"; mkdir -p "$S15"
printf '%s\n' G3 G2 G1 > "$S15/seen"
set_groups "$(group G3 103 new GpuFellOff c2 critical)" "$(group G2 102 new XidCritical c1 critical)" "$(group G1 101 acknowledged NvlinkDown c1 warning)"
: > "$TMP/investigated"
run_poll STATE_DIR="$S15"
lacks "old state -> no bootstrap" "bootstrapped" "$OUT$ERR"
[ ! -s "$TMP/investigated" ] && ok "old state -> nothing investigated" || bad "old state -> nothing investigated" "$(cat "$TMP/investigated")"

# 16. The core runs any adapter that honours the contract.
cat > "$TMP/fake.sh" <<'EOF2'
#!/usr/bin/env bash
cat "$FAKE_OUT"
EOF2
export FAKE_OUT="$TMP/fake.jsonl"
fake_line() { # fake_line <id> <ref>
  jq -nc --arg id "$1" --arg ref "$2" '{id:$id, ref:$ref, url:"https://pager.example/" + $ref, state:"firing", severity:"critical",
    alert:{alertname:"FakeAlert", severity:"critical", summary:"s", description:"d", labels:{cluster:"cf", node:"nf"}, starts_at:"2026-10-25T09:04:00Z"}}'
}
S16="$TMP/state-fake"; OB16="$TMP/outbox-fake"
fake_line F0 70 > "$FAKE_OUT"
run_poll STATE_DIR="$S16" OUTBOX_DIR="$OB16" ONCALL_PAGER=fake ONCALL_PAGER_ADAPTER="$TMP/fake.sh"
{ fake_line F0 70; fake_line F1 77; } > "$FAKE_OUT"
: > "$TMP/investigated"
run_poll STATE_DIR="$S16" OUTBOX_DIR="$OB16" ONCALL_PAGER=fake ONCALL_PAGER_ADAPTER="$TMP/fake.sh"
check "fake adapter -> log names the provider and ref" "fake #77" "$(cat "$S16/briefs.log" 2>&1)"
jq -e '.pager == "fake" and .id == "F1" and .ref == "77" and .url == "https://pager.example/77" and (has("irm_id") | not) and (has("irm_number") | not)' "$OB16/F1.json" >/dev/null && ok "fake adapter -> event has pager keys, no irm_ keys" || bad "fake adapter -> event has pager keys, no irm_ keys" "$(cat "$OB16/F1.json" 2>&1)"
jq -e '.label == "fake" and .pager == "fake" and .ref == "77"' <<< "$(tail -n 1 "$TMP/investigated")" >/dev/null && ok "fake adapter -> investigated line carries label" || bad "fake adapter -> investigated line carries label" "$(cat "$TMP/investigated")"

# 17. An unknown pager is a configuration error, not an empty poll.
run_poll STATE_DIR="$TMP/state-nope" ONCALL_PAGER=nope
[ "$RC" -eq 1 ] && ok "unknown pager -> exit 1" || bad "unknown pager -> exit 1" "rc=$RC"
check "unknown pager -> says so on stderr" "no adapter for ONCALL_PAGER=nope" "$ERR"

# 18. A shell's own PAGER (less, bat...) must not select the adapter.
S18="$TMP/state-shellpager"
set_groups "$(group P1 601 acknowledged NvlinkDown c40 warning)"
run_poll STATE_DIR="$S18" PAGER=less
set_groups "$(group P2 602 new GpuFellOff c40 critical)" "$(group P1 601 acknowledged NvlinkDown c40 warning)"
run_poll STATE_DIR="$S18" PAGER=less
check "shell PAGER=less -> irm adapter still used" "IRM #602" "$(cat "$S18/briefs.log" 2>&1)"

echo
echo "$PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
