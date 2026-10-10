# DemoHighLatency — 2026-09-12

- **Service**: demo-app
- **Severity**: warning
- **Status**: firing
- **Summary**: demo-app latency degradation

## Root cause
The alert is DemoHighLatency on demo-app. The live evidence shows:
- up: 1 (service is up)
- error_ratio_5m: 0.503448275862069 (about 50% error rate!)
- heap_alloc_bytes: 230736 (heap allocation)
- heap_trend: n/a
- latency_mode: 0 (but wait, the logs show chaos enabled with mode latency)

Recent logs:
- 2026-09-12T17:36:08Z: "chaos cleared"
- 2026-09-12T17:35:17Z: "chaos enabled","mode":"latency"
- 2026-09-12T17:32:36Z: "chaos cleared"

So chaos was enabled for latency at 17:35:17, then cleared at 17:36:08. The alert is for high latency. The error ratio is 50% which is very high. The latency_mode in evidence is 0 (maybe after cleared). But the chaos was enabled for latency, which likely injects latency. The runbook mentions chaos flags: demo_latency_mode / demo_error_mode — chaos flags (1 = active). So latency_mode=1 would mean chaos latency injection is active. The evidence shows latency_mode: 0, but the log shows it was enabled at 17:35:17 and cleared at 17:36:08. The alert likely fired during that window.

The web context is irrelevant (Chaos Cleared is a storage product). The error ratio is 0.5, which is huge. But the alert is for high latency, not high error rate. However, the runbook has a known failure mode for High error rate (DemoHighErrorRate). But the alert is DemoHighLatency. The evidence shows error_ratio_5m 0.5, which might be a side effect of latency injection causing timeouts? Or maybe the chaos latency injection also causes errors? The runbook says chaos flags: demo_latency_mode / demo_error_mode. So there are two separate chaos modes: latency and error. The log says "chaos enabled","mode":"latency". So only latency injection was enabled. That would increase latency, not necessarily errors. But error ratio is 50%? Could be that the latency injection causes requests to timeout and return 5xx? Or maybe the demo-app returns errors when latency is high? The alert is for latency degradation, but the evidence shows high error ratio. The root cause likely: chaos engineering latency injection was enabled at 17:35:17, causing artificial latency increase, which triggered the

## Evidence
- up: 1
- error_ratio_5m: 0.503448275862069
- heap_alloc_bytes: 230736
- heap_trend: n/a
- latency_mode: 0
- recent logs (5m window from alert start):
```
2026-09-12T17:36:08Z	{"time":"2026-09-12T17:36:08.009183323Z","level":"INFO","msg":"chaos cleared"}\n
2026-09-12T17:35:17Z	{"time":"2026-09-12T17:35:17.680979569Z","level":"INFO","msg":"chaos enabled","mode":"latency"}\n
2026-09-12T17:32:36Z	{"time":"2026-09-12T17:32:36.577970237Z","level":"INFO","msg":"chaos cleared"}\n
```

## Web context
- Chaos Cleared - NY NOW® Summer 2026: What We Do

Chaos Cleared designs premium canvas storage solutions that help people organize their homes beautifully. Our collections include storage for bedding, blankets, clothin
- Chaos Cleared Gray Clothing Bag 2 Pack – ChaosCleared: Choosing a selection results in a full page refresh.
 Opens in a new window. [...] ✅VALUE FOR MONEY: Unlike other bags that rip easily, these durable bags are made to last for year

## Suggested first action
See runbook.

## Full Nemotron analysis
The alert is DemoHighLatency on demo-app. The live evidence shows:
- up: 1 (service is up)
- error_ratio_5m: 0.503448275862069 (about 50% error rate!)
- heap_alloc_bytes: 230736 (heap allocation)
- heap_trend: n/a
- latency_mode: 0 (but wait, the logs show chaos enabled with mode latency)

Recent logs:
- 2026-09-12T17:36:08Z: "chaos cleared"
- 2026-09-12T17:35:17Z: "chaos enabled","mode":"latency"
- 2026-09-12T17:32:36Z: "chaos cleared"

So chaos was enabled for latency at 17:35:17, then cleared at 17:36:08. The alert is for high latency. The error ratio is 50% which is very high. The latency_mode in evidence is 0 (maybe after cleared). But the chaos was enabled for latency, which likely injects latency. The runbook mentions chaos flags: demo_latency_mode / demo_error_mode — chaos flags (1 = active). So latency_mode=1 would mean chaos latency injection is active. The evidence shows latency_mode: 0, but the log shows it was enabled at 17:35:17 and cleared at 17:36:08. The alert likely fired during that window.

The web context is irrelevant (Chaos Cleared is a storage product). The error ratio is 0.5, which is huge. But the alert is for high latency, not high error rate. However, the runbook has a known failure mode for High error rate (DemoHighErrorRate). But the alert is DemoHighLatency. The evidence shows error_ratio_5m 0.5, which might be a side effect of latency injection causing timeouts? Or maybe the chaos latency injection also causes errors? The runbook says chaos flags: demo_latency_mode / demo_error_mode. So there are two separate chaos modes: latency and error. The log says "chaos enabled","mode":"latency". So only latency injection was enabled. That would increase latency, not necessarily errors. But error ratio is 50%? Could be that the latency injection causes requests to timeout and return 5xx? Or maybe the demo-app returns errors when latency is high? The alert is for latency degradation, but the evidence shows high error ratio. The root cause likely: chaos engineering latency injection was enabled at 17:35:17, causing artificial latency increase, which triggered the
