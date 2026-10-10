# DemoHighErrorRate — 2026-09-13

- **Service**: demo-app
- **Severity**: critical
- **Status**: firing
- **Summary**: demo-app error rate above 10%

## Root cause
The demo-app is experiencing a 52.9% error rate driven by "upstream dial timeout" errors (502 responses), indicating it cannot establish connections to its downstream dependency within the dial timeout window. With heap allocation at only 476KB and latency_mode inactive (0), this is not a resource exhaustion or chaos-injection issue but a network or upstream service availability problem.

## Evidence
- up: 1
- error_ratio_5m: 0.5294117647058824
- heap_alloc_bytes: 476816
- heap_trend: n/a
- latency_mode: 0
- recent logs (5m window from alert start):
```
2026-09-13T19:41:32Z	{"time":"2026-09-13T19:41:32.952277028Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-09-13T19:41:32Z	{"time":"2026-09-13T19:41:32.215366628Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-09-13T19:41:31Z	{"time":"2026-09-13T19:41:31.157156805Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
```

## Web context
no results

## Suggested first action
Verify the upstream service (likely payment/inventory) is reachable from the demo namespace and check its health/endpoint status in the minikube cluster.

## Full Nemotron analysis
ROOT CAUSE: The demo-app is experiencing a 52.9% error rate driven by "upstream dial timeout" errors (502 responses), indicating it cannot establish connections to its downstream dependency within the dial timeout window. With heap allocation at only 476KB and latency_mode inactive (0), this is not a resource exhaustion or chaos-injection issue but a network or upstream service availability problem.

ACTION: Verify the upstream service (likely payment/inventory) is reachable from the demo namespace and check its health/endpoint status in the minikube cluster.
