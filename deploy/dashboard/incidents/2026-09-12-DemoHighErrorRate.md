# DemoHighErrorRate — 2026-09-12

- **Service**: demo-app
- **Severity**: critical
- **Status**: firing
- **Summary**: demo-app error rate above 10%

## Root cause
The 50.3% error rate (error_ratio_5m: 0.503) is driven by "upstream dial timeout" 502 errors on path "/", indicating demo-app cannot establish TCP connections to its upstream dependency despite the app itself being up (up: 1) with normal heap (788800 bytes). The dial_timeout occurs during the initial connection phase before request forwarding, pointing to an unreachable or misconfigured upstream service in the demo namespace.

## Evidence
- up: 1
- error_ratio_5m: 0.5034482758620689
- heap_alloc_bytes: 788800
- heap_trend: n/a
- latency_mode: 0
- recent logs (5m window from alert start):
```
2026-09-12T17:32:36Z	{"time":"2026-09-12T17:32:36.38493807Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-09-12T17:32:36Z	{"time":"2026-09-12T17:32:36.018561406Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
2026-09-12T17:32:34Z	{"time":"2026-09-12T17:32:34.964727767Z","level":"ERROR","msg":"request failed","path":"/","err":"upstream dial timeout","status":502}\n
```

## Web context
- Trying multiple upstream nodes/IP's before failing with 502 - Help - Caddy Community: `dial_timeout` is just for the “dialing” phase, i.e. the initial TCP connection to the upstream, before Caddy sends over the request. There’s other timeouts you can configure like 
- "UNAVAILABLE:upstream request timeout" when trying ...: This repository was archived by the owner on May 23, 2023. It is now read-only.

apigee   /  istio-mixer-adapter  Public archive

 Notifications  You must be signed in to change no

## Suggested first action
Verify the upstream service (likely product/order backend) is running, reachable on its expected port, and that demo-app's upstream configuration matches the actual service endpoints in the minikube cluster.

## Full Nemotron analysis
ROOT CAUSE: The 50.3% error rate (error_ratio_5m: 0.503) is driven by "upstream dial timeout" 502 errors on path "/", indicating demo-app cannot establish TCP connections to its upstream dependency despite the app itself being up (up: 1) with normal heap (788800 bytes). The dial_timeout occurs during the initial connection phase before request forwarding, pointing to an unreachable or misconfigured upstream service in the demo namespace.

ACTION: Verify the upstream service (likely product/order backend) is running, reachable on its expected port, and that demo-app's upstream configuration matches the actual service endpoints in the minikube cluster.
