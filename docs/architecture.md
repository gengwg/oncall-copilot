# Architecture (as built)

## Data flow

```
minikube (demo env)
+----------------------+
| demo-app (/chaos)    |
| Prometheus (alerts)  |
| Alertmanager --webhook-----+        host
| Loki (logs)          |     |  +-----------------------------+
+----------------------+     +->| alert-relay (0.0.0.0:9099)  |
        ^                      |  POST /alert -> stdout+queue|
        |                      |  GET /alerts  -> drain      |
        |                      +--------------+--------------+
        | kubectl port-forwards               |
        | (localhost + 172.18.0.1)            | policy: local-observability
        |                                     v
        |        NemoClaw / OpenShell sandbox (oncall)
        |        +---------------------------------------------+
        +--------| OpenClaw cron: alert-intake (every 30s)     |
        Prom/Loki|   bin/investigate.sh (command payload)      |
        via      |     1. drain relay /alerts (first firing)   |
        172.18.0.1|    2. promql-query + logql-query evidence  |
                 |     3. Tavily search (error string)         |
                 |     4. Nemotron 3 Ultra RCA (inference.local)|
                 |     5. write memory/incidents/<date>-<n>.md |
                 |     6. print brief -> cron announce         |
                 +------------------+--------------------------+
                                    |
                                    v
                          Telegram (@gengwg_oncall_bot)
                          (the page, deliveryStatus: delivered)
```

## Real on-call path (since v1.4.0)

```
IRM alert groups -> irm-poll.sh (timer, restricted user, read-only token)
  seen/storm/repeat filters -> investigate.sh -> brief, incident report, DC draft file
  -> $OUTBOX_DIR/<irm_id>.json  (tmp+rename; skipped groups emit nothing)

slack-agent (owner's user, Socket Mode)
  scan outbox every 10s -> per-event record {slack_ts, acked_at, dc_status}
  -> PostDM(brief)  -> ack gate: my shift (IRM final_shifts, fail closed),
                       alert fired in my shift, verdict hold, group still firing
  -> Acknowledge via a transport that allows only /acknowledge and /unacknowledge
  -> dc_draft set & node not in ledger (14d) -> claude -p /file-dc-request, draft only
  <- owner DM commands, matched to records by thread ts or by #number / card link
```

Each record stage is saved before the next side effect, so a restart repeats
nothing. A dead agent, a failed DM or a stale shift table all mean "not acked",
which is what IRM did before the agent existed.

## Key implementation decisions

- **Command payload, not agent turn.** This OpenClaw build (2026.7.1) does not
  reliably execute tools in unattended isolated/custom agent-turn jobs with the
  reasoning Ultra model (it narrates instead of calling exec). A deterministic
  command payload (investigate.sh) runs the real queries and calls Nemotron
  directly via the sandbox's managed inference route. More reliable, and the
  Nemotron analysis is the LLM value-add where it matters.
- **inference.local, not direct Token Factory egress.** The sandbox policy
  blocks api.tokenfactory.nebius.com; the managed inference route
  (https://inference.local/v1) is the sanctioned path (COMPATIBLE_API_KEY).
- **Command payload, single writer.** The cron runs `bin/investigate.sh`
  every 30s; a `flock` serializes runs so a slow Nemotron call can't overlap
  the next tick. (An earlier condition-trigger design used code-mode scripts;
  see docs/feedback.md for why that path was dropped.)
- **Custom policy preset** `local-observability` opens read-only Prom/Loki on
  the docker bridge gateway (172.18.0.1) with a private-host trust exemption.

## Verified end to end

All three chaos scenarios (error, latency, memory) pass `tests/e2e/run.sh`:
chaos -> Prometheus alert -> Alertmanager -> relay -> cron drain -> PromQL/
LogQL evidence -> Tavily web context -> Nemotron 3 Ultra RCA -> incident
report -> Telegram page. Reports correctly distinguish the injected failure
from resource exhaustion.
