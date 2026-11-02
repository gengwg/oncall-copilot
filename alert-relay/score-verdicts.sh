#!/usr/bin/env bash
# Scores the shadow briefs against what humans did. For each brief in the log it
# looks the alert group up in the IRM API (GET only) and says whether the
# verdict held up. Before the copilot may ack pages, no `hold` verdict should
# sit on an alert a human then had to act on (category MISS?).
#
# Definitions: a human acted when the group's acknowledged_by or resolved_by is
# non-null (a source auto-resolve leaves resolved_by null). Night is created_at
# between 22:00 and 08:00 America/Los_Angeles. A brief without a readable
# verdict counts as page, like investigate.sh. Categories: MISS? (hold, human
# acted), hold-ok (hold, resolved, nobody acted), page-ok (page, human acted),
# false-page? (page, resolved, nobody acted), open (nobody acted, not resolved),
# unknown (lookup failed; never counted as ok).
#
# Valid only for periods when ACK_DRY_RUN=1 held: live copilot acks use the
# owner's personal token and show as the owner, so any live-ack period makes
# page-ok too high and the false-page rate too low.
#
# Output goes to the operator's terminal only: it carries alert titles and links.
# stdout: one tab-separated row per brief, then a blank line and the summary.
#   ts ref id verdict night acted min_to_action state category title url
#
# Env: IRM_API_URL, IRM_TOKEN (required), GRAFANA_URL (required for a glsa_ token).
#      Auth as slack-agent/internal/irm/client.go: glsa_ is sent as Bearer, anything
#      else bare; X-Grafana-URL goes with either whenever GRAFANA_URL is set.
# Usage: score-verdicts.sh [briefs-log]   (default $STATE_DIR/briefs.log)
set -uo pipefail

STATE_DIR="${STATE_DIR:-$HOME/.local/state/oncall-copilot}"
LOG="${1:-$STATE_DIR/briefs.log}"
[ -r "$LOG" ] || { echo "error: cannot read briefs log $LOG" >&2; exit 1; }
[ -n "${IRM_API_URL:-}" ] || { echo "error: IRM_API_URL is not set" >&2; exit 1; }
[ -n "${IRM_TOKEN:-}" ] || { echo "error: IRM_TOKEN is not set" >&2; exit 1; }
if [ "${IRM_TOKEN#glsa_}" != "$IRM_TOKEN" ]; then
  [ -n "${GRAFANA_URL:-}" ] || { echo "error: GRAFANA_URL is not set (needed for a glsa_ token)" >&2; exit 1; }
  AUTH=(-H "Authorization: Bearer $IRM_TOKEN")
else
  AUTH=(-H "Authorization: $IRM_TOKEN")
fi
[ -z "${GRAFANA_URL:-}" ] || AUTH+=(-H "X-Grafana-URL: $GRAFANA_URL")

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

# Every NUL run is an entry boundary (a marker line), and a NUL run also ends the
# line before it, so a header after one still starts its own line. An entry is a
# well-formed header; the last Verdict: line wins. Verdict: lines that arrive
# with no entry open are counted: a NUL block may have eaten their header.
# The counts go to a file so a dropped entry or a gap shows in the summary.
LC_ALL=C tr -s '\000' '\001' < "$LOG" | LC_ALL=C sed 's/\x01/\n\x01\n/g' | awk -v counts="$T/counts" '
  function flush() { if (open) print ts "\t" ref "\t" id "\t" v; open = 0 }
  $0 == "\001" { flush(); blocks++; next }
  /^=== / { flush(); n++
            if (NF >= 5 && $4 ~ /^#/) { ts = $2; ref = substr($4, 2); id = $5; v = "page"; open = 1 }
            next }
  /^Verdict: / { if (!open) { outside++; next } w = tolower($2); gsub(/[^a-z]/, "", w); v = (w == "hold") ? "hold" : "page" }
  END { flush(); print n + 0, blocks + 0, outside + 0 > counts }
' > "$T/entries"

# created_at has fractional seconds, which fromdateiso8601 rejects.
CLASSIFY='
  def epoch: sub("\\.[0-9]+"; "") | fromdateiso8601;
  def strornull: . == null or type == "string";
  . as $g
  | (if ($g | type) == "object" and (.state | type) == "string" and (.created_at | type) == "string"
        and (["acknowledged_by", "acknowledged_at", "resolved_by", "resolved_at"]
             | all(. as $k | ($g | has($k)) and ($g[$k] | strornull)))
     then . else error("malformed group") end)
  | (.created_at | epoch) as $c
  | ((.acknowledged_by != null) or (.resolved_by != null)) as $acted
  | ([(if .acknowledged_by != null then .acknowledged_at else empty end),
      (if .resolved_by != null then .resolved_at else empty end)]
     | map(select(. != null) | epoch) | min) as $first
  | (if $acted then (if $verdict == "hold" then "MISS?" else "page-ok" end)
     elif .state == "resolved" then (if $verdict == "hold" then "hold-ok" else "false-page?" end)
     else "open" end) as $cat
  | ($c | localtime | .[3]) as $h
  | [$ts, $ref, $id, $verdict,
     (if ($h >= 22 or $h < 8) then "yes" else "no" end),
     (if $acted then "yes" else "no" end),
     (if $first != null then (($first - $c) / 60 | floor) else "" end),
     .state, $cat, (.title // ""), (.permalinks.slack // .permalinks.web // "")]
  | @tsv'

while IFS=$'\t' read -r ts ref id verdict; do
  rm -f "$T/g.json"
  code=000
  case "$id" in
    *[!A-Za-z0-9_-]*) ;;
    *) code=$(curl -s -m 20 -o "$T/g.json" -w '%{http_code}' "${AUTH[@]}" \
         "${IRM_API_URL%/}/api/v1/alert_groups/$id" < /dev/null) || code=000 ;;
  esac
  row=""
  [ "$code" = 200 ] && row=$(TZ=America/Los_Angeles jq -er --arg ts "$ts" --arg ref "$ref" --arg id "$id" \
    --arg verdict "$verdict" "$CLASSIFY" "$T/g.json" 2>/dev/null)
  [ -n "$row" ] || row=$(printf '%s\t%s\t%s\t%s\t\t\t\t\tunknown\t\t' "$ts" "$ref" "$id" "$verdict")
  printf '%s\n' "$row"
done < "$T/entries" > "$T/rows"

cat "$T/rows"
echo
awk -F'\t' -v cnt="$(cat "$T/counts")" '
  function counts(n,   s, i) {
    for (i = 1; i <= nc; i++) s = s (i > 1 ? "  " : "") cats[i] "=" n[cats[i]] + 0
    return s
  }
  function rate(n) {
    d = n["page-ok"] + n["false-page?"]
    return d ? sprintf("%d/%d (%.0f%%)", n["false-page?"], d, 100 * n["false-page?"] / d) : "n/a (no page briefs with an outcome)"
  }
  BEGIN { nc = split("MISS? hold-ok page-ok false-page? open unknown", cats, " ") }
  BEGIN { split(cnt, c, " ") }
  { all[$9]++; total++; ids[$3]++
    if ($5 == "yes") { night[$9]++; nightn++ }
    if ($9 == "MISS?") miss[++m] = $1 "  #" $2 "  " ($7 == "" ? "?" : $7) " min  " $10 "  " $11 }
  END {
    for (k in ids) distinct++
    printf "entries parsed: %d (distinct ids: %d), headers seen: %d%s\n", total, distinct, c[1], (total == c[1] ? "" : "  WARNING: entries were dropped")
    printf "NUL blocks: %d, verdict lines outside an entry: %d%s\n", c[2], c[3], (c[3] > 0 ? "  WARNING: the evidence has a gap" : "")
    print "all:   " counts(all)
    print "       false-page rate " rate(all)
    printf "night: %s  (%d briefs; unknown rows have no night flag)\n", counts(night), nightn
    print "       false-page rate " rate(night)
    printf "MISS? (%d), ts / ref / minutes to first human action / title / link:\n", m
    for (i = 1; i <= m; i++) print "  " miss[i]
  }' "$T/rows"
