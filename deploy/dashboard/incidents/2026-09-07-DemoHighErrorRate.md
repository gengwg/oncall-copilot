# DemoHighErrorRate — 2026-09-07

- **Service**: demo-app
- **Severity**: critical
- **Status**: firing
- **Summary**: demo-app error rate above 10%

## Root cause
The demo-app is experiencing a 46.8% error rate (error_ratio_5m: 0.4679) with all recent errors showing "upstream dial timeout" (502), indicating the upstream dependency is unreachable or timing out on connection establishment. Heap allocation (743,776 bytes) and latency_mode (0) are normal, confirming the issue is external connectivity rather than resource exhaustion or chaos injection.

## Evidence
- up: 1
- error_ratio_5m: 0.4679487179487179
- heap_alloc_bytes: 743776
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
Check the upstream service health and network connectivity from demo-app pods to the upstream endpoint (verify DNS resolution, firewall rules, and upstream pod readiness).

## Full Nemotron analysis
ROOT CAUSE: The demo-app is experiencing a 46.8% error rate (error_ratio_5m: 0.4679) with all recent errors showing "upstream dial timeout" (502), indicating the upstream dependency is unreachable or timing out on connection establishment. Heap allocation (743,776 bytes) and latency_mode (0) are normal, confirming the issue is external connectivity rather than resource exhaustion or chaos injection.

ACTION: Check the upstream service health and network connectivity from demo-app pods to the upstream endpoint (verify DNS resolution, firewall rules, and upstream pod readiness).
