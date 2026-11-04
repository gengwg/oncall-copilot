#!/usr/bin/env bash
# Grafana IRM adapter for poll.sh. Prints the newest alert groups as JSONL,
# oldest first, in the shape documented in poll.sh. On a failed fetch it prints
# one error line to stderr and exits non-zero.
#
# Usage: irm.sh               the newest 50 groups
#        irm.sh page <n>      page n of 50 (1 is the newest), for the history
#                             backfill; nothing once past the last page
#        irm.sh outcome <id>  one group's outcome as a JSON object:
#                             {id, ref, state, paged_at, acked_at, acked_by_person,
#                              resolved_at, resolved_by_person}
# Env: IRM_API_URL, GRAFANA_URL, GRAFANA_TOKEN (read-only service account).
set -uo pipefail

: "${IRM_API_URL:?set IRM_API_URL}" "${GRAFANA_URL:?set GRAFANA_URL}" "${GRAFANA_TOKEN:?set GRAFANA_TOKEN}"

mode="${1:-}"
case "$mode" in
  "") url="${IRM_API_URL%/}/api/v1/alert_groups/?perpage=50" ;;
  page)
    page="${2:-}"
    case "$page" in ""|*[!0-9]*) echo "error: page needs a number" >&2; exit 2 ;; esac
    url="${IRM_API_URL%/}/api/v1/alert_groups/?perpage=50&page=$page" ;;
  outcome)
    # The id lands in the URL path.
    gid=$(printf '%s' "${2:-}" | tr -cd 'A-Za-z0-9')
    [ -n "$gid" ] || { echo "error: outcome needs an alert group id" >&2; exit 2; }
    url="${IRM_API_URL%/}/api/v1/alert_groups/$gid" ;;
  *) echo "error: unknown mode $mode" >&2; exit 2 ;;
esac

body=$(mktemp)
trap 'rm -f "$body"' EXIT
# Only the first page by default, newest first. At a 60s cadence more than 50
# new groups between polls means a storm, and storm repeats are skipped anyway.
code=$(curl -s -m 20 -o "$body" -w '%{http_code}' \
  -H "Authorization: Bearer $GRAFANA_TOKEN" -H "X-Grafana-URL: $GRAFANA_URL" \
  "$url" 2>/dev/null) || true
# A refused token must not read as "no new alerts".
if [ "${code:-000}" != "200" ]; then
  echo "error: IRM alert_groups returned HTTP ${code:-000} from $IRM_API_URL" >&2
  exit 1
fi

if [ "$mode" = outcome ]; then
  # Timestamps lose their fraction so jq's fromdateiso8601 can read them. A
  # resolver of null means the alert itself cleared.
  jq -c 'def ts: if . == null then "" else sub("\\.[0-9]+"; "") end;
    {id, ref: (.alert_group_number | tostring), state,
     paged_at: (.created_at | ts), acked_at: (.acknowledged_at | ts),
     acked_by_person: (.acknowledged_by != null),
     resolved_at: (.resolved_at | ts), resolved_by_person: (.resolved_by != null)}' "$body" 2>/dev/null \
    || { echo "error: IRM alert_groups returned a body that is not an alert group" >&2; exit 1; }
  exit 0
fi

# Past the last page IRM answers with the last page again.
if [ "$mode" = page ] && [ "$(jq -r '.current_page_number // empty' "$body" 2>/dev/null)" != "$page" ] \
   && [ "$page" != 1 ]; then
  exit 0
fi

# The group's severity (for the filter) comes from the group payload; the
# alert's own severity is the firing alert's label. The customer label is
# dropped: it adds nothing to the evidence and it leaves the host.
# Each group is mapped on its own: one with an odd payload becomes an empty
# severity object, which poll.sh marks seen and the severity filter drops,
# instead of failing the poll for every group in the page.
jq -c '(.results | if type == "array" then reverse else error("no results") end) | .[] | . as $g
  | try (
      ((.last_alert.payload.alerts // []) | (map(select(.status == "firing")) | first) // .[0]) as $a
      | {id, ref: (.alert_group_number | tostring),
         url: (.permalinks.slack // .permalinks.web // ""), state, paged_at: (.created_at // ""),
         severity: (.last_alert.payload.commonLabels.severity // .last_alert.payload.alerts[0].labels.severity // ""),
         alert: {alertname: ($a.labels.alertname // "Unknown"),
                 severity: ($a.labels.severity // "warning"),
                 summary: ($a.annotations.summary // ""), description: ($a.annotations.description // ""),
                 labels: (($a.labels // {}) | del(.customer)), starts_at: ($a.startsAt // "")}}
    ) catch (
      {id: ($g | try .id catch null), ref: ($g | try (.alert_group_number | tostring) catch ""), url: "",
       state: ($g | try .state catch null), severity: "", alert: {}}
    )' "$body" 2>/dev/null \
  || { echo "error: IRM alert_groups returned a body that is not alert groups" >&2; exit 1; }
