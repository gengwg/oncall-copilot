# Real on-call adaptation: research notes

Status: research only, no code changed. Gathered 2026-10-25 with read-only
access to the team's Grafana Cloud stack (IRM, Prometheus, Loki), Slack, Notion,
the infra repos and local kubeconfigs. Nothing was acked, posted, silenced or
changed.

The hackathon demo stays as it is. This note asks what it would take to point
the same loop (alert in, evidence, model brief, page) at real on-call work,
mainly overnight.

## Source key

Internal sources are cited by placeholder so this file can live in a public
repo. The person who commissioned this note has the mapping.

| Placeholder | What it is |
|---|---|
| `<metarepo>` | the team's infra monorepo (private) |
| `<infra>` | its private infrastructure submodule that holds `terraform/grafana-alerting/` |
| `<skills>` | local Claude Code on-call skills (`oncall-start`, `oncall-sre:*`) |
| IRM query | a read-only `gcx irm oncall ...` or IRM plugin GET made through the grafana-cloud MCP on 2026-10-25 |
| Slack | a message in the paging channel, cited by IRM alert number and Pacific time |
| Notion | a page in the workspace, cited by title |

Repo paths without a placeholder are in this repo.

## Recommendation in brief

1. **Ingest by polling the IRM alert-group API**, not Slack and not an IRM
   outgoing webhook. Polling is outbound-only, so it works from a host behind
   NAT. It sees every alert group, including the ones IRM never posted to
   Slack, and it returns the alert-group ID the later steps need. The raw
   payload is already the Alertmanager shape `alert-relay` parses.
2. **Deliver the brief as a reply in the alert's Slack thread**, with no
   @-mentions overnight. Waking someone up is IRM's job, not Slack's.
3. **The copilot may add pages, never remove them.** Do not auto-ack. An ack
   stops escalation, and ack reminders are currently off. If warnings should not
   wake people at night, make that change in IRM's escalation chain, where the
   team can see it. The copilot's only overnight write is "this is real, page
   now", sent through the existing one-click paging integration.
4. **Evidence needs new credentials and new queries.** The agent needs its own
   read-only cluster identity. Loki has no logs from the GPU clusters. The evidence queries
   in `investigate.sh` only know the demo's `demo_*` series.
5. **Run it as a plain host service, not in NemoClaw**, at least until the
   upgrade and cron-loss problems in `docs/feedback.md` are fixed. Read-only
   credentials do more for safety than the sandbox does. With one model call
   per alert group, inference costs roughly $3-13/month at the real alert
   rate. An agent loop would cost about 40 times that. Choose the provider for
   overnight failover and zero data retention, not price.
6. **Start in shadow mode.** Briefs go to a local log first, then to the Slack
   thread, then to IRM notes. Paging comes last.

A Claude app already triages most firing alerts in the Slack thread, overnight
included (see "What already triages alerts"). The copilot's distinct value is
the things that app does not do: the overnight page-or-hold decision, cluster
evidence from kubectl, and incident memory. Duplicating its write-ups adds
nothing.

## How the real environment works

### Alert path

```
Grafana-managed alert rules (<infra>/terraform/grafana-alerting, OpenTofu)
  -> notification policy -> the IRM contact point (type oncall)
  -> IRM integration "Generic Integration" (grafana_alerting)
  -> route by commonLabels.severity
       critical -> chain "Daily Handoff Important" -> #grafana-alerts
       warning  -> chain "Daily Handoff Generic"   -> #grafana-alerts
       info     -> no chain                        -> #grafana-test
  -> Slack card posted by the Grafana IRM bot; schedule "Daily Handoff", handoff 08:00 PT
```

- Rules are Grafana-managed, not Prometheus/Alertmanager rules, and live as
  OpenTofu with one file per rule group (`<infra>/terraform/grafana-alerting/README.md:1-13`).
  About 192 rule blocks sit across 26 rule-group files. The literal severity
  labels are 23 critical, 83 warning and 36 info. The remaining rules set
  severity some other way, which I did not trace (`rg` count, unverified beyond that).
- Every rule notifies one contact point, of type `oncall`
  (`<infra>/terraform/grafana-alerting/main.tf:167-174`). The default policy
  groups by `grafana_folder, alertname`, with `group_wait 30s` and
  `repeat_interval 4h` (`main.tf:184-191`). A child policy mutes clusters that
  are not in service (`main.tf:193-209`, README "Which clusters page").
- The IRM routes on the Generic Integration filter on
  `payload.commonLabels.severity`. The critical and warning routes post to
  #grafana-alerts. The info route and the default route post to #grafana-test
  and have no escalation chain (IRM query: `channel_filters?alert_receive_channel=<generic>`).
- The two chains have the same steps. The critical chain marks every notify
  step Important and the warning chain uses default importance. The steps are:
  notify the schedule, wait 15 min, notify a named user, wait 30 min, notify a
  second named user (IRM query: `escalation_policies?escalation_chain=...`). No
  chain has a time-of-day step. This matches `<metarepo>/docs/alerts/README.md:42-61`:
  "Choosing `warning` gives up the out-of-hours override, not the notification."
- The Notion page with the draft on-call policy (2026-08-27) says only SEV1
  should page overnight and ticks "Route alerts by severity so only SEV1 pages
  off-hours" as done. The live chains above do not do that, so warnings still
  notify and escalate at night. Either the draft or IRM is out of date.
- Ack reminders are off. `slack_settings` reads `acknowledge_remind_timeout: 0`
  and `unacknowledge_timeout: 0` (IRM query, 2026-10-25), so an acked group
  never re-notifies. An earlier internal note recorded a 10h reminder. That note
  is now stale.
- No IRM outgoing webhooks are configured (`gcx irm oncall webhooks list` returned `[]`).

### Volume and the night profile

From one read-only listing of every IRM alert group, 2026-10-18 22:41Z to
2026-10-25 19:27Z (`gcx irm oncall alert-groups list --all --max-age 7d`, 746 groups):

| | Groups |
|---|---|
| Posted to #grafana-alerts (critical/warning, pages) | 274 (22 critical, 251 warning, 1 test P1) |
| Posted to #grafana-test (info, no page) | 377 |
| No Slack post at all | 95 |
| Pages started 22:00-08:00 PT | 61, of which 23 were ever acked |

- Volume is bursty. 193 of the 274 pages fell on two days (10-20 and 10-21).
  Most of those came from one storm: a telemetry label rename made
  `Node GPU Telemetry Absent` fire once per node (<metarepo> handover 2026-10-22, "Incidents").
- The most frequent night pages were `Node Telemetry Lost` (11), `Cluster Ingress
  Probe Loss From Region` (9), `IB: node has <50% active IB rails` (7), `GPU
  NVLink Link Down` (6), `External Probe Failing` (5), `GPU Xid Critical` (4) and
  `IB: <33% active rails` (4).
- Many groups resolve at the source within 10-30 minutes, before anyone acks.
  `Node Telemetry Lost` is the clearest case. The rule's own description warns
  that "RESOLUTION IS NOT RECOVERY" (Slack, IRM #5118, 2026-10-24 16:20 PT).
- 95 groups never got a Slack permalink, most of them in the 10-21 storm. I did
  not find out why. Whatever the cause, Slack is not a complete record of what
  IRM paged on. The oncall-start skill says the same: "A `new` group with no
  Slack permalink ... IRM still escalates it" (`<skills>/oncall-start/SKILL.md:37`).

### What an alert looks like

- **Slack card.** Posted by the Grafana IRM bot. It has a title with `#NNNN`
  and the label values in parentheses, the firing and resolved counts, a
  Runbook link, GroupLabels, CommonLabels (`cluster`, `node`, `severity`,
  sometimes `customer`), the annotations (`summary`, `description`, sometimes
  `runbook_url`), and Acknowledge, Resolve and "Investigate this alert" buttons
  (Slack, IRM #5140, 2026-10-25 10:43 PT).
- **IRM raw payload.** The Alertmanager webhook shape: `status`, `alerts[]`
  (`labels`, `annotations`, `startsAt`, `fingerprint`, `generatorURL`,
  `ruleUID`), `commonLabels`, `commonAnnotations`, `groupKey`, and
  `receiver` set to the IRM contact point (IRM query: `alert-groups list-alerts <id> --include-raw`).
  `alert-relay/main.go:18-28` already parses these fields.
- **Runbooks.** Rules carry a short description plus a `runbook_url` that
  points to `<metarepo>/docs/alerts/<slug>.md`. That directory has 57 pages,
  and 49 rules set the annotation (`<metarepo>/docs/alerts/README.md:6-10`).
  More runbooks live in a Notion "Runbooks" page.
- Severity means blast radius. Critical means "a human must act before
  morning", plus three progressive single-node faults (thermal)
  (`<metarepo>/docs/alerts/README.md:42-78`).

### What already triages alerts

- **A Claude app in Slack.** It replies in the alert thread within about a
  minute with a status line and then a verdict. It reads Grafana and Slack
  history, has no kubectl, and ends with "I haven't changed anything". It does
  this overnight too. Examples: IRM #5131 at 02:04 PT, #5140 at 10:43 PT, and
  NVLink triage at 00:11 and 04:11 PT on 2026-10-24 (Slack). Its write-ups
  already note when "the page went to" the next person in the escalation. How
  it is triggered, and its channel instructions, were not inspected.
- **A team rule for that app.** After it messaged someone during a night of
  benign probe pages, a teammate set the rule that it "may not under any
  circumstances @ anyone but on-call in the middle of the night unless
  specifically instructed to" (Slack, #grafana-alerts, 2026-10-02 07:58 PT).
- **Grafana Assistant.** Each card has an "Investigate this alert" button.
  IRM also has a "Grafana Assistant for IRM" webhook preset that starts an
  investigation on every new alert group and posts a note back
  (https://grafana.com/docs/grafana-cloud/platform/grafana-assistant/configure/irm-webhooks/).
  That preset is not enabled here (no outgoing webhooks exist).
- **A manual watch loop.** `/loop 15m /oncall-start watch` acks every new page
  during a shift (`<skills>/oncall-start/SKILL.md:33-39`). The skill notes that
  "an ack stops escalation to the next person on the schedule, so nothing acked
  may go unreported" (`oncall-shift-briefing/SKILL.md:57`).

## 1. Ingest

| Option | Reachability | Coverage | Gives the alert-group ID | Notes |
|---|---|---|---|---|
| **Poll the IRM API** (`GET /api/v1/alert_groups/` or `gcx irm oncall alert-groups list --state new`) | Outbound HTTPS only. Works from a laptop or sandbox | Every group, including ones without a Slack post | Yes | Latency is the poll interval. 30-60s is fine next to `group_wait 30s`. Needs a service-account token |
| IRM outgoing webhook (trigger "Alert Group Created", or a "Trigger webhook" chain step) | Needs a **public** URL. IRM refuses private, loopback and CGNAT addresses and enforces a 4s timeout | Every group the trigger matches | Yes, `{{ alert_group.id }}` | Push, low latency. Retries only on timeout, up to 3 times 1s apart. The demo's quick-tunnel URL rots, and there is no domain for a named tunnel |
| Read Slack (Socket Mode events or `conversations.history`) | Socket Mode is outbound WebSocket, no public URL | Misses groups with no Slack post (95 in the sample week) | Must be parsed from the card's link | Card text is a rendering. It is truncated and its format can change |
| A second Grafana contact point (webhook) beside the IRM one | Needs a public URL | Only rules routed to it | No IRM ID | It is a terraform change to the paging path, and it bypasses IRM state |

Sources: IRM outgoing webhooks
(https://grafana.com/docs/grafana-cloud/observe-and-act/respond-to-incidents/integrations/custom-integrations/outgoing-webhooks/,
sections "Webhook URL requirements", "Supported triggers", "Retry policy");
the alert groups API
(https://grafana.com/docs/grafana-cloud/observe-and-act/respond-to-incidents/reference/oncall-api/alertgroups/);
Slack Socket Mode (https://docs.slack.dev/apis/events-api/using-socket-mode).

**Recommendation: poll the IRM API.** It is the only option that needs no
public endpoint and still sees every page. It also returns the ID that notes
and escalation need. The change to this repo is small. Replace the relay drain
in `investigate.sh:72-94` with a poll that records which group IDs it has seen,
and keep the per-alert fields `alert-relay` already produces. Revisit the
webhook only if a stable public endpoint appears, for example a small cloud
function that queues events for the copilot to pull.

## 2. Deliver

- **Brief goes in the alert's thread.** The IRM `permalinks.slack` value
  carries the channel and parent `ts` (`.../archives/<channel>/p<ts>`). Post
  with `chat.postMessage` and `thread_ts`, and leave `reply_broadcast` off. That
  puts the brief next to the card and next to the Claude app's write-up, where
  the on-call reads in the morning
  (https://docs.slack.dev/reference/methods/chat.postMessage).
- **Do not DM to wake someone.** A Slack DM is a quiet notification. Waking a
  person should go through IRM's Important notification rules, which already
  exist per user. A morning DM digest of the night is optional and low risk.
- **No @-mentions between 22:00 and 08:00 PT** unless a person in the thread
  asks. This is the same rule the team set for the Claude app.
- **Bot token scopes.** `chat:write` to post in threads, after inviting the bot
  to #grafana-alerts (`chat:write.public` avoids the invite). Add
  `channels:history` only if the copilot reads Slack, for example to read the
  Claude app's verdict for comparison (https://docs.slack.dev/reference/methods/conversations.history).
  No `reactions:write`. The card says reacting with :robot_face: turns a thread
  message into an IRM note, which is a write into IRM and should stay a human action.
- **Split of duties.** IRM owns routing, escalation, acks and who gets woken.
  The copilot owns the brief and at most one extra page per incident (section 3).
  Avoid two bots posting the same analysis. The copilot's thread reply should
  carry what the Claude app cannot see: kubectl evidence, the copilot's
  page-or-hold verdict, and the related-incident link.

## 3. Sleep

### Why auto-ack is the wrong first move

- An ack stops the escalation chain, and ack reminders are off
  (`acknowledge_remind_timeout: 0`). An auto-acked group would never notify
  anyone again, even if the brief was wrong.
- The rules themselves warn that resolve does not mean recover
  (`Node Telemetry Lost`). Some conditions latch and only clear on a reboot or a
  restart of node-problem-detector (Slack card for `GPU Xid Critical`, IRM #5131).
  A model that sees "resolved" or "quiet now" can be wrong in exactly the cases
  that matter.
- The API permission that allows a resolution note, `grafana-irm-app.alert-groups:write`,
  also allows ack, resolve, silence and delete
  (https://grafana.com/docs/grafana-cloud/observe-and-act/respond-to-incidents/reference/oncall-api/resolution_notes/).
  The token cannot express "notes only", so the limit has to be enforced in
  code. That is a weaker guarantee, and another reason to keep IRM writes few.

### Proposed shape: IRM holds, the copilot can only escalate

1. **Decide in IRM, not in the copilot, whether warnings page at night.** The
   draft policy already says they should not. IRM has escalation steps for it:
   "Continue escalation if current UTC time is in range" (type 12) and "Pause
   escalation until team's working hours" (type 27) (IRM query:
   `escalation-policies list-step-types`). Adding one of them to the warning
   chain is a team decision and a reviewed IRM change. It is not part of this
   repo. Critical stays as it is.
2. **The copilot's one overnight write is an upgrade.** When the brief says a
   held warning is real and will get worse before morning, the copilot posts to
   the existing "Page on-call (one-click)" integration with `important: true`.
   That integration routes `{{ payload.important }}` to the Important chain and
   posts to #grafana-alerts (IRM query: `channel_filters?alert_receive_channel=<one-click>`;
   payload example `{"important": false, "message": ..., "state": "alerting", "title": ...}`
   from `get-templates`). It is an inbound webhook, so a local host can reach
   it. Put the original alert number and the brief in `message`.
   `escalate_next` is the other option, but whether it skips a "pause until
   working hours" step is unverified.
3. **Attach the brief to the alert group**, by thread reply first and later by
   resolution note (`POST /api/v1/resolution_notes/`), so the morning on-call
   sees the reasoning in IRM as well as Slack.

This shape fails safe. If the copilot is down, warnings wait until morning,
which is the policy the team already wrote down, and critical pages as before.
If the copilot is wrong in the "real" direction, someone is woken for nothing.
It can never be wrong in the "quiet" direction, because it has no quiet action.

### Phase 1 amendment (2026-10-31): acks on the owner's own shift

Weigang decided to run a personal Slack agent (`slack-agent/`) that acks new
pages, under his own IRM token, only while the IRM schedule says the shift is
his and only after the brief has been delivered to him as a Slack DM. The
reasoning above still holds for everyone else's shifts, where the agent never
acks. What replaces the escalation chain during his shift is the DM thread:
every ack is announced in the thread of the brief it belongs to, so nothing
acked is unreported, and a dead agent, a failed DM or a stale schedule all fail
closed to "not acked, IRM escalates as before". The agent's IRM writes are the
two acknowledge endpoints, enforced by an HTTP transport allowlist in
`slack-agent/internal/irm` with a unit test, not by convention. Hardware briefs
also trigger the metarepo `/file-dc-request` skill, headless and draft-only.

### Safety limits for unattended runs

- No ack, resolve, silence, unsilence, delete, label or maintenance-mode
  calls. Enforce this with an allowlist of the exact endpoints the code may
  call: read alert groups, post a resolution note, POST to the one-click
  integration.
- Kubernetes read-only: get/list/watch only, through a dedicated
  ServiceAccount (section 4). Never the admin kubeconfigs. Never exec or nvidia-smi.
  The rule descriptions warn that nvidia-smi can hang on some nodes.
- At most one copilot page per alert group and a small cap per night, for
  example 3. Above the cap, post one summary and stop. A storm like 10-21
  should not turn into a storm of upgraded pages.
- No @-mentions overnight. Slack replies in threads only.
- Never say who will fix something. Remediation is split by cluster between the
  team and a customer. The brief should name the owner from data, not promise
  action.
- A heartbeat that something else watches. The OpenClaw cron can stop silently
  (`docs/feedback.md` items 10 and 12). A dead copilot is safe under the shape
  above, but someone should still know it is dead.
- No public publishing. `deploy/sync-incidents.sh:6-13` commits every report
  to this public repo by default. A real deployment must set `PUBLISH_REPO=""`
  or not run the sync, and keep reports out of `deploy/dashboard/incidents/`.
- No Tavily, or only with node, cluster and customer identifiers stripped.
  `investigate.sh:200-207` sends the first error-log line to a web search API.

### How the failure-mode feature (#38) helps

- The taxonomy in the prompt (`investigate.sh:237`) already covers what pages
  at night: `hardware` (GPU Xid, ECC, NVLink), `fabric` (InfiniBand rails),
  `network` (ingress and egress probes), `capacity`, `control-plane`. Tagging
  each brief gives the morning summary a shape: "4 hardware, 1 network, 0 new
  failure modes".
- The related-incident link (`investigate.sh:258-267`) is the strongest
  overnight signal in the existing code. A night page on a node or cohort that
  is already a known, ticketed fault, such as the long-uptime vBIOS NVLink case
  the Claude app keeps identifying, can wait until morning. A new failure mode
  should page.
- Two changes are needed first. "Previous" is keyed on `alertname` alone
  (`investigate.sh:120`), and "Related" is keyed on failure mode plus a
  different alertname. In this fleet the same alertname fires on dozens of
  nodes, so both should key on `cluster` and `node` too (same node, any alert,
  last 30 days). Also, the label comes from the model. It is good for
  enrichment and should not be the only input to a hold decision.
- The handover doc keeps a ledger of open hardware items, and silences carry
  ticket numbers in their comments. Matching a page against those is a cheaper
  and more reliable "known issue" signal than model output. Where the
  ledger's tooling lives was not verified. It is not in `<metarepo>`.

## 4. Evidence

### Prometheus

- The stack's Prometheus is the `grafanacloud-prom` datasource (IRM query: `list_datasources`).
- **Simplest path:** query through Grafana's datasource proxy,
  `GET <stack>/api/datasources/proxy/uid/grafanacloud-prom/api/v1/query`, using
  a dedicated service-account token with the Viewer role. I verified this path
  returns standard Prometheus JSON
  (`count by (cluster)(up{job="integrations/kubernetes/kubelet"})` returned 15
  clusters). The skill scripts take `PROMETHEUS_URL` and build `/api/v1/query`
  under it (`skills/promql-query/scripts/query.sh:8-16`), so setting the URL to
  the proxy prefix works. The scripts send no auth header, so they need one
  optional `Authorization` header from the environment. The same change goes in
  `skills/logql-query/scripts/query.sh`.
- Alternative: a Grafana Cloud access policy token with `metrics:read` against
  the hosted Prometheus endpoint. Unverified here, and it needs the instance ID.
- `ALERTS_FOR_STATE` (`investigate.sh:144`) does not cover Grafana-managed
  rules. Their state is in `GRAFANA_ALERTS` (68 series, with alertnames
  matching the rules). `ALERTS_FOR_STATE` holds no `GPU.*` or `Node.*` series.
  "Condition began" time should come from `GRAFANA_ALERTS` or the alert-state
  history Loki datasource (`grafanacloud-alert-state-history`).
- The scenario queries (`investigate.sh:152-176`) read `demo_*` series. Real
  pages need per-family queries keyed on the rule. The descriptions and
  runbooks already name them: `node_boot_time_seconds` and the cordon reason
  for NVLink/Xid, `up{job="integrations/kubernetes/kubelet"}` for telemetry
  loss, `node_infiniband_*` for rails, DCGM ECC and row-remap series for GPUs.
  A small table from rule UID or alertname to a query set, kept beside the
  runbook pages, is less work than teaching the model to write PromQL.

### Loki

- Over the last 7 days the `grafanacloud-logs` datasource holds logs only from
  the two federation clusters (`cluster` label values; `job` values are
  `cluster-ingress` and one app job). **No logs come from the GPU
  clusters that produce most pages.** LogQL evidence would be empty for them,
  and `investigate.sh:186-197` would report "every query returned nothing".
- For node faults the useful "log" is the node condition message.
  node-problem-detector puts the dmesg line there (Slack card for
  `GPU Xid Critical`: "the condition message carries the offending dmesg
  line"). Get it with `kubectl get node -o json`, not Loki.

### Kubernetes

- The existing local kubeconfigs are operator credentials. **They must not be
  given to an unattended agent**; it needs its own read-only identity.
- A read-only identity already exists as a template. `dashboard-viewer` is a
  ServiceAccount with get/list on nodes, pods, services, namespaces, workloads,
  Argo CD applications and a few CRDs, and no secrets, logs, exec or writes. It
  has a script that mints short-lived tokens without a Secret
  (`<infra>/tools/cluster-dashboard/template/manifests/serviceaccounts/dashboard-viewer.yaml:1-19`).
  It is in two clusters' inventories so far. The copilot needs the same role
  plus `events` get/list. Rolling it out is a per-cluster apply by a human,
  with a manifest diff first.
- Which clusters: the ones with `in_service: true` in
  `<infra>/terraform/grafana-alerting/cluster_alerting.auto.tfvars.json`,
  because only those page (`README.md:19-31`). Read that file instead of
  keeping a second list.
- The copilot's host needs network access to each in-service API server.

### Runbooks and memory

- Prefer the alert's `runbook_url`, read from the local `<metarepo>` checkout
  of `docs/alerts/`, over `memory/runbooks/<service>.md` (`investigate.sh:117`).
  Real alerts have no `service` label, so today's lookup falls back to
  "no runbook" every time.

## 5. Runtime

**Recommendation: run the real copilot as a plain service on an always-on
host. Keep NemoClaw/OpenShell for the demo.**

Against NemoClaw for this job, from `docs/feedback.md`:

- `rebuild` reports success, exits 1 and drops cron jobs, so "an unattended
  agent silently stops running" (item 10).
- An upgrade destroyed a working sandbox over a rotated credential, and the
  backups taken for it could not be restored (item 13). Upgrades are also on
  hold until after the hackathon.
- A channel delivery failure shows up only as a cron `error` on the ticks that
  had output (item 12).
- Cron `--command-env` stores secrets in plaintext in the job spec (item 7,
  `deploy/setup-automations.sh:56-66`).
- Unattended agent turns did not reliably call tools, which is why the loop is
  a deterministic script that calls the model directly (item 5). That script
  does not need an agent runtime.
- The demo host runs the judged submission until mid-December. A second
  sandbox there risks the port and forward collisions in items 1 and 9.

What the sandbox gives, and how to replace it:

- **Deny-by-default egress.** The real loop needs the Grafana stack, the IRM
  API host, Slack, the inference endpoint and every in-service cluster API
  server. That is a long allowlist (compare
  `deploy/policy-local-observability.yaml:8-34`, which lists three local
  endpoints). The larger risk is what the credentials can do, and read-only
  tokens cap that. A host firewall or a single-purpose VM can restrict egress
  if the team wants it.
- **Secret isolation.** Use a root-owned env file read by a systemd unit, or
  the team's existing secret store. Not the cron job spec.

Reconsider NemoClaw after the hackathon if `rebuild` keeps cron jobs and
upgrades stop destroying sandboxes. The network-policy model is the part worth
keeping (`docs/feedback.md`, "Build with it again").

Outside the sandbox there is no `inference.local` route, so `INFERENCE_URL`,
the API key and `MODEL` point at the provider directly (`investigate.sh:38-39`).
Briefs carry node, cluster and customer pod names. Prod data already goes to
OpenRouter, so data policy does not block that option.

### Cost

The demo's last 30 days on Token Factory cost $18.58: 17.76M input tokens
($17.76) and 0.27M output tokens ($0.82). That is about 66:1 input to output,
an effective ~$1.00/M input and ~$3.0/M output (Token Factory console, read by
the commissioner).

**Where the input went (inference, not verified per request).** The
`investigate.sh` prompt is small. The template is ~1.2 KB (`investigate.sh:210-237`),
and it adds a few evidence numbers, 3 log lines, about 400 characters each of
web and previous-incident text, and the first 20 runbook lines (~0.5 KB). That
is roughly 1k tokens. It does not send skills or `memory/` beyond the previous
report's root cause. Output per call is capped at 1,500 tokens
(`investigate.sh:240`). Assume ~500 output tokens per call (reasoning plus the
five-line answer) and that every output token came from an investigation,
which overstates the count. Then 0.27M output tokens is about 540 calls, or
~0.5M of the 17.76M input tokens. The rest most likely came from OpenClaw agent
sessions: Telegram chat follow-ups and the agent-turn cron experiments in `docs/feedback.md` item 5,
which "loops `search_code` errors indefinitely". Each turn there re-sends the
framework prompt, the workspace files and the growing transcript. To confirm,
compare the console's request count with (input tokens / ~1k).

**Real volume.** From the IRM listing above:

| Scope | Alert groups | Calls per month |
|---|---|---|
| Night pages only (22:00-08:00 PT) | 61/week | ~270 |
| All pages, a normal week (outside the 10-20/21 storm) | ~16/day | ~480 |
| All pages, the sampled week including the storm | 274/week | ~1,200 |
| All pages plus info (#grafana-test) | 651/week | ~2,800 |

A real one-shot call is bigger than the demo's. Estimate ~6k input tokens (a
runbook triage section ~1.5k, a projected kubectl node excerpt ~2k, Prometheus
results ~1k, the template and related-incident lines ~1.5k) and ~1.5k output
tokens with reasoning. At Token Factory's effective rate that is about $0.011 per
alert group: roughly **$3/month for nights only and $13/month for every page**,
even in a storm week.

The design matters far more than the volume. An agent loop that re-sends a
growing context, say 15 tool steps averaging 25k tokens, uses ~375k input
tokens per alert, about $0.38 each, or **~$450/month** at the storm-week rate.
That is the demo's 66:1 pattern scaled up.

Levers, largest first:

1. **One model call per alert group.** Code collects the evidence and the
   model only writes the brief, which is the design `docs/architecture.md`
   already chose. No tool-calling loop, and no chat session around it.
2. **Investigate each group once.** Skip re-notifications, groups that resolved
   before the poll, and info severity. Collapse a storm into one investigation
   per `alertname` and `cluster` per window. The 10-21 storm was ~100 groups
   with one cause.
3. **Send the runbook's triage section, not the page.** Runbook pages are a
   median 5.8 KB, and the busiest one (node-problem-detector conditions) is
   22 KB, or ~5.6k tokens (`wc -c` on `<metarepo>/docs/alerts/`).
4. **Project the evidence in code.** Send `kubectl get node -o json | jq` to the
   conditions, cordon labels and pods on the node, not `-o yaml`. Send
   Prometheus values as numbers, not raw JSON. This repo already does that for
   the demo series.
5. **Keep skills, `TOOLS.md` and `memory/` out of the per-alert call.** Send only
   the matched previous and related report summaries, capped at about 400
   characters each as now (`investigate.sh:125`).
6. **Put the static prefix first** (instructions, reply format, failure-mode
   list), so providers that price cached input lower can reuse it. OpenRouter
   and Infron list cache-read prices below. Whether Token Factory caches was not
   verified.

### Inference provider

All three expose an OpenAI-compatible chat-completions API, so switching is the
three variables above plus the model ID's case (`nvidia/Nemotron-3-Ultra-550b-a55b`
on Token Factory, `nvidia/nemotron-3-ultra-550b-a55b` on the routers).

| | Token Factory (demo) | OpenRouter | Infron |
|---|---|---|---|
| What it is | Nebius-hosted inference | Router across upstream providers | Router, base URL `https://llm.onerouter.pro/v1` |
| Nemotron 3 Ultra price, $/M in / out | ~1.00 / ~3.0 effective (console) | 0.60 / 2.40 list. Per provider: DeepInfra fp4 0.50 / 2.20 (flex 0.40 / 1.76), Venice fp8 0.625 / 3.125 | 0.50 / 2.50 |
| Cached input, $/M | not verified | 0.08-0.19 depending on provider | 0.15 |
| Context / max output | not verified | 262,144 / 16,384 (DeepInfra) | 262,144 / 16,400 |
| Upstreams | itself | several, including DeepInfra and Venice | DeepInfra only |
| Data handling | not verified | OpenRouter keeps no prompts unless you opt in to logging. `"provider": {"zdr": true}` per request, or an account setting, restricts routing to zero-retention endpoints | Claims zero retention and no training by default. SOC 2 Type II audit in progress |
| Est. cost, ~1,200 calls/month (6k in / 1.5k out) | ~$13 | ~$7.6 (DeepInfra) to ~$8.6 (list) | ~$8.1 |

Sources: OpenRouter models and endpoints API
(`https://openrouter.ai/api/v1/models`, `https://openrouter.ai/api/v1/models/nvidia/nemotron-3-ultra-550b-a55b/endpoints`,
read 2026-10-25), OpenRouter ZDR (https://openrouter.ai/docs/guides/features/zdr),
the Infron model page (https://infron.ai/models/nvidia/nemotron-3-ultra-550b-a55b)
and Infron facts the commissioner checked on infron.ai. Token Factory's public
price page redirects to a logged-in console, so its row uses the demo's billed
rates.

Notes:

- At this volume the price differences are a few dollars a month. Choose on
  overnight availability and data handling, not price.
- A router with more than one upstream can fail over when one provider is
  down. Infron currently has one upstream for this model, and Token Factory is
  one provider. At 03:00 with nobody watching, that is the property that
  matters. If OpenRouter is used, set `zdr: true` and allow fallbacks.
- Quantization differs. OpenRouter lists DeepInfra at fp4 and Venice at
  fp8. Infron's page lists its DeepInfra upstream at fp16, which
  conflicts with OpenRouter's fp4 listing for the same provider (unverified
  which is current). Token Factory's precision was not checked. Phase 1 shadow
  runs can compare brief quality before the provider choice is fixed.
- OpenRouter also lists a `:free` variant. Do not use it for prod data unless
  its endpoint's data policy has been checked; I did not check it.

## 6. Phased plan

Each phase has an exit test. Do not move on until it passes.

**Phase 0: prerequisites (no runtime yet)**
- Create a Grafana service account with the Viewer role (Prometheus proxy) and
  an IRM token with read access. Write access waits until Phase 3.
- Write the `dashboard-viewer`-plus-events role and apply it to one in-service
  cluster, after a human reviews the manifest diff.
- Make the evidence scripts send an optional auth header. Turn off Tavily and
  `sync-incidents.sh` publishing for this deployment.
- Exit: one hand-run `investigate.sh` on a real resolved alert produces a
  report with non-empty evidence and no admin credentials anywhere on the host.

**Phase 1: shadow, local only (1-2 weeks)**
- Poll IRM for new groups in #grafana-alerts. Write briefs to a local
  directory only. No Slack, no IRM writes.
- Each morning, compare the copilot's verdict (real or hold, failure mode,
  related incident) with what happened: the human's action, the Claude app's
  thread, the handover note.
- Exit: across all night pages in the window, no "hold" verdict on an alert
  that needed action before morning. Record the false "real" rate.

**Phase 2: shadow in Slack**
- Post the brief in the alert's thread with no mentions, marked as a shadow
  verdict. Still no IRM writes.
- Exit: the on-call team finds it useful next to the Claude app's write-up,
  meaning it adds kubectl evidence or a correct related-incident link at least
  sometimes. If it only repeats the Claude app, stop and fold the evidence work
  into that app instead.

**Phase 3: notes in IRM**
- Add the brief as a resolution note on the alert group, with an IRM token that
  has `alert-groups:write` and a code allowlist of the one endpoint.
- Exit: notes appear in IRM and in the morning handover, and an audit of the
  token's calls shows only note creation.

**Phase 4: overnight hold and upgrade (team decision)**
- The team changes the warning chain to hold at night (step type 12 or 27).
  This is reviewed like any other IRM change.
- The copilot gets the one-click Important page with the per-night cap.
- Exit: one month with no missed real night incident and fewer night wake-ups
  than the Phase 1 baseline.

**Not planned:** auto-ack, auto-resolve, silences, or any cluster write. The
first three are reachable with the Phase 3 token, so the limit has to stay in
code and in review.

## Unverified and open questions

- How the Claude app in Slack is triggered, what its channel instructions say,
  and whether its Grafana access could be extended to read-only kubectl, which
  might make a separate copilot unnecessary.
- Why 95 alert groups in the sample week have no Slack permalink.
- Whether `escalate_next` skips a "pause until working hours" step.
- Whether IRM shows an API-created resolution note in the Slack thread.
- The hosted Prometheus endpoint and access-policy path (not tested; the
  datasource proxy was).
- Whether the draft on-call policy or the live IRM chains reflect what the team
  currently intends for warnings at night.
- Data-handling rules for sending production telemetry to a third-party model.
- Where the on-call ledger tooling referenced in the handover lives.

## Public-repo sensitivity

The commissioner should check this file before committing it. It names a Slack
channel (#grafana-alerts, #grafana-test) and IRM chain and integration names.
Those are internal but not secret. It
deliberately leaves out cluster names, customer and vendor names, people,
Slack and IRM object IDs, the Grafana stack URL, internal repo URLs and Slack
permalinks. The source key at the top replaces them.
