# DemoMemoryGrowth — 2026-09-13

- **Service**: demo-app
- **Severity**: critical
- **Status**: firing
- **Summary**: demo-app heap growing abnormally

## Root cause
The alert is about abnormal heap memory growth in demo-app. Looking at the live evidence:

1. `heap_alloc_bytes: 52627704` - current heap allocation is ~52.6 MB
2. `heap_trend` shows timestamps and values - let me analyze this trend data:
   - The trend shows pairs of [timestamp, value]
   - Values: 157486512, 157515240, 157526712, 157550712, 157563608, 157587112, 157598568, 187056, 195376, 211312, 219328
   - Wait, these values seem inconsistent. The first 7 values are around 157MB, then suddenly drop to ~187KB, then gradually increase to ~219KB
   - But the current `heap_alloc_bytes` is 52,627,704 (~52.6 MB)
   - The timestamps are around 1789279544 to 1789279694 (Unix timestamps, which would be around 2026-09-13)

3. Recent logs show:
   - "chaos enabled" with mode: "memory" at 06:09:54
   - "chaos cleared" at 06:09:13
   - "memory ballast allocated" with mb:50, held_mb:50 at 06:09:54

The runbook mentions `demo_latency_mode` / `demo_error_mode` as chaos flags (1 = active). The live evidence shows `latency_mode: 0`.

The key insight: The log shows "chaos enabled" with mode "memory" and "memory ballast allocated" with 50MB held. This is a chaos engineering experiment that's intentionally allocating memory to test the system. The heap trend shows a sudden drop from ~157MB to ~187KB, then gradual growth - this pattern suggests a garbage collection or memory release event followed by the chaos memory ballast allocation.

The root cause is the chaos engineering memory experiment that was enabled at 06:09:54, which allocated a 50MB memory ballast causing the abnormal heap growth. The heap trend shows the characteristic pattern of memory being released (drop from 157MB to 187KB) then the ballast allocation growing to 50MB.

First action should be to disable

## Evidence
- up: 1
- error_ratio_5m: NaN
- heap_alloc_bytes: 52627704
- heap_trend: [[1789279544,"157486512"],[1789279559,"157515240"],[1789279574,"157526712"],[1789279589,"157550712"],[1789279604,"157563608"],[1789279619,"157587112"],[1789279634,"157598568"],[1789279649,"187056"],[1789279664,"195376"],[1789279679,"211312"],[1789279694,"219328"]]
- latency_mode: 0
- recent logs (5m window from alert start):
```
2026-09-13T06:09:54Z	{"time":"2026-09-13T06:09:54.380064223Z","level":"INFO","msg":"chaos enabled","mode":"memory"}\n
2026-09-13T06:09:13Z	{"time":"2026-09-13T06:09:13.497707712Z","level":"INFO","msg":"chaos cleared"}\n
2026-09-13T06:09:54Z	{"time":"2026-09-13T06:09:54.380035754Z","level":"WARN","msg":"memory ballast allocated","mb":50,"held_mb":50}\n
```

## Web context
no results

## Suggested first action
See runbook.

## Full Nemotron analysis
The alert is about abnormal heap memory growth in demo-app. Looking at the live evidence:

1. `heap_alloc_bytes: 52627704` - current heap allocation is ~52.6 MB
2. `heap_trend` shows timestamps and values - let me analyze this trend data:
   - The trend shows pairs of [timestamp, value]
   - Values: 157486512, 157515240, 157526712, 157550712, 157563608, 157587112, 157598568, 187056, 195376, 211312, 219328
   - Wait, these values seem inconsistent. The first 7 values are around 157MB, then suddenly drop to ~187KB, then gradually increase to ~219KB
   - But the current `heap_alloc_bytes` is 52,627,704 (~52.6 MB)
   - The timestamps are around 1789279544 to 1789279694 (Unix timestamps, which would be around 2026-09-13)

3. Recent logs show:
   - "chaos enabled" with mode: "memory" at 06:09:54
   - "chaos cleared" at 06:09:13
   - "memory ballast allocated" with mb:50, held_mb:50 at 06:09:54

The runbook mentions `demo_latency_mode` / `demo_error_mode` as chaos flags (1 = active). The live evidence shows `latency_mode: 0`.

The key insight: The log shows "chaos enabled" with mode "memory" and "memory ballast allocated" with 50MB held. This is a chaos engineering experiment that's intentionally allocating memory to test the system. The heap trend shows a sudden drop from ~157MB to ~187KB, then gradual growth - this pattern suggests a garbage collection or memory release event followed by the chaos memory ballast allocation.

The root cause is the chaos engineering memory experiment that was enabled at 06:09:54, which allocated a 50MB memory ballast causing the abnormal heap growth. The heap trend shows the characteristic pattern of memory being released (drop from 157MB to 187KB) then the ballast allocation growing to 50MB.

First action should be to disable
