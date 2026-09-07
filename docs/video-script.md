# Demo video script (< 3 min, with audio narration)

Target: show the copilot working live, name the Nebius/NVIDIA pieces, tell the
"investigate before paging" story. Record in one take where possible.

---

## 0:00–0:25 — The problem (talking + a phone screenshot of a raw alert)

"On-call is brutal. You get paged at 3 AM with a bare alert — 'error rate above
10%' — and the first five minutes is always the same scramble: open dashboards,
read logs, dig up the runbook. I built an AI on-call copilot that does that
first investigation *before* it pages me, so I wake up to the answer, not the
alarm. It runs entirely on open infrastructure — Nebius and NVIDIA open models —
and my telemetry never leaves my control."

## 0:25–0:55 — The setup (screen: architecture diagram from docs/architecture.md)

"Here's the system. A demo service on Kubernetes with Prometheus, Alertmanager,
Loki, and Grafana. When an alert fires, it goes to the copilot, which runs
inside an NVIDIA OpenShell sandbox via NemoClaw — deny-by-default network
policy, so it can only reach my Prometheus, Loki, Telegram, Tavily, and the
Nebius Token Factory inference route. The reasoning is done by NVIDIA Nemotron
3 Ultra, an open model, served on Nebius Token Factory."

## 0:55–1:55 — Live demo (screen: terminal + Telegram side by side)

"I inject a failure — error chaos on the service." [run `curl .../chaos?mode=error`]

"Within about a minute the alert fires. Watch what the copilot does instead of
just forwarding it: it pulls the real error rate from Prometheus, reads the
actual error lines from Loki, searches the web for the error string with
Tavily, then asks Nemotron 3 Ultra to reason about root cause."

[show the Telegram brief arriving on the phone]

"And here's the page — not 'something is wrong', but: likely cause, the
evidence, and a suggested first action. It even ruled out resource exhaustion."

[show the incident report file]

"It writes an incident report to its persistent memory, so next time this
happens it already knows the history."

## 1:55–2:35 — Why it matters + the stack callouts

"This is the Personal AI track: an always-on, private assistant with persistent
memory and real tool use, keeping my data under my control. Everything you saw
runs on Nebius Token Factory and NVIDIA open models — Nemotron 3 Ultra for
reasoning, NemoClaw and OpenShell for the sandboxed runtime, Tavily for web
grounding. The whole thing is open source."

## 2:35–2:50 — Close

"It's tested end to end — three failure scenarios, each producing a verified
root-cause report. On-call doesn't have to mean waking up to a mystery. The
copilot does the first investigation so I can go straight to the fix."

---

## Shot list / b-roll

- [ ] terminal: `curl .../chaos?mode=error`
- [ ] terminal: `tests/e2e/run.sh` passing 3/3
- [ ] Telegram brief on phone (the page)
- [ ] incident report markdown with Nemotron analysis
- [ ] Grafana dashboard of the error spike
- [ ] architecture diagram (docs/architecture.md)

## Rules checklist

- [ ] <= 3 minutes
- [ ] audio narration naming Nebius Token Factory + Nemotron
- [ ] shows the project functioning
- [ ] public YouTube link
- [ ] no third-party trademarks/copyrighted music without permission
