# OnCall Copilot

An on-call copilot for a Grafana IRM rotation. For each new alert it gathers
the evidence first (the alerting node's conditions and events through a
read-only cluster identity, metrics and logs through Grafana), asks a model for
the likely cause, and writes a brief with two calls: **page or hold** (does
someone need to act before morning) and **real or false alarm**. The owner gets
the brief as a Slack DM, and a real hardware fault also gets a pre-filled
request to the datacenter provider. IRM acks are narrow and start in dry run.

It began as a submission to the Nebius x NVIDIA Global AI Hackathon (Personal
AI track). That demo is retired and kept below for reference.

## Real on-call mode

Two services on one always-on host:

```
Grafana IRM alert groups --poll (read-only SA token)--> alert-relay/poll.sh (IRM adapter)
   (restricted system user, systemd timer)   -> investigate.sh -> brief + DC draft
                                              -> one JSON event per brief in an outbox
                                                        |
                                                        v  (group-readable)
   slack-agent/ (Go, the owner's user, systemd --user, Socket Mode)
     -> on the owner's shift: DM the brief, reply in-thread with the ack decision
     -> on other shifts: hold it; one digest DM per day at 08:05 local
     -> ack in IRM only on the owner's own shift, only for a `hold` verdict,
        only after the DM posted; everything else keeps escalating
     -> real hardware faults: run the /file-dc-request skill headless (draft only)
     -> owner replies: file, sent, ignore, ack, unack, status; or `file #1234` / `file <card link>`
     -> pings an IRM heartbeat after each successful scan (optional)
```

The poller pings its own IRM heartbeat after each successful poll and holds it
after two degraded briefs in a row (model call failed, or node evidence not
collected), so a dead or useless service shows up without anyone checking.

Every credential the deployment uses (the Grafana service account, the cluster
tokens, the IRM token, the Slack app, the model key) belongs to the
organization running the rotation and lives outside the repo, in
`~/.config/oncall-copilot/` and the service users' env files. The repo's
`.env` belongs to the retired demo.

What is live, what was decided and what is still open:
`docs/real-oncall-status.md`. Why it is shaped this way, and why acks are so
narrow: `docs/real-oncall-research.md`. Deploy steps and the Slack app
settings: `deploy/systemd/README.md`. The agent starts in dry run
(`ACK_DRY_RUN=1`).

## Layout

- `alert-relay/` — `poll.sh` and `pagers/irm.sh` (poll IRM; the real mode) and `investigate.sh`
  (evidence, model call, brief, report, DC draft; both modes). Node evidence comes
  from per-cluster read-only kubeconfigs in `KUBECONFIG_DIR`; the Prometheus
  scenario queries still read the demo-app's `demo_*` series. The Go relay is the
  demo's Alertmanager webhook → queue
- `slack-agent/` — the real-on-call Slack side (Go): reads the poller's outbox,
  DMs each brief to one person, lets them steer by replying (`file`, `sent`, `ignore`,
  `ack`, `unack`, `status`, or `file #5474` / `file <alert link>` for any alert), acks `hold`-verdict pages during that person's own
  IRM shift once the brief has reached them (a `page` verdict keeps escalating), and drafts DC hardware requests through the
  metarepo `/file-dc-request` skill (`deploy/systemd/user/`, `deploy/systemd/install-slack-agent.sh`)
- `deploy/systemd/` — units and installers for the real mode
- `skills/` — OpenClaw skills: `promql-query`, `logql-query`; `investigate.sh` runs their query scripts
- `memory/` — agent knowledge layer (runbooks, incident reports) (demo)
- `workspace/TOOLS.md` — on-call notes OpenClaw loads into every chat session (demo)
- `demo-app/` — Go service with a `/chaos` endpoint (latency, errors, memory, CPU) (demo)
- `deploy/minikube/` — observability stack (Prometheus, Alertmanager, Loki, Grafana, Alloy) (demo)
- `deploy/dashboard/` — incident dashboard; `-render` writes it as static pages for GitHub Pages
  (https://gengwg.github.io/oncall-copilot/). `deploy/sync-incidents.sh` pushes each new report to the repo, which republishes the feed (demo)
- `deploy/policy-local-observability.yaml` — sandbox network policy for Prom/Loki (demo).
  Apply with `nemoclaw <sb> policy add --from-file deploy/policy-local-observability.yaml
  --trusted-private-host 172.18.0.1 --yes`; NemoClaw v0.0.123+ refuses RFC1918
  endpoints without that explicit trust flag
- `tests/e2e/` — chaos-scenario end-to-end harness (demo)
- `spike/` — Phase 0 verification (Token Factory, NemoClaw onboarding) (demo)
- `docs/` — real-mode status and research, architecture, feedback, plan

## Testing

```bash
# unit + race, one Go module per directory (what CI runs)
for d in alert-relay demo-app deploy/dashboard slack-agent; do
  (cd $d && test -z "$(gofmt -l .)" && go vet ./... && go test -race ./...)
done

# investigator against a real relay + stub Prometheus/Loki/inference
./tests/investigate_test.sh
# evidence query scripts and the IRM poller, against stubs
./tests/query_scripts_test.sh
./tests/irm_poll_test.sh

# end-to-end on the demo stack: chaos -> alert -> relay -> evidence -> Nemotron RCA -> report
RELAY_LOG=/tmp/opencode/relay-host.out COPILOT_ASSERT=1 ./tests/e2e/run.sh
# scenarios: error, latency, memory (all assert the incident report content)
```

## Hackathon demo (retired)

Built for the **Nebius x NVIDIA Global AI Hackathon** — **Personal AI track**.
The demo copilot watches your alerts, investigates **before** paging you, and
sends a Telegram brief with the likely root cause, the evidence, and a
suggested first action from your own runbooks.

Instead of waking you with a raw alert, the copilot does the first five minutes
of incident response for you: confirms the alert against live metrics, reads
the error logs, cross-references your runbook and the web, and reasons about
root cause — then pages you with a digested brief and files an incident report
it remembers for next time. When you open the page, the root cause is already
there, and the brief says how long after the failure began it was ready (about
a minute in the demo).

### Why it's Personal AI

- **Always-on**: a cron automation watches the alert stream every 30s.
- **Private & sandboxed**: runs inside an NVIDIA OpenShell sandbox with a
  deny-by-default network policy. Only Token Factory, Telegram, Tavily, and
  your own Prometheus/Loki are reachable. Your telemetry never leaves your
  infra except through the inference route you control.
- **Persistent memory**: runbooks and incident reports live in a plain-markdown
  knowledge layer (`memory/`) that the agent reads and updates.
- **Reusable skills**: `promql-query` and `logql-query` skills work against any
  Prometheus/Loki.

### Stack (how the required pieces are used)

| Piece | Role |
|---|---|
| **NVIDIA NemoClaw + OpenShell** | sandboxed agent runtime, network policy, secrets |
| **OpenClaw** | always-on agent: cron automations, Telegram channel, skills, memory |
| **NVIDIA Nemotron 3 Ultra** (Token Factory) | root-cause reasoning over collected evidence |
| **Nebius Token Factory** | OpenAI-compatible inference endpoint (`custom` provider in NemoClaw) |
| **Tavily** | web search for unfamiliar error strings during investigation |
| **GitHub Pages** | hosts the public incident feed (demo URL): the dashboard rendered as static pages, republished on every new report |
| Prometheus + Alertmanager + Loki + Grafana + Alloy | local demo observability stack (minikube) |

### Data flow

```
demo-app (/chaos) -> Prometheus alert -> Alertmanager --webhook--> alert-relay
                                                              (host, :9099)
                                                                  |
                    sandbox (NemoClaw/OpenShell)                  | drain
  +-----------------------------------------------------------------------+
  | OpenClaw cron: alert-intake (every 30s) -> bin/investigate.sh         |
  |   1. promql-query + logql-query  -> live metrics + error logs         |
  |   2. Tavily search               -> web context for the error string  |
  |   3. Nemotron 3 Ultra (inference.local) -> root-cause analysis        |
  |   4. write memory/incidents/<date>-<alert>.md  (persistent memory)    |
  |   5. print brief -> cron announce -> Telegram page                    |
  +-----------------------------------------------------------------------+
                                                                  |
                                                            incident dashboard
                                                            (Nebius Serverless)
```

### Quick start

Prereqs: Docker, minikube, kubectl, helm, Go, a Nebius Token Factory API key,
a Tavily API key, and a Telegram bot token + chat id.

```bash
# 1. credentials
cp .env.example .env   # fill in NEBIUS_API_KEY, TAVILY_API_KEY,
                       #     TELEGRAM_BOT_TOKEN, TELEGRAM_CHAT_ID

# 2. local observability stack + demo-app
./deploy/minikube/up.sh

# 3. host services: port-forwards, alert-relay, incident sync, dashboard
#    Idempotent — re-run it any time; it leaves healthy services alone and
#    restarts an alert-relay running a stale token.
./deploy/demo-services.sh start

# 4. NemoClaw sandbox on Token Factory (Nemotron 3 Ultra)
./spike/01-onboard.sh oncall

# 5. deploy skills + memory + investigator into the sandbox
./deploy/deploy-to-sandbox.sh oncall

# 6. wire the alert-intake cron (investigates + pages on new alerts)
bash deploy/setup-automations.sh   # reads .env itself
```

Trigger a failure and watch the copilot work:

```bash
curl "localhost:18080/chaos?mode=error"   # or latency | memory | cpu
# ... within ~1-2 min: a Telegram brief with root cause + evidence,
# and a report in memory/incidents/.
curl "localhost:18080/chaos/stop"
```

`./deploy/demo-services.sh status` shows the state of every host service;
`stop` tears them down. Start it with an SSH agent that can push to the repo
in the environment, or the incident sync cannot publish new reports.

### How Token Factory / Nemotron accelerated the build

- Token Factory's OpenAI-compatible endpoint dropped straight into NemoClaw's
  `custom` provider — no model-serving work.
- Nemotron 3 Ultra's reasoning produces root-cause analyses that correctly
  distinguish an injected failure from resource exhaustion (see
  `deploy/dashboard/incidents/` for real reports it filed).
- The sandbox's managed inference route (`inference.local`) keeps the analysis
  call inside the policy boundary.

See `docs/feedback.md` for detailed platform feedback (also a hackathon
deliverable).

## License

Apache 2.0 — see `LICENSE`.
