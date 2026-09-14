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

Three layers. Go unit and race tests cover the chaos handlers and the relay
queue and auth. An integration suite runs the real relay binary against stub
Prometheus/Loki/inference and covers the failure modes that matter: token
drift, an unreachable relay, batched alerts, path traversal in an alert label,
and an unwritable lock. The end-to-end chaos harness (`tests/e2e/run.sh`)
injects error, latency, and memory failures against the live stack and asserts
a root-cause report for each. 3/3 scenarios pass; 36 tests in total.
```

## 4. Track (required category)

```
Personal AI Track
```

## 5. Public code repository URL (required)

```
https://github.com/gengwg/oncall-copilot
```

> **The repo is PRIVATE right now** (deliberately, until closer to submission).
> A required field, so flip it back before submitting — see Step A. Latest
> release is `v1.1.0`; release pages 404 for the public while private.
> Apache-2.0 LICENSE is at the repo root. README has setup + how Nebius/
> Nemotron/Tavily were used.

## 6. Working demo URL (required, non-Physical tracks)

```
https://thats-throat-vegetable-clip.trycloudflare.com
```

> Live as of 2026-09-13: returns HTTP 200 and lists real incident reports the
> copilot filed. Served by `deploy/dashboard` through a Cloudflare quick-tunnel
> started by `deploy/demo-services.sh start`.
>
> **Quick-tunnel URLs are not stable.** A new one is minted every time the
> tunnel restarts, so re-check this field before submitting and again before
> judging: `bash deploy/demo-services.sh status` prints the current URL. The
> tunnel also dies with the machine, and the rules require the project to stay
> reachable until judging ends (~Dec 15).
>
> The durable alternative, `deploy/dashboard/deploy.sh` to Nebius Serverless
> Endpoints, is **blocked on account permissions** as of 2026-09-13. Reads
> succeed (subnets, registries, endpoints all list) but creating a container
> registry in `project-u00nmrg7kc00748cm51t86` returns
> `PermissionDenied: Service registry error Auth`. The script is fine; it gets
> through project detection and fails on the first write. Once billing or the
> registry/AI-endpoint editor roles are enabled, re-run it and swap this field
> for the managed URL.

## 7. Demonstration video URL (required, <= 3 min, public YouTube)

```
https://www.youtube.com/watch?v=7L6E7PknOUk
```

> Verified public 2026-09-13 (oEmbed returns 200). Current YouTube title is
> "demo2" — worth renaming to the project name before judges see it.
>
> Video: `docs/media/demo.mp4`, 84s, 1920x1080 h264+AAC, piper TTS narration
> naming Token Factory + Nemotron + NemoClaw/OpenShell + Tavily. A real
> screencast of the live pipeline with Telegram Desktop on screen: chaos
> injected, alert fires, the copilot investigates, the brief lands. Upload as
> PUBLIC (not unlisted/private).

## 8. City (Builders & Brews City Winner Award)

```
San Francisco
```

## 9. Feedback on Nebius Token Factory, AI Cloud, NVIDIA tools (bonus: Most Valuable Feedback)

Paste the full contents of `docs/feedback.md` — 12 documented, reproducible
findings, observed on `nemoclaw` CLI v0.0.109 (the only version published to
npm; upstream was at tag v0.0.123, which the doc states up front).

The original eight (dashboard port reallocation deadlock, Tavily
plugin build failure, reasoning-model null content on small max_tokens,
unattended agent-turn tool-execution bug, code-mode API drift,
secrets-in-job-spec, docs version drift, Ubuntu 26.04 validation gap) plus four
from operating the system through a sandbox rebuild:

- `openshell forward start` reports failure on a forward that succeeded, and
  `nemoclaw recover` inherits the same probe — a loop that tells you to re-run
  the command that just worked, while each retry leaks a tunnel that eventually
  causes the real port collision it then blames.
- `rebuild` prints "rebuilt successfully", exits 1, and silently drops every
  cron job while restoring the workspace.
- The device scope upgrade a rebuild triggers can only be approved from inside
  the sandbox, and no host CLI, error message, or TUI mentions it. (This one is
  a confirmation of the open upstream issue NVIDIA/NemoClaw#10070, not a new
  finding; the other three have no upstream match.)
- A failed channel delivery surfaces only as a cron `error`, indistinguishable
  from the command failing.

## 10. Significant updates during the submission period (pre-existing project?)

```
N/A — newly created during the submission period (Aug 26 – Oct 30, 2026).
```

---

## Step A — make the repo public (REQUIRED: currently private)

The repo exists and is pushed; it is deliberately private until closer to
submission. One command flips it back:

```bash
gh repo edit gengwg/oncall-copilot --visibility public --accept-visibility-change-consequences
gh repo view gengwg/oncall-copilot --json visibility   # verify
```

Do this before submitting: a private repo fails the required "public code
repository" field, and the release pages 404 for judges while it is private.

## Step B — before submitting, confirm services stay up for judging

The rules require the project to be available for judging/testing until the
judging period ends (~Dec 15). Two options:

1. **Keep this machine on** with `bash deploy/demo-services.sh start` and the
   Cloudflare tunnel alive. Re-print the URL (`demo-services.sh status`) and
   keep the Devpost field in sync if it changes.
2. **More durable:** deploy the dashboard to Nebius Serverless Endpoints
   (`deploy/dashboard/deploy.sh`), then replace the Devpost demo URL with the
   managed `https://...` endpoint URL. Attempted 2026-09-13 and blocked:
   registry creation returns `PermissionDenied`, so the account needs billing
   or the registry/AI-endpoint editor roles first.

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
- **Most Valuable Feedback** — `docs/feedback.md` (12 findings) is a strong
  candidate.

One project can win one Overall/Track award + one Bonus award.
