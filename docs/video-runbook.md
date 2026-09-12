# Video run-book — record in one take (~2.5 min)

Prep: terminal left, Telegram app right. Narration cues in [brackets].

## Scene 0 — before you hit record

```bash
cd ~/projects/nebius-hackathon
bash deploy/demo-services.sh start        # everything up + public tunnel
bash deploy/demo-services.sh status       # copy this tunnel URL to browser
```
Open the tunnel URL in a browser tab (the public incident feed).

## Scene 1 — hook (0:00–0:25)

[Read this:] "On-call is brutal. A bare alert at 3 AM, and the first five
minutes is dashboards, logs, and a runbook hunt. I built an AI on-call copilot
that investigates before it pages me — on open infrastructure, Nebius + NVIDIA
open models."

## Scene 2 — architecture (0:25–0:55)

Open `docs/architecture.md` (rendered). Point at: minikube stack → relay →
sandbox (OpenShell policy) → cron → skills → Nemotron → Telegram → dashboard.

[Read this:] "Sandboxed agent, deny-by-default policy, only Token Factory,
Telegram, Tavily, and my own Prometheus/Loki reachable."

## Scene 3 — live demo (0:55–2:00) — paste commands as you talk

```bash
# 1. all three scenarios, live
./tests/e2e/run.sh
```
[While it runs:] "Error, latency, memory — each fires, the copilot pulls real
metrics and logs, searches the web with Tavily, and Nemotron 3 Ultra writes
the root cause."

Telegram pings as briefs arrive. Show the incident-viewer URL updating.

## Scene 4 — close (2:00–2:30)

[Read this:] "Tested end to end — three scenarios, verified root-cause
reports. Open source, sandboxed, private. On-call shouldn't mean waking up to
a mystery."

## After the take

- `bash deploy/demo-services.sh stop` (kills the tunnel too)
- OBS: check audio levels (narration + light keyboard clicks, no music)
- Trim to ≤ 3:00, export 1080p, upload as PUBLIC YouTube
