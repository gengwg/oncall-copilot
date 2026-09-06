# demo-app runbook

## What it is

`demo-app` is the checkout service for the demo shop. It serves product and
order requests on port 8080, namespace `demo`, in the local minikube cluster.

Owner: @gengwg. Slack: #demo-app (simulated).

## Key metrics

- `demo_requests_total` — all requests
- `demo_errors_total` — failed requests (5xx)
- `demo_heap_alloc_bytes` — current heap
- `demo_latency_mode` / `demo_error_mode` — chaos flags (1 = active)

## Known failure modes

### High error rate (DemoHighErrorRate)

Symptom: 5xx ratio > 10%. Logs show `request failed ... upstream dial timeout`.

Usual cause: the orders upstream is unreachable or slow.

Investigate:
1. `sum(rate(demo_errors_total[5m])) / sum(rate(demo_requests_total[5m]))` — confirm ratio
2. Logs: `{service="demo-app"} |= "ERROR"` — read the actual error strings
3. If errors mention `dial timeout`, suspect the upstream, not demo-app itself.

First actions: check upstream health; if unresolved in 15m, roll back the last
deploy.

### High latency (DemoHighLatency)

Symptom: p95 latency > 800ms.

Investigate: heap size, CPU throttling, slow upstream. Check
`demo_heap_alloc_bytes` slope over 30m.

### Memory growth (DemoMemoryGrowth)

Symptom: heap > 40MB and climbing.

Usual cause: unbounded cache. Mitigation: restart the pod, then find the leak.

### Down (DemoAppDown)

Check `kubectl -n demo get pods`, recent restarts, and OOMKills.

## Past incidents

See `memory/incidents/`. Read the most recent report for this service before
concluding — recurring causes are common.
