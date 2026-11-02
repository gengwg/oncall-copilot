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
# Output goes to the operator's terminal only: it carries alert titles and links.
# stdout: one tab-separated row per brief, then a blank line and the summary.
#   ts ref id verdict night acted min_to_action state category title url
#
# Env: IRM_API_URL, IRM_TOKEN (required), GRAFANA_URL (required for a glsa_ token).
#      Auth as slack-agent/internal/irm/client.go: glsa_ is Bearer + X-Grafana-URL,
#      anything else is sent bare.
# Usage: score-verdicts.sh [briefs-log]   (default $STATE_DIR/briefs.log)
set -uo pipefail

STATE_DIR="${STATE_DIR:-$HOME/.local/state/oncall-copilot}"
LOG="${1:-$STATE_DIR/briefs.log}"
[ -r "$LOG" ] || { echo "error: cannot read briefs log $LOG" >&2; exit 1; }
[ -n "${IRM_API_URL:-}" ] || { echo "error: IRM_API_URL is not set" >&2; exit 1; }
[ -n "${IRM_TOKEN:-}" ] || { echo "error: IRM_TOKEN is not set" >&2; exit 1; }
if [ "${IRM_TOKEN#glsa_}" != "$IRM_TOKEN" ]; then
  [ -n "${GRAFANA_URL:-}" ] || { echo "error: GRAFANA_URL is not set (needed for a glsa_ token)" >&2; exit 1; }
  AUTH=(-H "Authorization: Bearer $IRM_TOKEN" -H "X-Grafana-URL: $GRAFANA_URL")
else
  AUTH=(-H "Authorization: $IRM_TOKEN")
fi

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

# NULs become newlines, not nothing: a header that shares a line with the NUL
# block still starts its own line. An entry is a well-formed header; the last
# Verdict: line wins. The header count goes to a file so a dropped entry shows.
tr '\000' '\n' < "$LOG" | awk -v hdrs="$T/hdrs" '
  function flush() { if (open) print ts "\t" ref "\t" id "\t" v; open = 0 }
  /^=== / { flush(); n++
            if (NF >= 5 && $4 ~ /^#/) { ts = $2; ref = substr($4, 2); id = $5; v = "page"; open = 1 }
            next }
  open && /^Verdict: / { w = tolower($2); gsub(/[^a-z]/, "", w); v = (w == "hold") ? "hold" : "page" }
  END { flush(); print n + 0 > hdrs }
' > "$T/entries"

# created_at has fractional seconds, which fromdateiso8601 rejects.
CLASSIFY='
  def epoch: sub("\\.[0-9]+"; "") | fromdateiso8601;
  (.created_at | epoch) as $c
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
awk -F'\t' -v hdrs="$(cat "$T/hdrs")" '
  function counts(n,   s, i) {
    for (i = 1; i <= nc; i++) s = s (i > 1 ? "  " : "") cats[i] "=" n[cats[i]] + 0
    return s
  }
  function rate(n) {
    d = n["page-ok"] + n["false-page?"]
    return d ? sprintf("%d/%d (%.0f%%)", n["false-page?"], d, 100 * n["false-page?"] / d) : "n/a (no page briefs with an outcome)"
  }
  BEGIN { nc = split("MISS? hold-ok page-ok false-page? open unknown", cats, " ") }
  { all[$9]++; total++
    if ($5 == "yes") { night[$9]++; nightn++ }
    if ($9 == "MISS?") miss[++m] = $1 "  #" $2 "  " ($7 == "" ? "?" : $7) " min  " $10 "  " $11 }
  END {
    printf "entries parsed: %d, headers seen: %d%s\n", total, hdrs, (total == hdrs ? "" : "  WARNING: entries were dropped")
    print "all:   " counts(all)
    print "       false-page rate " rate(all)
    printf "night: %s  (%d briefs; unknown rows have no night flag)\n", counts(night), nightn
    print "       false-page rate " rate(night)
    printf "MISS? (%d), ts / ref / minutes to first human action / title / link:\n", m
    for (i = 1; i <= m; i++) print "  " miss[i]
  }' "$T/rows"
