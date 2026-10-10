# DemoHighErrorRate — 2026-09-14

- **Service**: demo-app
- **Severity**: critical
- **Status**: firing
- **Summary**: demo-app error rate above 10%

## Root cause
The demo-app is returning 502 errors with "upstream dial timeout" for 50% of requests (error_ratio_5m: 0.5), indicating it cannot establish connections to its upstream dependency. Heap allocation (962200 bytes) and latency_mode (0) are normal, ruling out resource exhaustion or chaos injection.

## Evidence
- up: 1
- error_ratio_5m: 0.5
- heap_alloc_bytes: 962200
- heap_trend: n/a
- latency_mode: 0
- recent logs (5m window from alert start):
```
2026-09-14T05:16:06Z	{"time":"2026-09-14T05:16:06.536620486Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-09-14T05:16:05Z	{"time":"2026-09-14T05:16:05.903381721Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-09-14T05:16:05Z	{"time":"2026-09-14T05:16:05.198965973Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
```

## Web context
no results

## Suggested first action
Verify the upstream service (likely product catalog or payment) is healthy and reachable from demo-app's network namespace, and check for any recent deployment or network policy changes affecting egress.

## Full Nemotron analysis
ROOT CAUSE: The demo-app is returning 502 errors with "upstream dial timeout" for 50% of requests (error_ratio_5m: 0.5), indicating it cannot establish connections to its upstream dependency. Heap allocation (962200 bytes) and latency_mode (0) are normal, ruling out resource exhaustion or chaos injection.

ACTION: Verify the upstream service (likely product catalog or payment) is healthy and reachable from demo-app's network namespace, and check for any recent deployment or network policy changes affecting egress.
