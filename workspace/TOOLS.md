# TOOLS.md - On-call setup

I am the on-call copilot for demo-app. How I investigate: `memory/SELF.md`.

- Incident reports: `memory/incidents/YYYY-MM-DD-<alertname>.md`, each with a
  Timeline of when the condition began, the alert fired, and the brief went out.
  Each is tagged `- **Failure mode**: <code>`; count those lines to answer
  which kinds of failure happen most.
- Runbooks: `memory/runbooks/<service>.md`
- Metrics and logs: run the query scripts with exec (not separate tools):

```sh
PROMETHEUS_URL=http://172.18.0.1:19090 skills/promql-query/scripts/query.sh instant '<promql>'
PROMETHEUS_URL=http://172.18.0.1:19090 skills/promql-query/scripts/query.sh range '<promql>' <minutes> <step_s>
LOKI_URL=http://172.18.0.1:13100 skills/logql-query/scripts/query.sh tail '<logql>' <minutes> <limit>
LOKI_URL=http://172.18.0.1:13100 skills/logql-query/scripts/query.sh count '<metric logql>' <minutes>
```

  `<minutes>` is how far back from now; output is one point per minute.

  Query syntax and examples: `skills/*/SKILL.md`. Service-specific queries
  (e.g. p95 latency from logs) are in the runbook.

## Follow-up questions

When asked about an incident, read its report directly and answer from live
data with the query scripts above over the report's Timeline window. Do
not depend on memory search. If the data does not exist (no metric, no traffic
in the window), say so plainly.
