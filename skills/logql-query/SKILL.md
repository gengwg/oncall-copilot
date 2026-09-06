---
name: logql-query
description: Search application logs in Loki with LogQL to find error messages and patterns during incident investigation.
version: 0.1.0
metadata:
  openclaw:
    requires:
      env:
        - LOKI_URL
      bins:
        - curl
        - jq
    primaryEnv: LOKI_URL
    envVars:
      - name: LOKI_URL
        required: true
        description: Base URL of the Loki server, e.g. http://loki.observability.svc:3100
---

# logql-query

Search logs in Loki at `$LOKI_URL` using `scripts/query.sh`. Read-only.

## When to use

During alert investigation: find the actual error lines behind a metric spike.
Metrics tell you *that* something is wrong; logs tell you *what*.

## Usage

Recent lines for a service (last N minutes, limit):

```bash
scripts/query.sh tail '{service="demo-app"}' 30 50
```

Only errors:

```bash
scripts/query.sh tail '{service="demo-app"} |= "ERROR"' 30 50
```

Pattern match:

```bash
scripts/query.sh tail '{service="demo-app"} |~ "dial|timeout"' 30 50
```

Error count over time (metric query):

```bash
scripts/query.sh count 'sum(count_over_time({service="demo-app"} |= "ERROR" [1m]))' 30
```

## Investigation patterns

- Start broad (`{service="X"}`), then narrow with `|=` filters.
- Correlate with the alert's `starts_at`: query the window around it.
- Quote 1-3 representative log lines verbatim in the incident report as
  evidence, with timestamps.
