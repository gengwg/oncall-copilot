# Oncall Copilot

A private, always-on AI on-call copilot. It watches your alerts, investigates
before you wake up, and pages you on Telegram with a pre-digested incident
brief: what fired, likely root cause, evidence, and suggested first actions
from your own runbooks.

Built for the Nebius x NVIDIA Global AI Hackathon (Personal AI track).

## Stack

- **Agent runtime:** NVIDIA NemoClaw + OpenShell (sandboxed, policy-controlled)
- **Agent:** OpenClaw (always-on assistant)
- **Models:** NVIDIA Nemotron 3 via Nebius Token Factory
  - Ultra for root-cause reasoning, Nano for triage/classification
- **Search:** Tavily (error messages, CVEs, docs lookup during investigation)
- **Telemetry:** Prometheus + Alertmanager + Loki + Grafana (local demo env)
- **Dashboard:** Nebius Serverless Endpoints (public incident feed)

## Layout

- `skills/` — OpenClaw skills (promql-query, logql-query)
- `alert-relay/` — Alertmanager webhook -> stdout bridge (stream automation)
- `memory/` — agent knowledge layer (runbooks, incident reports)
- `demo-app/` — Go service with a `/chaos` endpoint for failure injection
- `deploy/minikube/` — local observability stack manifests
- `deploy/dashboard/` — incident dashboard (Nebius Serverless)
- `tests/e2e/` — chaos-scenario end-to-end test harness
- `spike/` — Phase 0 verification scripts
- `docs/` — architecture, video script, submission notes

Native OpenClaw features used: Telegram channel (paging), Tavily web search
(wired at NemoClaw onboarding), stream automations (alert intake), workspace
memory (runbooks/incidents).

## Status

Phase 0: environment bring-up. See `docs/plan.md`.
