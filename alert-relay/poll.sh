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
#      OUTBOX_DIR (optional; one JSON event per brief for the Slack agent).
# Usage: poll.sh   (run every 30-60s from a timer)
set -uo pipefail

ONCALL_PAGER="${ONCALL_PAGER:-irm}"
ADAPTER="${ONCALL_PAGER_ADAPTER:-$(dirname "$0")/pagers/$ONCALL_PAGER.sh}"
[ -r "$ADAPTER" ] || { echo "error: no adapter for ONCALL_PAGER=$ONCALL_PAGER" >&2; exit 1; }
pager_label() { case "$1" in irm) echo IRM ;; *) echo "$1" ;; esac; }
LABEL=$(pager_label "$ONCALL_PAGER")
STATE_DIR="${STATE_DIR:-$HOME/.local/state/oncall-copilot}"
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
  [ -n "${HEARTBEAT_URL:-}" ] || return 0
  if [ -f "$DEGRADED" ]; then
    echo "warn: not pinging the heartbeat; latest brief degraded: $(cat "$DEGRADED")" >&2
    return 0
  fi
  curl -fsS -m 10 -o /dev/null "$HEARTBEAT_URL" 2>/dev/null \
    || echo "warn: heartbeat ping failed; $LABEL will alert if pings stay missing" >&2
}

# Runs inside the lock, as the fetch always did. A failed adapter must not read
# as "no new alerts": nothing below runs, so no state changes and no heartbeat.
body=$(mktemp)
trap 'rm -f "$body"' EXIT
bash "$ADAPTER" > "$body" || exit 1
jq empty "$body" 2>/dev/null || { echo "error: adapter $ONCALL_PAGER printed output that is not JSONL" >&2; exit 1; }

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

  # The relay's line shape, built from the group's firing alert.
  line=$(printf '%s' "$g" | jq -c --arg pager "$ONCALL_PAGER" --arg label "$LABEL" \
    '{status: "firing"} + .alert + {pager: $pager, label: $label, ref, url}')
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
