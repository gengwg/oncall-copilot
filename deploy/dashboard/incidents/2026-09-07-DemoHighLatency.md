# DemoHighLatency — 2026-09-07

- **Service**: demo-app
- **Severity**: warning
- **Status**: firing
- **Summary**: demo-app latency degradation

## Root cause
The demo-app latency degradation is caused by an active chaos experiment injecting latency (latency_mode=1), which started at 23:53:25Z and correlates with the elevated 5m error ratio of 0.368. Although logs show "chaos cleared" at 23:54:15Z, the latency_mode flag remains at 1, indicating the chaos injection is still active.

## Evidence
- up: 1
- error_ratio_5m: 0.3680555555555556
- heap_alloc_bytes: 204320
- heap_trend: n/a
- latency_mode: 1
- recent logs (5m window from alert start):
```
2026-09-07T23:54:15Z	{"time":"2026-09-07T23:54:15.69681935Z","level":"INFO","msg":"chaos cleared"}\n
2026-09-07T23:53:25Z	{"time":"2026-09-07T23:53:25.364312101Z","level":"INFO","msg":"chaos enabled","mode":"latency"}\n
2026-09-07T23:50:49Z	{"time":"2026-09-07T23:50:49.225868389Z","level":"INFO","msg":"chaos cleared"}\n
```

## Web context
- Chaos Control iOS App Review: in the chaos box the chaos box I believe is an area where you kind of can chuck a heap of tasks in it's almost your kind of main task that you need to focus on um a good opportunit
- Chaos Cleared - NY NOW® Summer 2026: What We Do

Chaos Cleared designs premium canvas storage solutions that help people organize their homes beautifully. Our collections include storage for bedding, blankets, clothin

## Suggested first action
Disable the chaos latency injection by setting demo_latency_mode=0 to stop the artificial latency and allow the service to recover.

## Full Nemotron analysis
ROOT CAUSE: The demo-app latency degradation is caused by an active chaos experiment injecting latency (latency_mode=1), which started at 23:53:25Z and correlates with the elevated 5m error ratio of 0.368. Although logs show "chaos cleared" at 23:54:15Z, the latency_mode flag remains at 1, indicating the chaos injection is still active.

ACTION: Disable the chaos latency injection by setting demo_latency_mode=0 to stop the artificial latency and allow the service to recover.
