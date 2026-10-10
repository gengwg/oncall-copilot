# DemoHighErrorRate — 2026-09-07

- **Service**: demo-app
- **Severity**: critical
- **Status**: firing
- **Summary**: demo-app error rate above 10%

## Root cause
The demo-app is generating a 36.8% error rate (error_ratio_5m: 0.368) with repeated "upstream dial timeout" errors returning 502 status, indicating the upstream dependency is unreachable or exceeding the configured connection timeout. The heap allocation (686,528 bytes) and latency_mode (0) are normal, ruling out resource exhaustion or chaos injection.

## Evidence
- up: 1
- error_ratio_5m: 0.368
- heap_alloc_bytes: 686528
- heap_trend: n/a
- latency_mode: 0
- recent logs (5m window from alert start):
```
2026-09-07T23:50:48Z	{"time":"2026-09-07T23:50:48.885152544Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-09-07T23:50:48Z	{"time":"2026-09-07T23:50:48.175282724Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-09-07T23:50:46Z	{"time":"2026-09-07T23:50:46.740393454Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
```

## Web context
- Configure Upstream Timeouts | API Platform Docs: ### Example configuration (TOML)¶

The following example shows how to set the upstream connect timeout to 6 seconds (6000 ms):

```
[router] gateway_host  =  "" listener_port  =  8
- upstream request timeout in databricks apps when u...: Hi, i am building an application in Databricks apps, Sometimes when i try to fetch data using Databricks SQL connector in an API, it takes time to hit the SQL warehouse and if the 

## Suggested first action
Verify the upstream service health and network path, then check the upstream connect timeout configuration (currently likely ≤6s per docs) against actual upstream latency.

## Full Nemotron analysis
ROOT CAUSE: The demo-app is generating a 36.8% error rate (error_ratio_5m: 0.368) with repeated "upstream dial timeout" errors returning 502 status, indicating the upstream dependency is unreachable or exceeding the configured connection timeout. The heap allocation (686,528 bytes) and latency_mode (0) are normal, ruling out resource exhaustion or chaos injection.

ACTION: Verify the upstream service health and network path, then check the upstream connect timeout configuration (currently likely ≤6s per docs) against actual upstream latency.
