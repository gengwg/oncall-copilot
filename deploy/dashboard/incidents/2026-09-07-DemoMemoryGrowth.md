# DemoMemoryGrowth — 2026-09-07

- **Service**: demo-app
- **Severity**: critical
- **Status**: firing
- **Summary**: demo-app heap growing abnormally

## Root cause
The alert is DemoMemoryGrowth (critical) on demo-app, indicating abnormal heap growth. The live evidence shows heap_alloc_bytes at 52,663,600 bytes (about 52.6 MB). The heap_trend shows a series of timestamps and heap values. Looking at the trend: early values around 686KB, 773KB, 781KB, 797KB, 805KB, then drops to 200KB, 208KB, 224KB, 233KB, 249KB, 257KB, 277KB, 285KB, then drops to 196KB, 204KB, 225KB, 233KB, then massive jump to 52,629,200, 52,639,648, 52,655,616, 52,663,600. So there was a sudden increase around timestamp 1788825306 (which is 2026-09-07T23:55:06Z? Let's convert: 1788825306 is Unix time? Actually 1788825306 seconds since epoch is around 2026-09-07? Let's check: 1788825306 / 86400 = 20704 days since 1970, roughly 2026. The logs show chaos enabled at 2026-09-07T23:54:56Z, memory ballast allocated 50MB, then chaos cleared at 23:55:47Z. The heap trend shows the huge allocation at around 23:55:06 (since 1788825306 is 10 seconds after 1788825296? Actually the timestamps are every 15 seconds. The jump occurs at the 19th entry (index 18) with value 52629200. That corresponds to timestamp 1788825306. The log "chaos enabled" at 23:54:56Z, "memory ballast allocated 50MB" at same time. The heap jump is about 52.6 MB, matching the 50MB ballast. The chaos cleared at 23:55:47Z, but heap remains high (no decrease). The alert is

## Evidence
- up: 1
- error_ratio_5m: NaN
- heap_alloc_bytes: 52663600
- heap_trend: [[1788825051,"686528"],[1788825066,"773368"],[1788825081,"781352"],[1788825096,"797320"],[1788825111,"805304"],[1788825126,"200504"],[1788825141,"208744"],[1788825156,"224712"],[1788825171,"233048"],[1788825186,"249192"],[1788825201,"257096"],[1788825216,"277408"],[1788825231,"285744"],[1788825246,"196080"],[1788825261,"204320"],[1788825276,"225784"],[1788825291,"233944"],[1788825306,"52629200"],[1788825321,"52639648"],[1788825336,"52655616"],[1788825351,"52663600"]]
- latency_mode: 0
- recent logs (5m window from alert start):
```
2026-09-07T23:55:47Z	{"time":"2026-09-07T23:55:47.023659221Z","level":"INFO","msg":"chaos cleared"}\n
2026-09-07T23:54:56Z	{"time":"2026-09-07T23:54:56.687196268Z","level":"INFO","msg":"chaos enabled","mode":"memory"}\n
2026-09-07T23:54:56Z	{"time":"2026-09-07T23:54:56.687153506Z","level":"WARN","msg":"memory ballast allocated","mb":50}\n
```

## Web context
- Chaos Control iOS App Review: in the chaos box the chaos box I believe is an area where you kind of can chuck a heap of tasks in it's almost your kind of main task that you need to focus on um a good opportunit
- Chaos Cleared - NY NOW® Summer 2026: What We Do

Chaos Cleared designs premium canvas storage solutions that help people organize their homes beautifully. Our collections include storage for bedding, blankets, clothin

## Suggested first action
See runbook.

## Full Nemotron analysis
The alert is DemoMemoryGrowth (critical) on demo-app, indicating abnormal heap growth. The live evidence shows heap_alloc_bytes at 52,663,600 bytes (about 52.6 MB). The heap_trend shows a series of timestamps and heap values. Looking at the trend: early values around 686KB, 773KB, 781KB, 797KB, 805KB, then drops to 200KB, 208KB, 224KB, 233KB, 249KB, 257KB, 277KB, 285KB, then drops to 196KB, 204KB, 225KB, 233KB, then massive jump to 52,629,200, 52,639,648, 52,655,616, 52,663,600. So there was a sudden increase around timestamp 1788825306 (which is 2026-09-07T23:55:06Z? Let's convert: 1788825306 is Unix time? Actually 1788825306 seconds since epoch is around 2026-09-07? Let's check: 1788825306 / 86400 = 20704 days since 1970, roughly 2026. The logs show chaos enabled at 2026-09-07T23:54:56Z, memory ballast allocated 50MB, then chaos cleared at 23:55:47Z. The heap trend shows the huge allocation at around 23:55:06 (since 1788825306 is 10 seconds after 1788825296? Actually the timestamps are every 15 seconds. The jump occurs at the 19th entry (index 18) with value 52629200. That corresponds to timestamp 1788825306. The log "chaos enabled" at 23:54:56Z, "memory ballast allocated 50MB" at same time. The heap jump is about 52.6 MB, matching the 50MB ballast. The chaos cleared at 23:55:47Z, but heap remains high (no decrease). The alert is
