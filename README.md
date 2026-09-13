# OnCall Copilot

A private, always-on AI on-call copilot. It watches your alerts, investigates
**before** paging you, and sends a Telegram brief with the likely root cause,
the evidence, and a suggested first action from your own runbooks.

Built for the **Nebius x NVIDIA Global AI Hackathon** — **Personal AI track**.

Instead of waking you with a raw alert, the copilot does the first five minutes
of incident response for you: confirms the alert against live metrics, reads
the error logs, cross-references your runbook and the web, and reasons about
root cause — then pages you with a digested brief and files an incident report
it remembers for next time.

## Why it's Personal AI

- **Always-on**: a cron automation watches the alert stream every 30s.
- **Private & sandboxed**: runs inside an NVIDIA OpenShell sandbox with a
  deny-by-default network policy. Only Token Factory, Telegram, Tavily, and
  your own Prometheus/Loki are reachable. Your telemetry never leaves your
  infra except through the inference route you control.
- **Persistent memory**: runbooks and incident reports live in a plain-markdown
  knowledge layer (`memory/`) that the agent reads and updates.
- **Reusable skills**: `promql-query` and `logql-query` skills work against any
  Prometheus/Loki.

## Stack (how the required pieces are used)

| Piece | Role |
|---|---|
| **NVIDIA NemoClaw + OpenShell** | sandboxed agent runtime, network policy, secrets |
| **OpenClaw** | always-on agent: cron automations, Telegram channel, skills, memory |
| **NVIDIA Nemotron 3 Ultra** (Token Factory) | root-cause reasoning over collected evidence |
| **Nebius Token Factory** | OpenAI-compatible inference endpoint (`custom` provider in NemoClaw) |
| **Tavily** | web search for unfamiliar error strings during investigation |
| **Nebius Serverless Endpoints** | hosts the public incident dashboard (demo URL) |
| Prometheus + Alertmanager + Loki + Grafana + Alloy | local demo observability stack (minikube) |

## Data flow

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

## Layout

- `demo-app/` — Go service with a `/chaos` endpoint (latency, errors, memory, CPU)
- `alert-relay/` — Alertmanager webhook → stdout + queue; `investigate.sh` (the loop)
- `skills/` — OpenClaw skills: `promql-query`, `logql-query`
- `memory/` — agent knowledge layer (runbooks, incident reports)
- `deploy/minikube/` — observability stack (Prometheus, Alertmanager, Loki, Grafana, Alloy)
- `deploy/dashboard/` — incident dashboard (Nebius Serverless)
- `deploy/policy-local-observability.yaml` — sandbox network policy for Prom/Loki
- `tests/e2e/` — chaos-scenario end-to-end harness
- `spike/` — Phase 0 verification (Token Factory, NemoClaw onboarding)
- `docs/` — architecture, feedback, plan

## Quick start

Prereqs: Docker, minikube, kubectl, helm, Go, a Nebius Token Factory API key,
a Tavily API key, and a Telegram bot token + chat id.

```bash
# 1. credentials
cp .env.example .env   # fill in NEBIUS_API_KEY, TAVILY_API_KEY,
                       #     TELEGRAM_BOT_TOKEN, TELEGRAM_CHAT_ID

# 2. local observability stack + demo-app
./deploy/minikube/up.sh

# 3. host port-forwards (Prometheus/Loki/Alertmanager/demo-app)
./deploy/minikube/forwards.sh start

# 4. alert-relay on the host (Alertmanager posts here)
(cd alert-relay && go build -o ../bin/alert-relay .)
./bin/alert-relay -addr 0.0.0.0:9099 \
  -token "$(kubectl -n observability get secret relay-token -o jsonpath='{.data.token}' | base64 -d)" &

# 5. NemoClaw sandbox on Token Factory (Nemotron 3 Ultra)
./spike/01-onboard.sh oncall

# 6. deploy skills + memory + investigator into the sandbox
./deploy/deploy-to-sandbox.sh oncall

# 7. wire the alert-intake cron (investigates + pages on new alerts)
bash deploy/setup-automations.sh   # reads .env itself
```

Trigger a failure and watch the copilot work:

```bash
kubectl -n demo port-forward svc/demo-app 18080:8080 &
curl "localhost:18080/chaos?mode=error"
# ... within ~1-2 min: a Telegram brief with root cause + evidence,
# and a report in memory/incidents/.
```

## Testing

```bash
# end-to-end: chaos -> alert -> relay -> evidence -> Nemotron RCA -> report
RELAY_LOG=/tmp/opencode/relay-host.out COPILOT_ASSERT=1 ./tests/e2e/run.sh
# scenarios: error, latency, memory (all assert the incident report content)
```

## How Token Factory / Nemotron accelerated the build

- Token Factory's OpenAI-compatible endpoint dropped straight into NemoClaw's
  `custom` provider — no model-serving work.
- Nemotron 3 Ultra's reasoning produces root-cause analyses that correctly
  distinguish an injected failure from resource exhaustion (see
  `memory/incidents/` for real examples).
- The sandbox's managed inference route (`inference.local`) keeps the analysis
  call inside the policy boundary.

See `docs/feedback.md` for detailed platform feedback (also a hackathon
deliverable).

## License

Apache 2.0 — see `LICENSE`.
