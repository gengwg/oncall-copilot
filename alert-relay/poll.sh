#!/usr/bin/env bash
# Shadow-mode poller for an on-call paging provider. Each run asks a pager
# adapter for the newest alert groups, investigates the ones it has not seen,
# and appends each brief to a local log. It writes nothing to the pager or Slack.
#
# Polling instead of an outgoing webhook: it needs no public endpoint and
# sees every group, including ones the pager never posted to Slack.
#
# Adapter contract: pagers/<name>.sh, no arguments, provider settings from env.
# On success it exits 0 and prints JSONL, oldest first, one object per group:
#   {"id":"<provider id>","ref":"<human number, string>","url":"<link or empty>",
#    "state":"firing|acknowledged|resolved|silenced","severity":"<group severity>",
#    "paged_at":"<when the pager paged, RFC3339, or empty>",
#    "alert":{"alertname":..,"severity":..,"summary":..,"description":..,
#             "labels":{..},"starts_at":".."}}
# alert.labels already has customer removed. On a failed fetch it prints one
# "error: ..." line to stderr and exits non-zero; empty output means no groups.
#
# Env: ONCALL_PAGER (default irm) picks $(dirname "$0")/pagers/$ONCALL_PAGER.sh, ONCALL_PAGER_ADAPTER
#      overrides the path (tests). Provider settings are the adapter's: see pagers/.
#      STATE_DIR (default ~/.local/state/oncall-copilot), SHADOW_LOG,
#      INVESTIGATE (default investigate.sh next to this script),
#      STORM_WINDOW_S (default 1800), STORM_MAX (default 5),
#      SEVERITIES (default "critical warning"),
#      HEARTBEAT_URL (optional heartbeat endpoint; holds a secret),
#      DEGRADED_HOLD_AFTER (default 2 degraded briefs in a row),
#      OUTBOX_DIR (optional; one JSON event per brief for the Slack agent),
#      HISTORY_DAYS (default 90; 0 turns off the first-run backfill),
#      HISTORY_PAST (default 3 past pages handed to each investigation).
#
# Optional adapter modes, for past pages of the same alert on the same node:
# `page <n>` prints page n (1 newest) in the same JSONL, nothing past the last
# page; `outcome <id>` prints {id, ref, state, paged_at, acked_at,
# acked_by_person, resolved_at, resolved_by_person}. An adapter without them
# exits non-zero for them and briefs simply get no past pages.
# Usage: poll.sh   (run every 30-60s from a timer)
set -uo pipefail

# A gap is time the poller was not running, so it is measured from here, not
# from when a slow run ends.
run_start=$(date +%s)
ONCALL_PAGER="${ONCALL_PAGER:-irm}"
STATE_DIR="${STATE_DIR:-$HOME/.local/state/oncall-copilot}"

# Health events for the daily digest, read by another user's agent: one
# "<UTC time>\t<kind>\t<detail>" line each. Also used before the lock and
# before the state dir exists, so it makes the dir and takes no lock. Bounded
# here (last 3000 lines, about two days of a failing poller) so every writer
# keeps the file short. A failed write never changes an exit code or adds to
# stderr. Details must never carry the heartbeat URL (it holds a secret).
health_event() { # health_event <poll-failed|ping-failed|held|gap> <detail>
  local f="$STATE_DIR/health.log"
  mkdir -p "$STATE_DIR" 2>/dev/null || return 0
  { printf '%s\t%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" \
      "$(printf '%s' "$2" | tr '\t\r\n' '   ' | cut -c1-300)" >> "$f"; } 2>/dev/null || return 0
  { tail -n 3000 "$f" > "$f.tmp" && mv "$f.tmp" "$f"; } 2>/dev/null
  return 0
}

ADAPTER="${ONCALL_PAGER_ADAPTER:-$(dirname "$0")/pagers/$ONCALL_PAGER.sh}"
[ -r "$ADAPTER" ] || {
  echo "error: no adapter for ONCALL_PAGER=$ONCALL_PAGER" >&2
  health_event poll-failed "no adapter for ONCALL_PAGER=$ONCALL_PAGER"
  exit 1
}
pager_label() { case "$1" in irm) echo IRM ;; *) echo "$1" ;; esac; }
LABEL=$(pager_label "$ONCALL_PAGER")
SHADOW_LOG="${SHADOW_LOG:-$STATE_DIR/briefs.log}"
INVESTIGATE="${INVESTIGATE:-$(dirname "$0")/investigate.sh}"
STORM_WINDOW_S="${STORM_WINDOW_S:-1800}"
STORM_MAX="${STORM_MAX:-5}"
SEVERITIES="${SEVERITIES:-critical warning}"
DEGRADED_HOLD_AFTER="${DEGRADED_HOLD_AFTER:-2}"
OUTBOX_DIR="${OUTBOX_DIR:-}"
SEEN="$STATE_DIR/seen"
STORM="$STATE_DIR/storm"
DEGRADED="$STATE_DIR/degraded"
STREAK="$STATE_DIR/degraded-streak"
HISTORY="$STATE_DIR/history.tsv"
HISTORY_DAYS="${HISTORY_DAYS:-90}"
HISTORY_PAST="${HISTORY_PAST:-3}"

mkdir -p "$STATE_DIR"
exec 9>>"$STATE_DIR/.poll.lock"
flock -n 9 || exit 0

# Pinged only after a successful poll, so the pager alerts on anything that stops
# them: a dead timer, a down host, or a refused or expired token. The URL is
# never printed because it carries the heartbeat's secret.
#
# Also held once DEGRADED_HOLD_AFTER briefs in a row came out degraded (model
# or evidence failure): polls still succeed then, but the briefs are useless.
# One alone is usually an upstream blip and only warns. It stays held on quiet
# polls, which cannot show the cause is fixed, until a later brief is healthy.
heartbeat() {
  if [ -f "$DEGRADED" ]; then
    health_event held "$(cat "$DEGRADED")"
    [ -z "${HEARTBEAT_URL:-}" ] || echo "warn: not pinging the heartbeat; latest brief degraded: $(cat "$DEGRADED")" >&2
    return 0
  fi
  if [ -z "${HEARTBEAT_URL:-}" ]; then
    mark_ok
    return 0
  fi
  local rc=0
  curl -fsS -m 10 -o /dev/null "$HEARTBEAT_URL" 2>/dev/null || rc=$?
  if [ "$rc" -eq 0 ]; then
    mark_ok
  else
    echo "warn: heartbeat ping failed; $LABEL will alert if pings stay missing" >&2
    health_event ping-failed "curl exit $rc"
  fi
}

# last-ok is the last run that succeeded and was not held (with a heartbeat
# URL, the last successful ping). A stopped poller writes nothing, so the first
# run back reports the outage as a gap. No previous last-ok (first run after
# deploy) means no gap to report. A long run (a burst of briefs) is not a gap:
# only the time before it started counts.
mark_ok() {
  local f="$STATE_DIR/last-ok" prev now_s
  now_s=$(date +%s)
  if [ -f "$f" ]; then
    if grep -qE '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$' "$f" 2>/dev/null; then
      prev=$(date -u -d "$(cat "$f")" +%s 2>/dev/null)
    fi
    [ -n "${prev:-}" ] || prev=$(stat -c %Y "$f" 2>/dev/null)
    if [ -n "$prev" ] && [ $((run_start - prev)) -gt 600 ]; then
      health_event gap "$(( (run_start - prev) / 60 )) min since $(date -u -d "@$prev" +%Y-%m-%dT%H:%M:%SZ)"
    fi
  fi
  { date -u -d "@$now_s" +%Y-%m-%dT%H:%M:%SZ > "$f"; } 2>/dev/null
  return 0
}

# Runs inside the lock, as the fetch always did. A failed adapter must not read
# as "no new alerts": nothing below runs, so no state changes and no heartbeat.
body=$(mktemp)
adapter_err=$(mktemp)
trap 'rm -f "$body" "$adapter_err"' EXIT
# The adapter's stderr is replayed unchanged, once it has exited, so the first
# line can also go into the health event.
bash "$ADAPTER" > "$body" 2> "$adapter_err" || {
  rc=$?
  cat "$adapter_err" >&2
  health_event poll-failed "adapter $ONCALL_PAGER: $(head -n 1 "$adapter_err" | grep . || echo "exit $rc")"
  exit 1
}
cat "$adapter_err" >&2
jq empty "$body" 2>/dev/null || {
  echo "error: adapter $ONCALL_PAGER printed output that is not JSONL" >&2
  health_event poll-failed "adapter $ONCALL_PAGER printed output that is not JSONL"
  exit 1
}

# The relay's line shape, built from a group's firing alert. Some rules name
# the node in a Hostname label, and node-exporter rules only in instance; read
# either as node here, once, so dedup, node evidence, the contained-Xid hold,
# past pages and the outbox all see it. An instance is a node only as a bare
# name: host:port, an IP or a URL is a scrape target or a probe.
LINE_JQ='{status: "firing"} + .alert + {pager: $pager, label: $label, ref, url, paged_at: (.paged_at // "")}
  | if (.labels | type) == "object" and (.labels.node // "") == "" then
      (.labels.instance // "") as $i
      | if (.labels.Hostname // "") != "" then .labels.node = .labels.Hostname
        elif ($i | test("^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$")) and ($i | test("^[0-9.]+$") | not)
        then .labels.node = $i else . end
    else . end'

# Past pages: history.tsv holds one "paged_at id ref alertname cluster node"
# row per group ever listed, so a brief can say how the same alert on the same
# node went before, including pages from before this host's own briefs.
history_rows() { # history_rows <jsonl-file>: TSV rows for its groups
  jq -r --arg pager "$ONCALL_PAGER" --arg label "$LABEL" '. as $g | select((.alert.alertname // "") != "")
    | ('"$LINE_JQ"') | [.paged_at, $g.id, .ref, .alertname, (.labels.cluster // ""), (.labels.node // "")]
    | map(tostring | gsub("[\t\r\n]"; " ")) | @tsv' "$1" 2>/dev/null
}
record_history() { # record_history <jsonl-file>
  [ -f "$HISTORY" ] || return 0
  history_rows "$1" >> "$HISTORY"
  sort -t$'\t' -k1,1 "$HISTORY" | awk -F'\t' '!seen[$2]++' | tail -n 20000 > "$HISTORY.tmp" && mv "$HISTORY.tmp" "$HISTORY"
}
# Once, when there is no history yet: page back to HISTORY_DAYS. Stops at an
# empty page, a page older than the cutoff, a page with nothing new, or 200
# pages. A failed page keeps what was read; the rest fills in as groups arrive.
backfill_history() {
  [ -f "$HISTORY" ] && return 0
  local cutoff p page rows oldest n
  if [ "$HISTORY_DAYS" -le 0 ] 2>/dev/null; then : > "$HISTORY"; return 0; fi
  cutoff=$(date -u -d "-$HISTORY_DAYS days" +%Y-%m-%dT%H:%M:%SZ)
  history_rows "$body" > "$HISTORY.new"
  page=$(mktemp)
  for p in $(seq 2 200); do
    bash "$ADAPTER" page "$p" > "$page" 2>/dev/null || break
    rows=$(history_rows "$page")
    [ -n "$rows" ] || break
    n=$(printf '%s\n' "$rows" | awk -F'\t' 'NR == FNR { s[$2] = 1; next } !($2 in s)' "$HISTORY.new" - | wc -l)
    [ "$n" -gt 0 ] || break
    printf '%s\n' "$rows" >> "$HISTORY.new"
    oldest=$(printf '%s\n' "$rows" | cut -f1 | sort | head -1)
    [[ "$oldest" < "$cutoff" ]] && break
  done
  rm -f "$page"
  awk -F'\t' -v c="$cutoff" '$1 >= c' "$HISTORY.new" | sort -t$'\t' -k1,1 | awk -F'\t' '!seen[$2]++' > "$HISTORY"
  rm -f "$HISTORY.new"
  echo "history: $(wc -l < "$HISTORY") past alert groups back to $cutoff"
}
past_matches() { # past_matches <alertname> <cluster> <node> <id> <paged_at>: "paged_at id ref", oldest first
  awk -F'\t' -v a="$1" -v c="$2" -v n="$3" -v id="$4" -v at="$5" \
    '$4 == a && $5 == c && $6 == n && $2 != id && (at == "" || $1 < at) { print $1 "\t" $2 "\t" $3 }' "$HISTORY" 2>/dev/null | sort
}
past_pages() { # past_pages <alertname> <cluster> <node> <id> <paged_at>: JSON array, newest first
  local ids out o
  ids=$(past_matches "$@" | tail -n "$HISTORY_PAST" | tac)
  [ -n "$ids" ] || { echo '[]'; return 0; }
  out=$(while IFS=$'\t' read -r at hid ref; do
    o=$(bash "$ADAPTER" outcome "$hid" 2>/dev/null | jq -c --arg id "$hid" 'select(.id == $id)' 2>/dev/null)
    [ -n "$o" ] || o=$(jq -nc --arg id "$hid" --arg ref "$ref" --arg at "$at" '{id: $id, ref: $ref, paged_at: ($at | sub("\\.[0-9]+"; "")), state: "unknown"}')
    printf '%s\n' "$o"
  done <<< "$ids")
  printf '%s\n' "$out" | jq -sc .
}
backfill_history
record_history "$body"

# The first run records what already exists rather than investigating a backlog.
if [ ! -f "$SEEN" ]; then
  jq -r '.id' "$body" > "$SEEN"
  echo "bootstrapped $(wc -l < "$SEEN") existing alert groups; investigating new ones from now on"
  heartbeat
  exit 0
fi

log() { printf '=== %s %s #%s %s\n%s\n\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$LABEL" "$1" "$2" "$3" >> "$SHADOW_LOG"; }
skip() { printf -- '--- %s %s #%s %s (%s)\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$LABEL" "$1" "$3" "$2" >> "$SHADOW_LOG"; }
# One event per brief for the Slack agent, which runs as another user and only
# reads here. The verdict fields are lifted from the brief's fixed lines; a
# brief without them reads as unknown. Written whole, then renamed, so a
# half-written file is never picked up. Skipped groups get no event.
emit_event() { # emit_event <id> <alert-line-json> <brief> <degraded-reason>
  [ -n "$OUTBOX_DIR" ] || return 0
  mkdir -p "$OUTBOX_DIR"
  local f="$OUTBOX_DIR/$1.json"
  printf '%s' "$2" | jq --arg id "$1" --arg brief "$3" --arg degraded "$4" \
      --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '
    def field($p): ($brief | split("\n") | map(select(startswith($p))) | last // "" | ltrimstr($p));
    def word($s): if $s == "" then "unknown" else ($s | split(" ")[0] | ascii_downcase) end;
    {pager, id: $id, ref, url, alertname, severity,
     cluster: (.labels.cluster // ""), node: (.labels.node // ""), starts_at,
     fault: word(field("Fault: ")), verdict: word(field("Verdict: ")),
     failure_mode: word(field("Failure mode: ")),
     degraded: (if $degraded == "" then null else $degraded end),
     dc_draft: (field("DC draft: ") | if . == "" then null else . end),
     brief: $brief, ts: $ts}
    | if .pager == "irm" then . + {irm_id: .id, irm_number: (.ref | tonumber? // null), irm_url: .url} else . end' > "$f.tmp" && mv "$f.tmp" "$f"
}

now=$(date +%s)
# Oldest first, so storm suppression keeps the first group of a burst.
mapfile -t groups < <(jq -c . "$body")
for g in "${groups[@]}"; do
  id=$(printf '%s' "$g" | jq -r '.id')
  grep -qxF -- "$id" "$SEEN" && continue
  # Marked seen before investigating: a crash mid-investigation costs one
  # shadow brief, not a loop that re-bills the model every poll.
  echo "$id" >> "$SEEN"
  num=$(printf '%s' "$g" | jq -r '.ref')
  state=$(printf '%s' "$g" | jq -r '.state')
  sev=$(printf '%s' "$g" | jq -r '.severity')

  case " $SEVERITIES " in *" $sev "*) ;; *) continue ;; esac
  if [ "$state" = "resolved" ]; then
    skip "$num" "$id" "skipped: resolved before investigation"
    continue
  fi

  line=$(printf '%s' "$g" | jq -c --arg pager "$ONCALL_PAGER" --arg label "$LABEL" "$LINE_JQ")
  # Two different checks. The same alert on the same node is one problem. The
  # same alert on other nodes is separate hardware and gets its own brief, up
  # to STORM_MAX per alert and cluster per window: a label rename that fires on
  # every node is one cause, not a hundred.
  key=$(printf '%s' "$line" | jq -r '[.alertname, (.labels.cluster // ""), (.labels.node // "")] | join("\t")')
  read -r same_node in_window < <(awk -F'\t' -v k="$key" -v now="$now" -v w="$STORM_WINDOW_S" '
    BEGIN { split(k, p, "\t") }
    now - $4 < w && $1 == p[1] && $2 == p[2] { n++; if ($3 == p[3]) s = 1 }
    END { print s + 0, n + 0 }' "$STORM" 2>/dev/null || echo "0 0")
  if [ "${same_node:-0}" -eq 1 ]; then
    skip "$num" "$id" "skipped: repeat on the same node within $((STORM_WINDOW_S / 60))m"
    continue
  fi
  if [ "${in_window:-0}" -ge "$STORM_MAX" ]; then
    skip "$num" "$id" "skipped: storm, $in_window briefs for this alert on this cluster in the last $((STORM_WINDOW_S / 60))m"
    continue
  fi
  printf '%s\t%s\n' "$key" "$now" >> "$STORM"

  # The same alert on the same node before: the newest HISTORY_PAST with their
  # outcomes, and how many there were.
  k_alert=$(printf '%s' "$line" | jq -r '.alertname // ""')
  k_cluster=$(printf '%s' "$line" | jq -r '.labels.cluster // ""')
  k_node=$(printf '%s' "$line" | jq -r '.labels.node // ""')
  k_at=$(printf '%s' "$line" | jq -r '.paged_at // ""')
  past=$(past_pages "$k_alert" "$k_cluster" "$k_node" "$id" "$k_at")
  total=$(past_matches "$k_alert" "$k_cluster" "$k_node" "$id" "$k_at" | wc -l)
  line=$(printf '%s' "$line" | jq -c --argjson past "${past:-[]}" --argjson total "${total:-0}" '. + {past_pages: $past, past_pages_total: $total}')

  brief=$(bash "$INVESTIGATE" "$line" 2>>"$STATE_DIR/investigate.err")
  log "$num" "$id" "${brief:-investigation produced no brief (see $STATE_DIR/investigate.err)}"
  reason=$(printf '%s\n' "$brief" | sed -n 's/^Degraded: //p' | head -1)
  [ -z "$brief" ] && reason="investigation produced no brief"
  emit_event "$id" "$line" "$brief" "$reason"
  if [ -n "$reason" ]; then
    n=$(cat "$STREAK" 2>/dev/null)
    case "$n" in ''|*[!0-9]*) n=0 ;; esac
    n=$((n + 1))
    echo "$n" > "$STREAK"
    if [ "$n" -ge "$DEGRADED_HOLD_AFTER" ]; then
      printf '%s %s #%s: %s (%s in a row)\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$LABEL" "$num" "$reason" "$n" > "$DEGRADED"
    else
      echo "warn: $n degraded brief in a row ($LABEL #$num: $reason); the heartbeat is held from $DEGRADED_HOLD_AFTER" >&2
    fi
  else
    rm -f "$DEGRADED" "$STREAK"
  fi
done

# Bound the state files.
tail -n 2000 "$SEEN" > "$SEEN.tmp" && mv "$SEEN.tmp" "$SEEN"
[ -f "$STORM" ] && tail -n 500 "$STORM" > "$STORM.tmp" && mv "$STORM.tmp" "$STORM"
[ -d "$OUTBOX_DIR" ] && find "$OUTBOX_DIR" -maxdepth 1 -name '*.json' -mtime +7 -delete
heartbeat
exit 0
