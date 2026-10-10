# DemoHighLatency — 2026-10-10

- **Service**: demo-app
- **Severity**: warning
- **Status**: firing
- **Summary**: demo-app latency degradation

## Root cause
The latency degradation was caused by a chaos engineering experiment that injected artificial latency (mode="latency") starting at 23:29:10Z, which was subsequently cleared at 23:29:55Z; the alert fired during the active chaos window. Current latency_mode=0 confirms the injection has stopped.

## Evidence
- up: 1
- error_ratio_5m: n/a
- heap_alloc_bytes: 194624
- heap_trend: n/a
- latency_mode: 0
- recent logs (5m window from alert start):
```
2026-10-10T23:29:55Z	{"time":"2026-10-10T23:29:55.621532663Z","level":"INFO","msg":"chaos cleared"}\n
2026-10-10T23:29:10Z	{"time":"2026-10-10T23:29:10.48714331Z","level":"INFO","msg":"chaos enabled","mode":"latency"}\n
```

## Web context
no results

## Suggested first action
Verify the chaos injection remains disabled (latency_mode=0) and monitor demo-app latency for 5 minutes to confirm return to baseline.

## Full Nemotron analysis
ROOT CAUSE: The latency degradation was caused by a chaos engineering experiment that injected artificial latency (mode="latency") starting at 23:29:10Z, which was subsequently cleared at 23:29:55Z; the alert fired during the active chaos window. Current latency_mode=0 confirms the injection has stopped.
ACTION: Verify the chaos injection remains disabled (latency_mode=0) and monitor demo-app latency for 5 minutes to confirm return to baseline.
