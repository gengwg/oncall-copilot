# DemoHighLatency — 2026-10-18

- **Service**: demo-app
- **Severity**: warning
- **Status**: firing
- **Summary**: demo-app latency degradation
- **Time to brief**: 32s after the alert fired, 62s after the condition began
- **Previous**: 2026-10-18
- **Confidence**: high - the latency_mode metric is 1 and the log shows "chaos enabled" mode latency at alert start.  

## Root cause
The latency degradation is caused by the chaos latency injection being enabled at 00:53:55Z (latency_mode=1), which artificially adds latency to requests.  

## Ruled out
High error rate - error_ratio_5m is n/a and no error logs; Heap memory pressure - heap_alloc_bytes is only 247KB, well within normal limits.  

## Timeline
- condition true: 00:54:08Z
- alert fired: 00:54:38Z
- briefed: 00:55:10Z

## Evidence
- up: 1
- error_ratio_5m: n/a
- heap_alloc_bytes: 247424
- heap_trend: n/a
- latency_mode: 1
- recent logs (5m window from alert start):
```
2026-10-18T00:53:55Z	{"time":"2026-10-18T00:53:55.839141799Z","level":"INFO","msg":"chaos enabled","mode":"latency"}\n
2026-10-18T00:49:18Z	{"time":"2026-10-18T00:49:18.2488203Z","level":"INFO","msg":"chaos cleared"}\n
2026-10-18T00:48:06Z	{"time":"2026-10-18T00:48:06.914244611Z","level":"INFO","msg":"chaos enabled","mode":"latency"}\n
```

## Web context
no results

## Suggested first action
Disable the chaos latency injection via the chaos API or wait for it to be cleared (since it may be a test).

## Full Nemotron analysis
ROOT CAUSE: The latency degradation is caused by the chaos latency injection being enabled at 00:53:55Z (latency_mode=1), which artificially adds latency to requests.  
CONFIDENCE: high - the latency_mode metric is 1 and the log shows "chaos enabled" mode latency at alert start.  
RULED OUT: High error rate - error_ratio_5m is n/a and no error logs; Heap memory pressure - heap_alloc_bytes is only 247KB, well within normal limits.  
ACTION: Disable the chaos latency injection via the chaos API or wait for it to be cleared (since it may be a test).
