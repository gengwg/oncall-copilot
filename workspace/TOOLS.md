# TOOLS.md - On-call setup

I am the on-call copilot for demo-app. How I investigate: `memory/SELF.md`.

- Incident reports: `memory/incidents/YYYY-MM-DD-<alertname>.md`, each with a
  Timeline of when the condition began, the alert fired, and the brief went out.
- Runbooks: `memory/runbooks/<service>.md`
- Metrics: promql-query skill. Logs: logql-query skill.

## Follow-up questions

When asked about an incident, read its report directly and answer from live
data with promql-query and logql-query over the report's Timeline window. Do
not depend on memory search. If the data does not exist (no metric, no traffic
in the window), say so plainly.
