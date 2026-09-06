---
name: promql-query
description: Run PromQL queries against Prometheus/Mimir to investigate alerts — check current values, rates, and history for metrics.
version: 0.1.0
metadata:
  openclaw:
    requires:
      env:
        - PROMETHEUS_URL
      bins:
        - curl
        - jq
    primaryEnv: PROMETHEUS_URL
    envVars:
      - name: PROMETHEUS_URL
        required: true
        description: Base URL of the Prometheus server, e.g. http://prometheus.observability.svc:9090
---

# promql-query

Run PromQL against the Prometheus server at `$PROMETHEUS_URL` using
`scripts/query.sh`. All queries are read-only HTTP GETs.

## When to use

During alert investigation: verify a hypothesis by checking the actual metric
values (error rates, latency, saturation) before naming a root cause.

## Usage

Instant query:

```bash
scripts/query.sh instant 'sum(rate(demo_errors_total[5m]))'
```

Range query (last N minutes, step seconds):

```bash
scripts/query.sh range 'sum(rate(demo_errors_total[1m]))' 30 15
```

List label values (e.g. which services exist):

```bash
scripts/query.sh labels service
```

## Investigation patterns

- Error rate ratio: `sum(rate(demo_errors_total[5m])) / sum(rate(demo_requests_total[5m]))`
- Per-instance breakdown: `sum by (instance) (rate(demo_errors_total[5m]))`
- Memory growth: `demo_heap_alloc_bytes` as a range query to see a leak slope
- Confirm an alert is still firing: query the alert's own expression

Always verify with real data before concluding. Quote the numbers you saw in
your incident report.
