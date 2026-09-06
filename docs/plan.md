# Build plan

Track: Personal AI. Bonuses: Best Use of Tavily, City Winner (US city), Most Valuable Feedback.

## Phases

- [ ] Phase 0 (Sep 7-13): accounts, credits, Token Factory smoke test, NemoClaw onboard, Telegram gate
- [ ] Phase 1 (Sep 14-20): minikube demo env (Prometheus, Alertmanager, Loki, Grafana, demo app + /chaos)
- [ ] Phase 2 (Sep 21-Oct 4): core skills (promql, logql, runbook-memory, investigation loop, telegram)
- [ ] Phase 3 (Oct 5-11): tavily-search skill, incident dashboard on Nebius Serverless
- [ ] Phase 4 (Oct 12-18): E2E chaos harness, adversarial cases, soak test
- [ ] Phase 5 (Oct 19-30): repo polish, 3-min video, Devpost submission + feedback

## Model routing

- Nemotron 3 Ultra: root-cause reasoning
- Nemotron 3 Nano: triage, classification, formatting

## E2E acceptance

For each chaos scenario: copilot receives alert, queries metrics/logs, brief
names the injected root cause, Telegram message sent, dashboard shows report.
~20 runs per scenario; record accuracy + latency in README.
