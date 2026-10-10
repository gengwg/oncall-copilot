# DemoHighLatency — 2026-09-13

- **Service**: demo-app
- **Severity**: warning
- **Status**: firing
- **Summary**: demo-app latency degradation

## Root cause
The latency_mode chaos flag is active (value 1), and memory ballast allocations have increased held memory from 50 MB to 150 MB in 3 seconds, causing GC pressure and latency degradation.  

## Evidence
- up: 1
- error_ratio_5m: NaN
- heap_alloc_bytes: 212384
- heap_trend: n/a
- latency_mode: 1
- recent logs (5m window from alert start):
```
2026-09-13T06:08:20Z	{"time":"2026-09-13T06:08:20.100115562Z","level":"WARN","msg":"memory ballast allocated","mb":50,"held_mb":150}\n
2026-09-13T06:08:19Z	{"time":"2026-09-13T06:08:19.057045629Z","level":"WARN","msg":"memory ballast allocated","mb":50,"held_mb":100}\n
2026-09-13T06:08:18Z	{"time":"2026-09-13T06:08:18.011369761Z","level":"WARN","msg":"memory ballast allocated","mb":50,"held_mb":50}\n
```

## Web context
no results

## Suggested first action
Disable the latency_mode chaos flag (set demo_latency_mode to 0) to stop synthetic latency injection.

## Full Nemotron analysis
ROOT CAUSE: The latency_mode chaos flag is active (value 1), and memory ballast allocations have increased held memory from 50 MB to 150 MB in 3 seconds, causing GC pressure and latency degradation.  
ACTION: Disable the latency_mode chaos flag (set demo_latency_mode to 0) to stop synthetic latency injection.
