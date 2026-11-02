#!/usr/bin/env bash
# Tests for alert-relay/score-verdicts.sh against a stub IRM alert-groups API
# that serves $TMP/groups/<id>.json (404 if missing) and records every request.
#
# Usage: ./tests/score_verdicts_test.sh
set -uo pipefail

cd "$(dirname "$0")/.." || exit
TMP=$(mktemp -d)
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
BEARER_PORT=$(free_port)
BARE_PORT=$(free_port)
GLSA=glsa_test-token
PLAIN=plain-test-token

PASS=0; FAIL=0

cleanup() {
  [ -n "${STUB_PIDS:-}" ] && kill $STUB_PIDS 2>/dev/null
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

# Serves $TMP/groups/<id>.json; 401 unless the auth headers match the mode
# (bearer: Bearer + X-Grafana-URL; bare: the token alone). Logs every request,
# and answers any method but GET with 405 after logging it.
cat > "$TMP/stub.py" <<'EOF'
import os, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
port, mode, token, groups, reqs = int(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5]
class H(BaseHTTPRequestHandler):
    def note(self):
        with open(reqs, "a") as f:
            f.write("%s %s auth=%s grafana=%s\n" % (self.command, self.path,
                    self.headers.get("Authorization"), self.headers.get("X-Grafana-URL")))
    def do_GET(self):
        self.note()
        got = self.headers.get("Authorization")
        if mode == "bearer":
            good = got == "Bearer " + token and self.headers.get("X-Grafana-URL") == "https://grafana.test"
        else:
            good = got == token and not self.headers.get("X-Grafana-URL")
        if not good:
            self.send_response(401); self.end_headers(); return
        name = self.path.rsplit("/", 1)[-1]
        path = os.path.join(groups, name + ".json")
        if not self.path.startswith("/api/v1/alert_groups/") or not os.path.exists(path):
            self.send_response(404); self.end_headers(); return
        self.send_response(200); self.end_headers()
        self.wfile.write(open(path, "rb").read())
    def other(self):
        self.note(); self.send_response(405); self.end_headers()
    do_POST = do_PUT = do_PATCH = do_DELETE = do_HEAD = other
    def log_message(self, *a): pass
HTTPServer(("127.0.0.1", port), H).serve_forever()
EOF
mkdir -p "$TMP/groups"
: > "$TMP/req-bearer"; : > "$TMP/req-bare"
python3 "$TMP/stub.py" "$BEARER_PORT" bearer "$GLSA" "$TMP/groups" "$TMP/req-bearer" &
STUB_PIDS=$!
python3 "$TMP/stub.py" "$BARE_PORT" bare "$PLAIN" "$TMP/groups" "$TMP/req-bare" &
STUB_PIDS="$STUB_PIDS $!"
for p in "$BEARER_PORT" "$BARE_PORT"; do
  for _ in $(seq 50); do
    curl -s -o /dev/null "http://127.0.0.1:$p/" && break
    sleep 0.1
  done
done

# Fixtures. add <ref> <verdict-lines> <created_at> <state> <ack_by> <ack_at> <res_by> <res_at>
# writes a brief to the log and a group (id G<ref>) to the stub; ack/res values are raw JSON.
# Times are UTC; PT is UTC-7 until 2026-11-01 09:00Z, UTC-8 after.
LOG="$TMP/briefs.log"
add() {
  printf '=== 2026-11-02T20:00:00Z IRM #%s G%s\nSummary: the copilot looked at this.\n%b\n' "$1" "$1" "$2" >> "$LOG"
  [ "$3" = none ] && return
  jq -n --arg id "G$1" --arg c "$3" --arg s "$4" --argjson ab "${5:-null}" --argjson aa "${6:-null}" \
        --argjson rb "${7:-null}" --argjson ra "${8:-null}" \
    '{id: $id, alert_group_number: 1, state: $s, created_at: $c, acknowledged_by: $ab, acknowledged_at: $aa,
      resolved_by: $rb, resolved_at: $ra, title: ("title " + $id),
      permalinks: {slack: ("https://slack.test/" + $id), web: "https://web.test/x"}}' > "$TMP/groups/G$1.json"
}
H='Verdict: hold - looks like noise\n'
P='Verdict: page - needs eyes\n'
: > "$LOG"
add H1 "$H" 2026-11-02T10:00:00.123456Z acknowledged '"U1"' '"2026-11-02T10:12:30.5Z"'
add H2 "$H" 2026-11-02T20:00:00.5Z resolved null null null '"2026-11-02T20:05:00Z"'
add P1 "$P" 2026-11-02T18:00:00Z resolved null null '"U2"' '"2026-11-02T18:30:00Z"'
printf '\n--- 2026-11-02T20:01:00Z IRM #X1 GX1 (storm)\n\n' >> "$LOG"
add P2 "$P" 2026-11-02T06:30:00Z resolved null null null '"2026-11-02T06:40:00Z"'
head -c 1166 /dev/zero >> "$LOG"
add P3 "Prose mentions Verdict: hold mid-line.\n" 2026-11-02T19:00:00Z acknowledged '"U3"' '"2026-11-02T19:03:00Z"'
add P4 "${H}Verdict: Page - changed my mind\n" 2026-11-02T19:00:00Z acknowledged '"U3"' '"2026-11-02T19:09:00Z"'
head -c 40 /dev/zero >> "$LOG"       # NULs directly before a header, no newline between
add O1 "$P" 2026-11-02T19:30:00Z firing
add U1 "$P" none
add N1 "Verdict: page - no group file, but the id is fine\n" none
add J1 "$H" 2026-11-02T19:30:00Z resolved
echo 'not json at all' > "$TMP/groups/GJ1.json"
add W1 "$P" none
echo '{"id":"GW1","created_at":"2026-10-30T12:00:00Z"}' > "$TMP/groups/GW1.json"
add W2 "$P" none
echo '{"id":"GW2","created_at":"2026-10-30T12:00:00Z","state":7}' > "$TMP/groups/GW2.json"
# Night boundaries (PST, after the 2026-11-01 DST change): 21:59 no, 22:00 yes, 07:59 yes, 08:00 no.
add B1 "$H" 2026-11-03T05:59:00Z resolved null null null '"2026-11-03T06:30:00Z"'
add B2 "$H" 2026-11-03T06:00:00Z resolved null null null '"2026-11-03T06:30:00Z"'
add B3 "$H" 2026-11-02T15:59:00Z resolved null null null '"2026-11-02T16:30:00Z"'
add B4 "$H" 2026-11-02T16:00:00Z resolved null null null '"2026-11-02T16:30:00Z"'
# The same boundaries in PDT (before the change): 21:59 no, 22:00 yes.
add B5 "$H" 2026-11-01T04:59:00Z resolved null null null '"2026-11-01T06:30:00Z"'
add B6 "$H" 2026-11-01T05:00:00Z resolved null null null '"2026-11-01T06:30:00Z"'
# 01:30 on the DST change day, both occurrences: PDT then PST.
add D1 "$H" 2026-11-01T08:30:00Z resolved null null null '"2026-11-01T09:00:00Z"'
add D2 "$H" 2026-11-01T09:30:00Z resolved null null null '"2026-11-01T10:00:00Z"'

# The header for N1 exists but its group file doesn't: 404, same as U1.
HEADERS=$(tr '\000' '\n' < "$LOG" | grep -c '^=== ')

run_bearer() { IRM_API_URL="http://127.0.0.1:$BEARER_PORT/" IRM_TOKEN="$GLSA" GRAFANA_URL=https://grafana.test "$@"; }
run_bare()   { IRM_API_URL="http://127.0.0.1:$BARE_PORT" IRM_TOKEN="$PLAIN" env -u GRAFANA_URL "$@"; }

OUT=$(run_bearer ./alert-relay/score-verdicts.sh "$LOG" 2>"$TMP/err"); RC=$?
[ "$RC" = 0 ] && ok "exits 0" || bad "exits 0" "rc=$RC; $(head -3 "$TMP/err")"

col() { # col <ref> <n>: column n of the row for brief #<ref>
  printf '%s\n' "$OUT" | awk -F'\t' -v r="$1" -v n="$2" '$2 == r { print $n }'
}
cat_of() { check "$1 is $2" "$2" "$(col "$1" 9)"; }

cat_of H1 'MISS?'
cat_of H2 'hold-ok'
cat_of P1 'page-ok'
cat_of P2 'false-page?'
cat_of P3 'page-ok'
cat_of P4 'page-ok'
cat_of O1 'open'
cat_of U1 'unknown'
cat_of N1 'unknown'
cat_of J1 'unknown'
cat_of W1 'unknown'
cat_of W2 'unknown'
check "P3 (no Verdict line) reads as page" "page" "$(col P3 4)"
check "P4 (hold then page) last verdict wins" "page" "$(col P4 4)"
check "H1 is a night brief (02:00 PST)" "yes" "$(col H1 5)"
check "H1 minutes to first human action" "12" "$(col H1 7)"
check "P1 minutes (resolved by a human)" "30" "$(col P1 7)"
check "H1 row carries the slack link" "https://slack.test/GH1" "$(col H1 11)"
check "H1 row carries the title" "title GH1" "$(col H1 10)"
empty() { [ -z "$(col "$2" "$3")" ] && ok "$1" || bad "$1" "expected empty, got: $(col "$2" "$3")"; }
empty "P2 (auto-resolved) has no minutes" P2 7
check "P2 night (22:30 PDT)" "yes" "$(col P2 5)"
check "H2 is not night (13:00 PST)" "no" "$(col H2 5)"
empty "unknown rows leave night empty" U1 5

for b in "B1 no" "B2 yes" "B3 yes" "B4 no" "B5 no" "B6 yes" "D1 yes" "D2 yes"; do
  set -- $b
  check "night boundary $1 -> $2" "$2" "$(col "$1" 5)"
  check "night boundary $1 is hold-ok" "hold-ok" "$(col "$1" 9)"
done

check "MISS? list names H1" "#H1  12 min  title GH1  https://slack.test/GH1" "$OUT"
check "MISS? list has one entry" "MISS? (1)" "$OUT"
check "false-page rate counts P2 over page-ok + false-page?" "false-page rate 1/4 (25%)" "$OUT"
check "night-only counts" "night: MISS?=1  hold-ok=5" "$OUT"
PARSED=$(printf '%s\n' "$OUT" | sed -n 's/^entries parsed: \([0-9]*\), headers seen: \([0-9]*\).*/\1 \2/p')
check "summary header count equals the log's" "$HEADERS $HEADERS" "$PARSED"
lacks "no drop warning on a clean log" "WARNING" "$OUT"

# A header that cannot be parsed is not silently dropped from the count.
printf '=== garbled header\nVerdict: hold - x\n\n' > "$TMP/garbled.log"
add_out=$(run_bearer ./alert-relay/score-verdicts.sh "$TMP/garbled.log" 2>&1)
check "garbled header is flagged" "entries parsed: 0, headers seen: 1  WARNING" "$add_out"

# Requests: GET only, one per brief, and the right auth for each token kind.
check "bearer token sent as Bearer" "auth=Bearer $GLSA grafana=https://grafana.test" "$(cat "$TMP/req-bearer")"
lacks "no non-GET request" "POST " "$(cat "$TMP/req-bearer")"
NONGET=$(grep -vc '^GET ' "$TMP/req-bearer")
[ "$NONGET" = 0 ] && ok "every request is a GET" || bad "every request is a GET" "$NONGET non-GET requests"
REQS=$(grep -c "^GET /api/v1/alert_groups/" "$TMP/req-bearer")
[ "$REQS" = "$HEADERS" ] && ok "one request per brief" || bad "one request per brief" "$REQS requests for $HEADERS briefs"

BARE_OUT=$(run_bare ./alert-relay/score-verdicts.sh "$LOG" 2>&1)
check "bare token sent without Bearer or X-Grafana-URL" "auth=$PLAIN grafana=None" "$(cat "$TMP/req-bare")"
lacks "bare token run is not rejected" "unknown	" "$(printf '%s\n' "$BARE_OUT" | awk -F'\t' '$2 == "H1"')"
check "bare run classifies H1" "MISS?" "$BARE_OUT"

# A rejected token must show as unknown, never as hold-ok or false-page?.
BAD_OUT=$(IRM_API_URL="http://127.0.0.1:$BARE_PORT" IRM_TOKEN=wrong ./alert-relay/score-verdicts.sh "$LOG" 2>&1); BAD_RC=$?
[ "$BAD_RC" = 0 ] && ok "401s do not change the exit code" || bad "401s do not change the exit code" "rc=$BAD_RC"
check "401 run: every brief unknown" "unknown=$HEADERS" "$BAD_OUT"
lacks "401 run: nothing counted as hold-ok" "hold-ok=1" "$BAD_OUT"

# Config errors.
ERR=$(env -u IRM_TOKEN IRM_API_URL=http://x ./alert-relay/score-verdicts.sh "$LOG" 2>&1); RC=$?
[ "$RC" = 1 ] && ok "missing IRM_TOKEN exits 1" || bad "missing IRM_TOKEN exits 1" "rc=$RC"
check "missing IRM_TOKEN says error:" "error:" "$ERR"
ERR=$(env -u IRM_API_URL IRM_TOKEN=t ./alert-relay/score-verdicts.sh "$LOG" 2>&1); RC=$?
[ "$RC" = 1 ] && ok "missing IRM_API_URL exits 1" || bad "missing IRM_API_URL exits 1" "rc=$RC"
ERR=$(env -u GRAFANA_URL IRM_API_URL=http://x IRM_TOKEN=glsa_x ./alert-relay/score-verdicts.sh "$LOG" 2>&1); RC=$?
[ "$RC" = 1 ] && ok "glsa_ token without GRAFANA_URL exits 1" || bad "glsa_ token without GRAFANA_URL exits 1" "rc=$RC"
ERR=$(IRM_API_URL=http://x IRM_TOKEN=t ./alert-relay/score-verdicts.sh "$TMP/nope.log" 2>&1); RC=$?
[ "$RC" = 1 ] && ok "missing log exits 1" || bad "missing log exits 1" "rc=$RC"
check "missing log says error:" "error:" "$ERR"

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
