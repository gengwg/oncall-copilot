# DemoHighErrorRate — 2026-10-18

- **Service**: demo-app
- **Severity**: critical
- **Status**: firing
- **Summary**: demo-app error rate above 10%
- **Time to brief**: 22s after the alert fired, 52s after the condition began
- **Previous**: 2026-10-18
- **Confidence**: high - the error logs directly show "upstream dial timeout" for all recent errors

## Root cause
The demo-app is healthy (up:1, low heap 232608 bytes) but cannot reach its upstream dependencies, evidenced by a 49.2% error rate with all recent logs showing "upstream dial timeout" (502); this matches the previous incident on 2026-10-10 which had identical symptoms.

## Ruled out
Application crash/heap exhaustion - heap_alloc_bytes is low at 232608 bytes and service is up (up:1); Latency chaos flag - latency_mode is 0 (not active)

## Timeline
- condition true: 03:29:48Z
- alert fired: 03:30:18Z
- briefed: 03:30:40Z

## Evidence
- up: 1
- error_ratio_5m: 0.49212598425196846
- heap_alloc_bytes: 232608
- heap_trend: n/a
- latency_mode: 0
- recent logs (5m window from alert start):
```
2026-10-18T03:30:36Z	{"time":"2026-10-18T03:30:36.738664424Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-10-18T03:30:36Z	{"time":"2026-10-18T03:30:36.4155649Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-10-18T03:30:36Z	{"time":"2026-10-18T03:30:36.098993649Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
```

## Web context
no results

## Suggested first action
Check upstream service connectivity and network paths from demo-app namespace in the minikube cluster.

## Full Nemotron analysis
ROOT CAUSE: The demo-app is healthy (up:1, low heap 232608 bytes) but cannot reach its upstream dependencies, evidenced by a 49.2% error rate with all recent logs showing "upstream dial timeout" (502); this matches the previous incident on 2026-10-10 which had identical symptoms.
CONFIDENCE: high - the error logs directly show "upstream dial timeout" for all recent errors
RULED OUT: Application crash/heap exhaustion - heap_alloc_bytes is low at 232608 bytes and service is up (up:1); Latency chaos flag - latency_mode is 0 (not active)
ACTION: Check upstream service connectivity and network paths from demo-app namespace in the minikube cluster.
