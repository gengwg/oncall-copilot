#!/usr/bin/env bash
# Deterministic incident investigator. Runs as an OpenClaw command payload.
#   1. Drains the alert-relay queue, investigates every firing alert in it
#   2. Runs real PromQL/LogQL evidence collection (scenario-appropriate)
#   3. For unfamiliar error strings, enriches with a Tavily web search
#   4. Asks Nemotron (via the sandbox inference route) to analyze
#   5. Writes an incident report to memory/incidents/
#   6. Prints a Telegram brief on stdout (delivered by the cron announce)
#
# Usage: investigate.sh ['<alert-json-line>']
set -uo pipefail

# The 30s cadence can overlap a slow run (Nemotron -m 150 + Tavily -m 20).
# Serialize with a lock so two runs never double-drain or clobber one report.
# If the lock is unavailable, proceed unlocked with a visible warning rather
# than silently NO_REPLY-ing every tick.
WORKSPACE="${WORKSPACE:-/sandbox/.openclaw/workspace}"
LOCKFILE="$WORKSPACE/.investigate.lock"
mkdir -p "$(dirname "$LOCKFILE")" 2>/dev/null || true
# Open the lock file before flock: a failed `exec 9>` does NOT abort the script,
# so an unguarded redirect leaves fd 9 closed and flock then fails with "Bad
# file descriptor" -> NO_REPLY on every tick, i.e. a silent outage.
if command -v flock >/dev/null 2>&1 && : >>"$LOCKFILE" 2>/dev/null && exec 9>>"$LOCKFILE"; then
  flock -n 9 || { echo "NO_REPLY"; exit 0; }
else
  echo "warn: cannot lock $LOCKFILE (flock missing or unwritable); running unlocked" >&2
fi

export PROMETHEUS_URL="${PROMETHEUS_URL:-http://172.18.0.1:19090}"
export LOKI_URL="${LOKI_URL:-http://172.18.0.1:13100}"
RELAY="${ALERT_RELAY_URL:-http://172.18.0.1:9099}"
# Bearer token for the relay's /alerts drain (same token Alertmanager uses to POST).
RELAY_TOKEN="${RELAY_TOKEN:-}"
PROM="$WORKSPACE/skills/promql-query/scripts/query.sh"
LOGQ="$WORKSPACE/skills/logql-query/scripts/query.sh"
INCIDENTS="$WORKSPACE/memory/incidents"
RUNBOOKS="$WORKSPACE/memory/runbooks"
# Read-only kubeconfigs named <cluster>.kubeconfig. Node evidence is collected
# only through these, never through whatever context the host defaults to.
KUBECONFIG_DIR="${KUBECONFIG_DIR:-}"
# Pre-filled DC hardware requests for real node faults. DC_PROVIDERS is a
# private TSV: cluster, site, channel name, channel ID, how tickets open.
DC_DRAFT_DIR="${DC_DRAFT_DIR:-}"
DC_PROVIDERS="${DC_PROVIDERS:-}"
MODEL="${MODEL:-nvidia/Nemotron-3-Ultra-550b-a55b}"
INFERENCE_URL="${INFERENCE_URL:-https://inference.local/v1/chat/completions}"

# Prometheus returns NaN for 0/0 -- a ratio computed over a window with no
# traffic -- and +Inf/-Inf for division by zero. Those are real result values,
# not empty results, so neither the jq fallback nor ${x:-n/a} catches them and
# "err_ratio=NaN" reaches the brief, where the model remarks on it instead of
# the incident.
# Telegram briefs are capped per field. Cutting at a byte boundary lops words in
# half ("and verify K"), which reads as a broken page rather than a short one.
clip() { # clip <max-chars>: flatten newlines, trim at a word boundary, mark elision
  tr '\n' ' ' | awk -v n="$1" '{
    gsub(/  +/, " ");
    if (length($0) <= n) { print; next }
    s = substr($0, 1, n);
    sub(/[^ ]*$/, "", s);
    sub(/[ ,;:.]+$/, "", s);
    print s "..."
  }'
}

norm() {
  case "$1" in
    ""|NaN|nan|+Inf|-Inf|Inf|inf) echo "n/a" ;;
    *) echo "$1" ;;
  esac
}

if [ -z "${NEBIUS_API_KEY:-}" ]; then
  echo "error: NEBIUS_API_KEY is unset; cannot reach $INFERENCE_URL" >&2
  echo "NO_REPLY"
  exit 1
fi

if [ $# -eq 0 ]; then
  auth_header=()
  [ -n "$RELAY_TOKEN" ] && auth_header=(-H "Authorization: Bearer $RELAY_TOKEN")
  body=$(mktemp)
  # Distinguish "relay said no alerts" from "relay refused us". Without the
  # status check a 401 (token drift) looks exactly like an empty queue, so
  # alerts pile up in the relay and nothing is ever investigated or paged.
  code=$(curl -s -m 8 -o "$body" -w '%{http_code}' "${auth_header[@]}" "$RELAY/alerts" 2>/dev/null) || true
  code="${code:-000}"
  batch=$(cat "$body"); rm -f "$body"
  if [ "$code" != "200" ]; then
    case "$code" in
      000) echo "error: alert-relay unreachable at $RELAY/alerts" >&2 ;;
      401) echo "error: alert-relay rejected our RELAY_TOKEN (HTTP 401) — token drift between the relay and this cron" >&2 ;;
      *)   echo "error: alert-relay drain failed (HTTP $code) at $RELAY/alerts" >&2 ;;
    esac
    echo "NO_REPLY"
    exit 1
  fi
  # The drain is destructive: the relay clears its queue on GET. Investigate
  # every firing alert in the batch or the rest are lost.
  mapfile -t alerts < <(printf '%s' "$batch" | jq -c '.[] | select(.status=="firing")' 2>/dev/null)
  [ "${#alerts[@]}" -eq 0 ] && { echo "NO_REPLY"; exit 0; }
else
  alerts=("$1")
fi

investigate_one() {
alert_json="$1"
alertname=$(printf '%s' "$alert_json" | jq -r '.alertname // "Unknown"')
service=$(printf '%s' "$alert_json" | jq -r '.service // ""')
# Sanitize like alertname: service lands in the runbook path and in queries.
service=$(printf '%s' "$service" | tr -c 'A-Za-z0-9_-' '_')
# Real-cluster alerts name a cluster and node instead of a service. Both land in
# a kubeconfig path and a field selector.
cluster=$(printf '%s' "$alert_json" | jq -r '.labels.cluster // ""')
cluster=$(printf '%s' "$cluster" | tr -c 'A-Za-z0-9_.-' '_')
node=$(printf '%s' "$alert_json" | jq -r '.labels.node // ""')
node=$(printf '%s' "$node" | tr -c 'A-Za-z0-9_.-' '_')
subject="${service:-${cluster:-unknown}${node:+/$node}}"
severity=$(printf '%s' "$alert_json" | jq -r '.severity // "warning"')
summary=$(printf '%s' "$alert_json" | jq -r '.summary // ""')
description=$(printf '%s' "$alert_json" | jq -r '.description // ""' | clip 600)
status=$(printf '%s' "$alert_json" | jq -r '.status // "firing"')
date=$(date -u +%Y-%m-%d)
# Sanitize: alertname is an untrusted label and lands in a path. A '/' or '..'
# would write outside $INCIDENTS.
alert_slug=$(printf '%s' "$alertname" | tr -c 'A-Za-z0-9_-' '_')
report="$INCIDENTS/$date-$alert_slug.md"
mkdir -p "$INCIDENTS"

[ "$status" != "firing" ] && return 1

runbook=$(cat "$RUNBOOKS/$service.md" 2>/dev/null || echo "no runbook")

# Latest earlier report for this alert, so the model can say "seen before".
prev=$(ls "$INCIDENTS"/[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]-"$alert_slug".md 2>/dev/null | tail -1)
prev_seen="none on file"
prev_cause="none"
if [ -n "$prev" ]; then
  prev_seen=$(basename "$prev" | cut -c1-10)
  prev_cause=$(awk '/^## /{f=($0=="## Root cause"); next} f' "$prev" | clip 400)
fi

# --- evidence collection (scenario-aware, read-only, bounded) ---
# Anchor the log window to the alert's start so repeated runs don't re-read the
# same stale lines.
starts_at=$(printf '%s' "$alert_json" | jq -r '.starts_at // empty')
lookback=15
start_s=0
if [ -n "$starts_at" ]; then
  start_s=$(date -d "$starts_at" +%s 2>/dev/null || echo 0)
  now_s=$(date +%s)
  [ "$start_s" -gt 0 ] && lookback=$(( (now_s - start_s) / 60 + 2 ))
  [ "$lookback" -gt 60 ] && lookback=60
  [ "$lookback" -lt 5 ] && lookback=5
fi

# When the rule's condition first held, before its `for:` wait: the closest
# signal we have to when the failure began.
pending_s=$($PROM instant "ALERTS_FOR_STATE{alertname=\"$alert_slug\"}" 2>/dev/null | jq -r '.value' | head -1)
pending_s="${pending_s%.*}"
case "$pending_s" in ""|*[!0-9]*) pending_s=0 ;; esac

# Reset per-alert so evidence from the previous alert in the batch can't leak in.
heap_trend=""
evidence_broken=""
# Always-collected baselines.
up=$($PROM instant "up{job=\"$service\"}" 2>/dev/null | jq -r '.value // "n/a"' | head -1)
err_ratio=$($PROM instant 'sum(rate(demo_errors_total[5m])) / sum(rate(demo_requests_total[5m]))' 2>/dev/null | jq -r '.value // "n/a"' | head -1)

# Scenario-specific signals. The alert name maps to the failure class.
case "$alertname" in
  *Latency*|*latency*)
    lat_mode=$($PROM instant 'demo_latency_mode' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
    heap=$($PROM instant 'demo_heap_alloc_bytes' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
    # latency chaos logs "slow request" WARNs; fall back to all logs if none in window
    logs=$($LOGQ tail "{service=\"$service\"} |~ \"slow request|WARN\"" "$lookback" 3 2>/dev/null | head -3)
    [ -z "$logs" ] && logs=$($LOGQ tail "{service=\"$service\"}" 15 3 2>/dev/null | head -3)
    ;;
  *Memory*|*memory*)
    lat_mode=$($PROM instant 'demo_latency_mode' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
    heap=$($PROM instant 'demo_heap_alloc_bytes' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
    # slope matters for a leak
    heap_trend=$($PROM range 'demo_heap_alloc_bytes' "$lookback" 15 2>/dev/null | jq -c '.points' | head -1)
    logs=$($LOGQ tail "{service=\"$service\"}" "$lookback" 3 2>/dev/null | head -3)
    ;;
  *Down*|*down*|*)
    lat_mode=$($PROM instant 'demo_latency_mode' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
    heap=$($PROM instant 'demo_heap_alloc_bytes' 2>/dev/null | jq -r '.value // "n/a"' | head -1)
    logs=$($LOGQ tail "{service=\"$service\"} |= \"ERROR\"" "$lookback" 3 2>/dev/null | head -3)
    ;;
esac
# `jq -r '.value // "n/a"'` yields an empty string, not "n/a", when the query
# returns no series at all (zero jq inputs -> zero output lines) — which is
# exactly the ServiceDown case. norm() also folds NaN/Inf to n/a.
up=$(norm "$up")
err_ratio=$(norm "$err_ratio")
heap=$(norm "$heap")
lat_mode=$(norm "$lat_mode")
err_logs="${logs:-}"

# Node evidence for real-cluster alerts. node-problem-detector puts the dmesg
# line in a node condition's message, so conditions away from their normal
# state are the "logs" for hardware faults; these clusters ship no logs to Loki.
node_info=""
node_note=""
if [ -n "$node" ]; then
  kc="$KUBECONFIG_DIR/$cluster.kubeconfig"
  if [ -z "$KUBECONFIG_DIR" ] || [ -z "$cluster" ] || [ ! -r "$kc" ]; then
    node_note="not collected (no read-only kubeconfig for cluster ${cluster:-unknown})"
  else
    k=(kubectl --kubeconfig "$kc" --request-timeout=10s)
    # node-problem-detector alerts name the node condition they fired on.
    # Whether it is still set is computed here: left to the model, the alert's
    # own wording wins over a condition that is simply missing from the list.
    alert_cond=$(printf '%s' "$alert_json" | jq -r '.labels.condition // ""')
    # A condition that was already False when the alert fired is a different
    # story (stale or misrouted alert) from one that cleared afterwards.
    # Kubernetes' own node conditions (MemoryPressure, ...) and
    # node-problem-detector's are normal when False. Health-check conditions
    # named like *Passed / *Healthy / *Completed read the other way round:
    # True is normal, False is the failed check. Without this a node lists
    # every passing check and hides the failed ones.
    node_info=$("${k[@]}" get node "$node" -o json 2>/dev/null | jq -r --arg cond "$alert_cond" --argjson fired "$start_s" '
      def normal: if (.type | test("(Passed|Healthy|OK|Available|Completed)$")) then "True" else "False" end;
      (if $cond == "" then empty else
        ([.status.conditions[]? | select(.type == $cond)] | first) as $c
        | if $c == null then "alert condition " + $cond + ": not reported by this node"
          elif $c.status == ($c | normal) then
            ($c.lastTransitionTime // "") as $at
            | (try ($at[0:19] + "Z" | fromdateiso8601) catch 0) as $t
            | if $fired > 0 and $t > 0 and $t >= $fired then "alert condition " + $cond + ": " + $c.status + " now, cleared after the alert fired (at " + $at + ")"
              elif $fired > 0 and $t > 0 then "alert condition " + $cond + ": " + $c.status + " since " + $at + ", already clear when the alert fired"
              else "alert condition " + $cond + ": " + $c.status + " now (last transition " + ($at | if . == "" then "unknown" else . end) + ")" end
          else "alert condition " + $cond + ": " + $c.status + " now, still active" end
      end),
      "ready: " + ([.status.conditions[]? | select(.type=="Ready") | .status + " (" + (.reason // "") + ")"] | first // "unknown"),
      "cordoned: " + ((.spec.unschedulable // false) | tostring),
      "taints: " + ([.spec.taints[]? | .key + ":" + .effect] | join(", ") | if . == "" then "none" else . end),
      ([.status.conditions[]? | select(.type != "Ready" and .status != normal)]
        | if length == 0 then "abnormal conditions now: none"
          else .[] | "condition " + .type + "=" + .status + ": " + ((.message // .reason // "") | .[0:240]) end)' 2>/dev/null)
    if [ -n "$node_info" ]; then
      pods=$("${k[@]}" get pods -A --field-selector "spec.nodeName=$node" -o json 2>/dev/null \
        | jq -r '"pods on node: " + ([.items[].status.phase] | group_by(.) | map(.[0] + "=" + (length | tostring)) | join(" "))' 2>/dev/null)
      events=$("${k[@]}" get events -A --field-selector "involvedObject.kind=Node,involvedObject.name=$node" -o json 2>/dev/null \
        | jq -r '.items | sort_by(.lastTimestamp // .eventTime // "") | .[-5:][]
          | "event " + (.lastTimestamp // .eventTime // "?") + " " + .reason + ": " + ((.message // "") | .[0:160])' 2>/dev/null)
      node_info=$(printf '%s\n%s\n%s' "$node_info" "$pods" "$events" | sed '/^$/d')
    else
      node_note="not collected (kubectl could not read node $node in $cluster)"
    fi
  fi
fi

# Every field n/a and no logs means the collectors are broken -- unreadable
# skill scripts, an unreachable Prometheus -- not a mysterious incident. The
# n/a fallback above is what makes that look like a normal quiet page, so say
# so loudly instead of shipping a brief reasoned over nothing.
if [ "$up" = "n/a" ] && [ "$err_ratio" = "n/a" ] && [ "$heap" = "n/a" ] \
   && [ "$lat_mode" = "n/a" ] && [ -z "$err_logs" ] && [ -z "$node_info" ]; then
  echo "error: no evidence collected for $alertname — every query returned nothing." >&2
  echo "  checked: $PROM, $LOGQ against $PROMETHEUS_URL and $LOKI_URL" >&2
  [ -x "$PROM" ] || echo "  $PROM is not executable" >&2
  [ -x "$LOGQ" ] || echo "  $LOGQ is not executable" >&2
  [ -n "$node_note" ] && echo "  node evidence $node_note" >&2
  evidence_broken=1
fi
err_string=$(printf '%s' "$err_logs" | head -1 | sed 's/^[^\t]*\t//' | jq -r 'try (.err // .msg // empty) catch empty' 2>/dev/null | head -1)

# --- Tavily enrichment for unfamiliar errors ---
web_context="not queried"
if [ -n "${TAVILY_API_KEY:-}" ] && [ -n "$err_string" ] && [ "$err_string" != "null" ]; then
  web_context=$(jq -n --arg q "$err_string $service" '{query:$q, max_results:2, api_key:env.TAVILY_API_KEY}' \
    | curl -sf -m 20 https://api.tavily.com/search -H "Content-Type: application/json" -d @- 2>/dev/null \
    | jq -r '[.results[]? | "- " + .title + ": " + (.content|tostring|.[0:180])] | join("\n")' 2>/dev/null)
  [ -z "$web_context" ] && web_context="no results"
fi

node_section=""
# Whether the alert's own condition cleared is computed above. What the model
# must not infer: faults read from metrics (NVLink, InfiniBand) never show in
# node status, and Ready says nothing about GPUs, so reading either as recovery
# would argue a real page down.
[ -n "$node" ] && node_section="
- node $subject (read now; only conditions away from normal are listed. Faults the alert reads from metrics, such as NVLink or InfiniBand, do not show here, so their absence is not a sign of recovery. Ready means the kubelet is healthy, not the GPUs):
${node_info:-$node_note}"

# --- Nemotron analysis ---
prompt="You are an SRE analyzing a live alert. Given the alert, live evidence, and web context, write a 2-sentence root-cause assessment, up to 2 other plausible causes the evidence rules out, and 1 suggested first action. Be specific; use the numbers. If the previous incident had the same cause, say so.

ALERT: $alertname ($severity) on $subject — $summary${description:+
RULE DESCRIPTION: $description}

LIVE EVIDENCE:
- up: $up
- error_ratio_5m: $err_ratio
- heap_alloc_bytes: $heap
- heap_trend: ${heap_trend:-n/a}
- latency_mode: $lat_mode$node_section
- recent logs (${lookback}m window from alert start):
$err_logs

WEB CONTEXT (for the error string):
$web_context

PREVIOUS INCIDENT FOR THIS ALERT ($prev_seen):
$prev_cause

RUNBOOK EXCERPT:
$(printf '%s' "$runbook" | head -20)

Reply format (plain text, seven lines):
ROOT CAUSE: <2 sentences>
CONFIDENCE: <high|medium|low> - <the one evidence item it rests on>
RULED OUT: <cause - evidence against it>; <cause - evidence against it>
ACTION: <1 sentence>
FAILURE MODE: <one word: change (deploy/config) | capacity (OOM, disk, CPU, quota) | dependency (upstream service or provider) | network (DNS, partition, LB) | hardware (GPU Xid/ECC, disk, NIC, node) | fabric (InfiniBand, NCCL) | control-plane (scheduling, operators) | other>
FAULT: <real|false-alarm> - <why, citing evidence>. false-alarm only if the evidence shows this is not a fault on this node: a telemetry or exporter artifact, a condition already clear when the alert fired, or a known benign signal. A real fault that is already contained is still real.
VERDICT: <page|hold> - <why, citing evidence>. page if a person must act before morning to stop customer impact or further damage; hold only if the evidence shows it is already contained (node cordoned and drained), cleared, or harmless until business hours. When unsure, page."

ask_model() {
  jq -n --arg m "$MODEL" --arg p "$prompt" \
    '{model:$m, messages:[{role:"user",content:$p}], max_tokens:1500}' \
    | curl -sf -m 150 "$INFERENCE_URL" \
        -H "Authorization: Bearer $NEBIUS_API_KEY" -H "Content-Type: application/json" \
        -d @- 2>/dev/null | jq -r '.choices[0].message.content // "analysis unavailable"'
}
# One retry: a single upstream blip would otherwise mark the brief degraded,
# hold the shadow heartbeat and lose that alert's DC draft.
analysis=$(ask_model)
case "$analysis" in ""|"analysis unavailable") sleep 2; analysis=$(ask_model) ;; esac

# Last match, not first: Nemotron reasons in the open and often echoes the
# reply template ("ROOT CAUSE: <2 sentences>") before its real answer.
rootcause=$(printf '%s' "$analysis" | sed -n 's/^ROOT CAUSE: *//p' | tail -1)
action=$(printf '%s' "$analysis" | sed -n 's/^ACTION: *//p' | tail -1)
confidence=$(printf '%s' "$analysis" | sed -n 's/^CONFIDENCE: *//p' | tail -1)
ruled_out=$(printf '%s' "$analysis" | sed -n 's/^RULED OUT: *//p' | tail -1)
[ -z "$rootcause" ] && rootcause="$analysis"
[ -z "$action" ] && action="See runbook."
[ -z "$confidence" ] && confidence="n/a"
[ -z "$ruled_out" ] && ruled_out="n/a"
# Said outright so the shadow poller can stop its heartbeat: a brief written
# without the model or without the node it is about still "succeeds".
degraded=""
case "$analysis" in ""|"analysis unavailable") degraded="model call failed" ;; esac
[ -n "$node_note" ] && degraded="${degraded:+$degraded; }node evidence $node_note"
fm=$(printf '%s' "$analysis" | sed -n 's/^FAILURE MODE: *//p' | tail -1 | awk '{print tolower($1)}')
case "$fm" in change|capacity|dependency|network|hardware|fabric|control-plane) ;; *) fm=other ;; esac
# Shadow-mode verdict, logged for comparison with what on-call actually did.
# Anything but an explicit hold reads as page: a wrong page costs sleep, a
# wrong hold costs an outage.
verdict_line=$(printf '%s' "$analysis" | sed -n 's/^VERDICT: *//p' | tail -1)
verdict=$(printf '%s' "$verdict_line" | awk '{print tolower($1)}')
case "$verdict" in
  page|hold) verdict="$verdict - $(printf '%s' "$verdict_line" | sed 's/^[^ ]* *-* *//')" ;;
  *) verdict="page - no verdict given, defaulting to page" ;;
esac
# Real versus false alarm is a separate call from page versus hold: a dead GPU
# on a drained node can wait for morning but still needs a ticket. Without a
# call it reads as real, since a needless draft costs a skim and a missing one
# costs a ticket.
fault_line=$(printf '%s' "$analysis" | sed -n 's/^FAULT: *//p' | tail -1)
fault=$(printf '%s' "$fault_line" | awk '{print tolower($1)}')
case "$fault" in
  real|false-alarm) fault="$fault - $(printf '%s' "$fault_line" | sed 's/^[^ ]* *-* *//')" ;;
  *) fault="real - no fault call given, treating as real" ;;
esac

# A real hardware or fabric fault on a node is a request to the DC provider.
# Draft it in the house format from what this read-only run can see; SSH
# evidence, the node's IP and provider ID, and the cordon call stay with the
# human finishing it, so the draft says what is missing. Nothing is sent.
dc_draft=""
if [ -n "$DC_DRAFT_DIR" ] && [ -r "$DC_PROVIDERS" ] && [ -n "$node" ] && [ "${fault%% *}" = real ]; then
  case "$fm" in hardware|fabric)
    prov=$(awk -F'\t' -v c="$cluster" '$1 == c' "$DC_PROVIDERS" | head -1)
    if [ -n "$prov" ]; then
      IFS=$'\t' read -r _ site chan chan_id ticket_note <<< "$prov"
      irm_url=$(printf '%s' "$alert_json" | jq -r '.irm_url // ""')
      irm_number=$(printf '%s' "$alert_json" | jq -r '.irm_number // ""' | tr -cd '0-9')
      mkdir -p "$DC_DRAFT_DIR"
      dc_draft="$DC_DRAFT_DIR/$date-${irm_number:+IRM$irm_number-}$node.md"
      {
        printf 'Hardware request — %s, %s: %s (cluster %s)\n\n' "$site" "$node" "$alertname" "$cluster"
        printf 'Node:    %s — TODO: IP and provider ID from the inventory\n' "$node"
        printf 'Serial:  TODO: sudo dmidecode -s system-serial-number on the node\n\n'
        printf 'Symptom: %s since %s UTC. %s\n' "$alertname" "${starts_at:-unknown}" "$(printf '%s' "$rootcause" | clip 400)"
        printf 'Node state read %s:\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        printf '%s\n' "$node_info" | sed 's/^/  /'
        printf '\nRequest: TODO: the specific action (check counters / reseat / replace / open an RMA). Copilot verdict: %s\n' "$verdict"
        printf 'Say whether the node stays in service. Physical work is the provider'"'"'s; drain belongs to whoever manages the cluster.\n\n'
        printf 'Please open a ticket for this and share the case number.\n\n'
        printf -- '---\nPost to: %s (%s) — %s\n' "$chan" "$chan_id" "$ticket_note"
        printf 'Draft only, nothing was sent. Finish with: /file-dc-request %s\n' "${irm_url:-<alert link>}"
        printf '(adds SSH evidence, the IP and provider ID, and the cordon decision, then drafts it in Slack)\n'
      } > "$dc_draft"
    fi ;;
  esac
fi

# Latest report for a different alert with the same failure mode: the same
# kind of failure can surface under another alert name.
related="none"
if [ "$fm" != "other" ]; then
  related=$(grep -lxF -- "- **Failure mode**: $fm" "$INCIDENTS"/*.md 2>/dev/null | while read -r f; do
    b=$(basename "$f" .md)
    case "$b" in ????-??-??-"$alert_slug") ;; *) echo "$b" ;; esac
  done | tail -1)
  [ -z "$related" ] && related="none"
fi

# Alert start -> brief ready: the head start the page gives a human. The
# condition start adds the rule's `for:` wait, which the page cannot beat.
now_s=$(date +%s)
hms() { if [ "$1" -gt 0 ]; then date -u -d "@$1" +%H:%M:%SZ; else echo n/a; fi; }
ttb="n/a"
[ "$start_s" -gt 0 ] && ttb="$(( now_s - start_s ))s"
ttb="$ttb after the alert fired"
[ "$pending_s" -gt 0 ] && ttb="$ttb, $(( now_s - pending_s ))s after the condition began"

# --- incident report ---
# Build with printf '%s' per field — never expand untrusted values (Tavily web
# results, Nemotron output, log lines, alert annotations) into a heredoc body,
# which would re-parse and execute $(...) / backticks.
{
  printf '# %s — %s\n\n' "$alertname" "$date"
  printf -- '- **Service**: %s\n' "$subject"
  printf -- '- **Severity**: %s\n' "$severity"
  printf -- '- **Status**: firing\n'
  printf -- '- **Summary**: %s\n' "$summary"
  printf -- '- **Time to brief**: %s\n' "$ttb"
  printf -- '- **Previous**: %s\n' "$prev_seen"
  printf -- '- **Confidence**: %s\n' "$confidence"
  printf -- '- **Failure mode**: %s\n' "$fm"
  printf -- '- **Verdict**: %s\n' "$verdict"
  printf -- '- **Fault**: %s\n' "$fault"
  [ -n "$degraded" ] && printf -- '- **Degraded**: %s\n' "$degraded"
  printf -- '- **Related**: %s\n\n' "$related"
  printf '## Root cause\n%s\n\n' "$rootcause"
  printf '## Ruled out\n%s\n\n' "$ruled_out"
  printf '## Timeline\n'
  printf -- '- condition true: %s\n' "$(hms "$pending_s")"
  printf -- '- alert fired: %s\n' "$(hms "$start_s")"
  printf -- '- briefed: %s\n\n' "$(hms "$now_s")"
  printf '## Evidence\n'
  printf -- '- up: %s\n' "$up"
  printf -- '- error_ratio_5m: %s\n' "$err_ratio"
  printf -- '- heap_alloc_bytes: %s\n' "$heap"
  printf -- '- heap_trend: %s\n' "${heap_trend:-n/a}"
  printf -- '- latency_mode: %s\n' "$lat_mode"
  [ -n "$node" ] && printf -- '- node %s:\n```\n%s\n```\n' "$subject" "${node_info:-$node_note}"
  printf -- '- recent logs (%sm window from alert start):\n```\n%s\n```\n\n' "$lookback" "$err_logs"
  [ -n "${evidence_broken:-}" ] && printf '> **Evidence collection failed** — every query returned nothing, so the\n> analysis below has no live data behind it.\n\n'
  printf '## Web context\n%s\n\n' "$web_context"
  printf '## Suggested first action\n%s\n\n' "$action"
  printf '## Full Nemotron analysis\n%s\n' "$analysis"
} > "$report"

# --- Telegram brief on stdout (cron announce delivers it) ---
printf 'ALERT: %s (%s)\nService: %s\nLikely cause: %s\nConfidence: %s\nVerdict: %s\nFault: %s\nPrevious: %s\nFailure mode: %s (related: %s)\nEvidence: err_ratio=%s heap=%s latency_mode=%s\nSuggested: %s\nBriefed %s\n' \
  "$alertname" "$severity" "$subject" "$(printf '%s' "$rootcause" | clip 200)" \
  "$(printf '%s' "$confidence" | clip 120)" "$(printf '%s' "$verdict" | clip 160)" "$(printf '%s' "$fault" | clip 160)" "$prev_seen" "$fm" "$related" \
  "$err_ratio" "$heap" "$lat_mode" "$(printf '%s' "$action" | clip 120)" "$ttb"
if [ -n "$degraded" ]; then
  printf 'Degraded: %s\n' "$degraded"
fi
if [ -n "$dc_draft" ]; then
  printf 'DC draft: %s\n' "$dc_draft"
fi
}

briefed=0
for a in "${alerts[@]}"; do
  [ -n "$a" ] || continue
  investigate_one "$a" && briefed=$((briefed + 1))
done
[ "$briefed" -eq 0 ] && echo "NO_REPLY"
exit 0
