# Devpost submission checklist

Deadline: **Oct 30, 2026 @ 10:00am PDT**. URL:
https://nebiusglobalaihackathon.devpost.com/ ("Enter a Submission")

Fill each field with the values below.

---

## 1. Project name

```
OnCall Copilot — a private, always-on AI on-call copilot
```

## 2. Elevator pitch / short description (max ~150 chars)

```
Investigates alerts BEFORE paging you: Nemotron 3 Ultra on Nebius Token
Factory reads live metrics+logs, reasons about root cause, and pages with
evidence and a first action. Sandboxed, private, open source.
```

## 3. The story / full description

```markdown
## What it does

On-call is brutal. A bare alert at 3 AM means five minutes of dashboards,
logs, and runbook hunting before you even know what's wrong. OnCall Copilot
inverts that: it investigates the alert before it pages you.

When an alert fires, the copilot:
1. Pulls the real metric from Prometheus and the error lines from Loki.
2. Searches the web for the error string with Tavily.
3. Asks NVIDIA Nemotron 3 Ultra (on Nebius Token Factory) to reason about
   the root cause against live evidence and your runbook.
4. Pages you on Telegram with the likely cause, the evidence, and a
   suggested first action.
5. Writes an incident report to persistent memory (markdown), so the next
   incident inherits the history.

## Why it's Personal AI

- Always-on: a cron automation watches the alert stream every 30s.
- Private & sandboxed: runs inside an NVIDIA OpenShell sandbox (NemoClaw)
  with deny-by-default network policy. Only Token Factory, Telegram, Tavily,
  and your own Prometheus/Loki are reachable.
- Persistent memory: runbooks and incident reports in plain markdown.
- Reusable skills: promql-query and logql-query against any Prom/Loki.

## How Nebius + NVIDIA were used

- NVIDIA Nemotron 3 Ultra (open model) served on **Nebius Token Factory** —
  the root-cause reasoning.
- **NVIDIA NemoClaw + OpenShell** — the sandboxed, policy-controlled agent
  runtime.
- **Tavily** — web search for unfamiliar error strings during investigation.
- Prometheus + Alertmanager + Loki + Grafana (minikube) as the demo stack.

## Testing

End-to-end chaos harness (`tests/e2e/run.sh`) injects failure modes
(error, latency, memory) and asserts the copilot produces a root-cause
incident report for each. 3/3 scenarios pass on a clean soak.
```

## 4. Track (required category)

```
Personal AI Track
```

## 5. Public code repository URL (required)

```
https://github.com/gengwg/oncall-copilot
```

> Action: make the repo public BEFORE submitting (see step A below).
> Apache-2.0 LICENSE is at the repo root. README has setup + how Nebius/
> Nemotron/Tavily were used.

## 6. Working demo URL (required, non-Physical tracks)

```
https://explorer-music-pierre-clerk.trycloudflare.com
```

> Note: this is a Cloudflare quick-tunnel. For judging, start services so the
> URL stays alive: `bash deploy/demo-services.sh start`, then print the current
> URL with `bash deploy/demo-services.sh status`. If it changed, update this
> field. The demo shows the live incident feed produced by the copilot.

## 7. Demonstration video URL (required, <= 3 min, public YouTube)

```
<your public YouTube URL for /tmp/opencode/video/final.mp4>
```

> Video: 78s, h264+AAC, TTS narration naming Token Factory + Nemotron +
> NemoClaw/OpenShell + Tavily. Upload as PUBLIC (not unlisted/private).

## 8. City (Builders & Brews City Winner Award)

```
<pick your nearest: NYC / Boston / SF / LA>
```

## 9. Feedback on Nebius Token Factory, AI Cloud, NVIDIA tools (bonus: Most Valuable Feedback)

Paste the full contents of `docs/feedback.md` — 8 documented, reproducible
findings (NemoClaw dashboard port reallocation deadlock, Tavily plugin build
failure, reasoning-model null content on small max_tokens, unattended
agent-turn tool-execution bug, code-mode API drift, secrets-in-job-spec, docs
version drift, Ubuntu 26.04 validation gap).

## 10. Significant updates during the submission period (pre-existing project?)

```
N/A — newly created during the submission period (Aug 26 – Oct 30, 2026).
```

---

## Step A — make the repo public (do this first)

```bash
cd /home/gengwg/projects/nebius-hackathon
git remote add origin git@github.com:gengwg/oncall-copilot.git 2>/dev/null || true
gh repo create gengwg/oncall-copilot --public --source=. --push 2>/dev/null || git push -u origin main
# verify: gh repo view gengwg/oncall-copilot --json visibility
```

If the repo already exists on GitHub as private, flip it:
```bash
gh repo edit gengwg/oncall-copilot --visibility public --accept-visibility-change-consequences
```

## Step B — before submitting, confirm services stay up for judging

The rules require the project to be available for judging/testing until the
judging period ends (~Dec 15). Two options:

1. **Keep this machine on** with `bash deploy/demo-services.sh start` and the
   Cloudflare tunnel alive. Re-print the URL (`demo-services.sh status`) and
   keep the Devpost field in sync if it changes.
2. **More durable:** deploy the dashboard to Nebius Serverless Endpoints once
   the AI Cloud billing unlocks (`deploy/dashboard/deploy.sh`), then replace
   the Devpost demo URL with the managed `https://...` endpoint URL.

## Step C — submit early

The submission period closes **Oct 30 @ 10:00am PDT**. Submit 1-2 days early
to leave buffer; you can update the Devpost project page after submitting.

---

## Prize-eligibility summary

- Overall awards (Grand/2nd/3rd) — judged on the 4 criteria.
- **Personal AI Track Winner** — the project is on-track for this.
- **Best Use of Tavily** — Tavily is a runtime call inside the investigation
  loop (verified in incident reports).
- **City Winner ($500)** — pick your city in field 8.
- **Most Valuable Feedback** — `docs/feedback.md` (8 findings) is a strong
  candidate.

One project can win one Overall/Track award + one Bonus award.
