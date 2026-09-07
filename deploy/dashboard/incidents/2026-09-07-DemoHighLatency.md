# DemoHighLatency — 2026-09-07

- **Service**: demo-app
- **Severity**: warning
- **Status**: firing
- **Summary**: demo-app latency degradation

## Root cause
The demo-app's 47.3% error ratio (error_ratio_5m=0.473) and latency degradation are caused by upstream dial timeouts (status 502)

## Evidence
- up: 1
- error_ratio_5m: 0.47328244274809156
- heap_alloc_bytes: 227880
- latency_mode: 0
- recent ERROR logs:
```
2026-09-07T08:26:25Z	{"time":"2026-09-07T08:26:25.593431818Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-09-07T08:26:24Z	{"time":"2026-09-07T08:26:24.887158845Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-09-07T08:26:24Z	{"time":"2026-09-07T08:26:24.20186129Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
```

## Web context
- What Does "Upstream Request Timeout" Mean and How to Fix It?: An upstream request timeout is an error that occurs when a server (like a reverse proxy or load balancer) that is acting on behalf of a client is waiting for a response from an ups
- upstream request timeout in databricks apps when u... - Databricks Community - 110253: Hi, i am building an application in Databricks apps, Sometimes when i try to fetch data using Databricks SQL connector in an API, it takes time to hit the SQL warehouse and if the 

## Suggested first action
See runbook.

## Full Nemotron analysis
ROOT CAUSE: The demo-app's 47.3% error ratio (error_ratio_5m=0.473) and latency degradation are caused by upstream dial timeouts (status 502)
