#!/usr/bin/env bash
# Grafana IRM adapter for poll.sh. Prints the newest alert groups as JSONL,
# oldest first, in the shape documented in poll.sh. On a failed fetch it prints
# one error line to stderr and exits non-zero.
#
# Env: IRM_API_URL, GRAFANA_URL, GRAFANA_TOKEN (read-only service account).
set -uo pipefail

: "${IRM_API_URL:?set IRM_API_URL}" "${GRAFANA_URL:?set GRAFANA_URL}" "${GRAFANA_TOKEN:?set GRAFANA_TOKEN}"

body=$(mktemp)
trap 'rm -f "$body"' EXIT
# Only the first page, newest first. At a 60s cadence more than 50 new groups
# between polls means a storm, and storm repeats are skipped anyway.
code=$(curl -s -m 20 -o "$body" -w '%{http_code}' \
  -H "Authorization: Bearer $GRAFANA_TOKEN" -H "X-Grafana-URL: $GRAFANA_URL" \
  "${IRM_API_URL%/}/api/v1/alert_groups/?perpage=50" 2>/dev/null) || true
# A refused token must not read as "no new alerts".
if [ "${code:-000}" != "200" ]; then
  echo "error: IRM alert_groups returned HTTP ${code:-000} from $IRM_API_URL" >&2
  exit 1
fi

# The group's severity (for the filter) comes from the group payload; the
# alert's own severity is the firing alert's label. The customer label is
# dropped: it adds nothing to the evidence and it leaves the host.
jq -c '.results | reverse | .[]
  | ((.last_alert.payload.alerts // []) | (map(select(.status == "firing")) | first) // .[0]) as $a
  | {id, ref: (.alert_group_number | tostring),
     url: (.permalinks.slack // .permalinks.web // ""), state,
     severity: (.last_alert.payload.commonLabels.severity // .last_alert.payload.alerts[0].labels.severity // ""),
     alert: {alertname: ($a.labels.alertname // "Unknown"),
             severity: ($a.labels.severity // "warning"),
             summary: ($a.annotations.summary // ""), description: ($a.annotations.description // ""),
             labels: (($a.labels // {}) | del(.customer)), starts_at: ($a.startsAt // "")}}' "$body" \
  || { echo "error: IRM alert_groups returned a body that is not alert groups" >&2; exit 1; }
