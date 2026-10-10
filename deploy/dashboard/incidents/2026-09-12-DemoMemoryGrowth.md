# DemoMemoryGrowth — 2026-09-12

- **Service**: demo-app
- **Severity**: critical
- **Status**: firing
- **Summary**: demo-app heap growing abnormally

## Root cause
The memory chaos experiment enabled at 17:36:48Z allocated a 50 MB ballast (log: "memory ballast allocated, mb:50"), causing heap to jump from ~300 KB to 52.6 MB; although chaos was cleared at 17:37:34Z, the allocated memory has not been released.  

## Evidence
- up: 1
- error_ratio_5m: NaN
- heap_alloc_bytes: 52695960
- heap_trend: [[1789234371,"788800"],[1789234386,"804768"],[1789234401,"812752"],[1789234416,"828720"],[1789234431,"836720"],[1789234446,"221704"],[1789234461,"230040"],[1789234476,"246360"],[1789234491,"254344"],[1789234506,"270312"],[1789234521,"278296"],[1789234536,"298592"],[1789234551,"306576"],[1789234566,"217784"],[1789234581,"230736"],[1789234596,"246704"],[1789234611,"255040"],[1789234626,"52659136"],[1789234641,"52667120"],[1789234656,"52683264"],[1789234671,"52695960"]]
- latency_mode: 0
- recent logs (5m window from alert start):
```
2026-09-12T17:37:34Z	{"time":"2026-09-12T17:37:34.241769386Z","level":"INFO","msg":"chaos cleared"}\n
2026-09-12T17:36:48Z	{"time":"2026-09-12T17:36:48.968930294Z","level":"INFO","msg":"chaos enabled","mode":"memory"}\n
2026-09-12T17:36:48Z	{"time":"2026-09-12T17:36:48.968900655Z","level":"WARN","msg":"memory ballast allocated","mb":50}\n
```

## Web context
- Chaos Cleared - NY NOW® Summer 2026: What We Do

Chaos Cleared designs premium canvas storage solutions that help people organize their homes beautifully. Our collections include storage for bedding, blankets, clothin
- Chaos Cleared Gray Clothing Bag 2 Pack – ChaosCleared: Choosing a selection results in a full page refresh.
 Opens in a new window. [...] ✅VALUE FOR MONEY: Unlike other bags that rip easily, these durable bags are made to last for year

## Suggested first action
Capture a heap profile to confirm the ballast is retained, then restart the demo-app pod to reclaim the ~50 MB if it isn't freed automatically.

## Full Nemotron analysis
ROOT CAUSE: The memory chaos experiment enabled at 17:36:48Z allocated a 50 MB ballast (log: "memory ballast allocated, mb:50"), causing heap to jump from ~300 KB to 52.6 MB; although chaos was cleared at 17:37:34Z, the allocated memory has not been released.  
ACTION: Capture a heap profile to confirm the ballast is retained, then restart the demo-app pod to reclaim the ~50 MB if it isn't freed automatically.
