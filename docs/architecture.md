# Architecture

## Overview

```
minikube (demo env, local)                     NemoClaw/OpenShell sandbox (host)
+----------------------+                       +--------------------------------------+
| demo-app (/chaos)    |                       | OpenClaw gateway (always-on)         |
| Prometheus           |  scrape               |                                      |
| Alertmanager --webhook--+---> alert-relay ---+--> stream automation fires agent turn|
| Loki                 |       (stdout lines)  |   1. triage (Nemotron 3 Nano)        |
| Grafana              |                       |   2. investigate (Nemotron 3 Ultra): |
+----------------------+                       |      - promql-query skill -> Prom    |
                                               |      - logql-query skill -> Loki     |
                                               |      - tavily web search (native)    |
                                               |      - runbook-memory (workspace md) |
                                               |   3. write incident report           |
                                               |   4. deliver brief                   |
                                               +--------+------------------+----------+
                                                        |                  |
                                           Telegram channel (native)       |
                                                        |                  |
                                                        v                  v
                                                  user's phone      incident dashboard
                                                  (the "page")      (Nebius Serverless,
                                                                     public demo URL)
```

## Component choices (verified against docs)

| Need | Mechanism | Custom code? |
|---|---|---|
| Alert intake | OpenClaw automation `--stream-command` supervising `alert-relay` (webhook -> stdout JSON lines) | yes, small Go server |
| Metrics queries | `promql-query` skill (SKILL.md + curl/jq script) | yes |
| Log queries | `logql-query` skill (SKILL.md + curl/jq script) | yes |
| Persistent memory | OpenClaw workspace markdown + built-in memory; runbooks seeded in workspace | content only |
| Web search | Tavily, wired at NemoClaw onboarding (native support) | no |
| Paging | OpenClaw Telegram channel (native), `--announce --channel telegram` | no |
| Demo URL | incident dashboard web app on Nebius Serverless Endpoints | yes |
| Models | Token Factory: Ultra = investigation turns, Nano = triage jobs (`--model` per automation) | no |

## Investigation loop

1. Alertmanager fires -> alert-relay prints alert JSON line.
2. Stream automation batches the line, fires an isolated agent turn with the
   alert payload (model: Nano) for triage: severity, service, dedupe against
   `memory/incidents/`.
3. Triage turn escalates to an investigation turn (model: Ultra) with the
   runbook for the affected service. The agent runs promql-query/logql-query
   to test hypotheses, optionally tavily-search for unfamiliar errors.
4. Agent writes `memory/incidents/<date>-<alertname>.md` (report) and updates
   the runbook if something new was learned.
5. Brief delivered to Telegram: what fired, likely root cause, evidence
   (query results), suggested first actions, link to dashboard.
6. Dashboard reads the same incident store and renders the feed.

## Incident store

Single source of truth: `memory/incidents/*.md` in the agent workspace.
Dashboard reads them via a sync sidecar (workspace dir is host-mounted);
keeps the agent as the only writer.

## Security posture

- Agent runs inside OpenShell sandbox with NemoClaw network policy:
  allowlist = Token Factory, Tavily, Telegram, minikube services only.
- promql/logql skills are read-only HTTP GETs.
- alert-relay binds loopback + minikube network only, token-authenticated.
