# DemoHighLatency — 2026-10-18

- **Service**: demo-app
- **Severity**: warning
- **Status**: firing
- **Summary**: demo-app latency degradation
- **Time to brief**: 22s after the alert fired, 52s after the condition began
- **Previous**: 2026-10-18
- **Confidence**: <high|medium|low> - <the one evidence item it rests on>

## Root cause
<2 sentences>

## Ruled out
<cause - evidence against it>; <cause - evidence against it>

## Timeline
- condition true: 00:48:18Z
- alert fired: 00:48:48Z
- briefed: 00:49:10Z

## Evidence
- up: 1
- error_ratio_5m: n/a
- heap_alloc_bytes: 238736
- heap_trend: n/a
- latency_mode: 1
- recent logs (5m window from alert start):
```
2026-10-18T00:42:04Z	{"time":"2026-10-18T00:42:04.62732506Z","level":"WARN","msg":"slow request","path":"/","injected_latency_ms":1293}\n
2026-10-18T00:48:06Z	{"time":"2026-10-18T00:48:06.914244611Z","level":"INFO","msg":"chaos enabled","mode":"latency"}\n
2026-10-18T00:43:27Z	{"time":"2026-10-18T00:43:27.75591682Z","level":"INFO","msg":"chaos cleared"}\n
```

## Web context
no results

## Suggested first action
<1 sentence>

## Full Nemotron analysis
The alert is DemoHighLatency on demo-app. Live evidence shows up:1, heap_alloc_bytes: 238736, latency_mode: 1. Recent logs show "slow request" with injected_latency_ms:1293 at 00:42:04Z, "chaos enabled" mode:"latency" at 00:48:06Z, and "chaos cleared" at 00:43:27Z. Wait, the timestamps: 00:42:04 slow request, 00:43:27 chaos cleared, 00:48:06 chaos enabled. That seems out of order? Actually the logs are from a 5m window from alert start. The alert start time not given but assume around 00:42. The logs show chaos cleared at 00:43:27, then chaos enabled at 00:48:06. But latency_mode is 1 (active). The previous incident note says latency degradation caused by active chaos engineering experiment injecting artificial latency, indicated by latency_mode=1 and log entries showing injected_latency_ms values of 1144-1548 ms. Matches previous incident on 2026-10-10.

Thus root cause: chaos latency injection is active (latency_mode=1) and injecting ~1293ms latency, causing high latency. The previous incident had same cause.

Ruled out: high error rate (error_ratio_5m n/a, no error logs), resource exhaustion (heap_alloc_bytes only 238KB, low). 

Action: disable chaos latency injection (set latency_mode=0) or wait for experiment to end.

Need to output in format: four lines.

ROOT CAUSE: <2 sentences>
CONFIDENCE: <high|medium|low> - <the one evidence item it rests on>
RULED OUT: <cause - evidence against it>; <cause - evidence against it>
ACTION: <1 sentence>

Let's craft.

ROOT CAUSE: The latency degradation is caused by an active chaos engineering experiment injecting artificial latency of ~1293 ms, as shown by latency_mode=1 and the "slow request" log with injected_latency_ms=1293. This matches the previous incident on 2026-10-10 where the same chaos latency injection triggered the alert.

CONFIDENCE: high - latency_mode=1 and injected_latency_ms=1293 in logs

RULED OUT: high error rate - error_ratio_5m is n/a and no error logs present; resource exhaustion - heap_alloc_bytes is only 238736 bytes, well within normal limits

ACTION: Disable the chaos latency
