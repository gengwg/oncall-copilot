# DemoHighErrorRate — 2026-10-10

- **Service**: demo-app
- **Severity**: critical
- **Status**: firing
- **Summary**: demo-app error rate above 10%

## Root cause
The demo-app is experiencing a 48.5% error rate (error_ratio_5m: 0.485) with all recent errors showing "upstream dial timeout" (502), indicating the application cannot establish connections to its upstream dependencies despite the app itself being up (up: 1) and healthy heap usage (960712 bytes).

## Evidence
- up: 1
- error_ratio_5m: 0.48484848484848486
- heap_alloc_bytes: 960712
- heap_trend: n/a
- latency_mode: 0
- recent logs (5m window from alert start):
```
2026-10-10T21:58:05Z	{"time":"2026-10-10T21:58:05.689177451Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-10-10T21:58:05Z	{"time":"2026-10-10T21:58:05.332639928Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-10-10T21:58:04Z	{"time":"2026-10-10T21:58:04.670166782Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
```

## Web context
no results

## Suggested first action
Check the health and reachability of demo-app's upstream services (likely product catalog or order services) and verify network connectivity from the demo namespace.

## Full Nemotron analysis
ROOT CAUSE: The demo-app is experiencing a 48.5% error rate (error_ratio_5m: 0.485) with all recent errors showing "upstream dial timeout" (502), indicating the application cannot establish connections to its upstream dependencies despite the app itself being up (up: 1) and healthy heap usage (960712 bytes).

ACTION: Check the health and reachability of demo-app's upstream services (likely product catalog or order services) and verify network connectivity from the demo namespace.
