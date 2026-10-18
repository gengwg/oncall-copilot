# DemoHighLatency — 2026-10-18

- **Service**: demo-app
- **Severity**: warning
- **Status**: firing
- **Summary**: demo-app latency degradation
- **Time to brief**: 32s after the alert fired
- **Previous**: 2026-10-10
- **Confidence**: high - latency_mode=1 and injected_latency_ms in logs

## Root cause
The latency degradation is caused by an active chaos engineering experiment injecting artificial latency, as indicated by latency_mode=1 and log entries showing injected_latency_ms values of 1144-1548 ms. This matches the previous incident on 2026-10-10 where the same chaos latency injection caused the alert.

## Evidence
- up: 1
- error_ratio_5m: 0
- heap_alloc_bytes: 323608
- heap_trend: n/a
- latency_mode: 1
- recent logs (5m window from alert start):
```
2026-10-18T00:42:04Z	{"time":"2026-10-18T00:42:04.62732506Z","level":"WARN","msg":"slow request","path":"/","injected_latency_ms":1293}\n
2026-10-18T00:42:03Z	{"time":"2026-10-18T00:42:03.324452097Z","level":"WARN","msg":"slow request","path":"/","injected_latency_ms":1548}\n
2026-10-18T00:42:01Z	{"time":"2026-10-18T00:42:01.766378957Z","level":"WARN","msg":"slow request","path":"/","injected_latency_ms":1144}\n
```

## Web context
no results

## Suggested first action
Disable the chaos latency injection by setting demo_latency_mode to 0.

## Full Nemotron analysis
ROOT CAUSE: The latency degradation is caused by an active chaos engineering experiment injecting artificial latency, as indicated by latency_mode=1 and log entries showing injected_latency_ms values of 1144-1548 ms. This matches the previous incident on 2026-10-10 where the same chaos latency injection caused the alert.

CONFIDENCE: high - latency_mode=1 and injected_latency_ms in logs

ACTION: Disable the chaos latency injection by setting demo_latency_mode to 0.
