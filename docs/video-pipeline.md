# Video pipeline (as built)

Headless-host approach: no screen recording (the desktop has no visible
terminal window). The video is composited from:

1. **Intro slide** — rendered PNG from PIL (project name + stack callouts)
2. **Live dashboard** — headless chromium screenshot of the public tunnel URL
   (the Cloudflare quick-tunnel serving the local incident feed)
3. **Grafana** — headless chromium shot of the embedded explore
4. **Terminal output** — the e2e log rendered to PNG via PIL

plus **piper TTS narration** (`docs/narrate.sh`, en_US-lessac-medium voice).

## Build

```bash
# 1. services up + tunnel
bash deploy/demo-services.sh start

# 2. narration segments -> full.wav (data-dir holds the voice model)
bash docs/narrate.sh

# 3. composite
ffmpeg -loop 1 -t 18 -i /tmp/opencode/video/intro.png \
       -loop 1 -t 14 -i /tmp/opencode/video/dash.png \
       -loop 1 -t 14 -i /tmp/opencode/video/grafana.png \
       -loop 1 -t 32 -i /tmp/opencode/video/term.png \
       -i /tmp/opencode/video/narration/full.wav \
  -filter_complex "[0:v]scale=1920:1080[v0];[1:v]scale=1920:1080[v1];\
[2:v]scale=1920:1080[v2];[3:v]scale=1920:1080[v3];\
[v0][v1][v2][v3]concat=n=4:v=1:a=0[v]" \
  -map "[v]" -map "4:a" -shortest \
  -c:v libx264 -c:a aac /tmp/opencode/video/final.mp4
```

## Rules checklist

- duration 78s (< 3:00) ✓
- narration names Token Factory + Nemotron + NemoClaw/OpenShell + Tavily ✓
- audio is piper TTS only (no copyrighted/third-party music) ✓
- content shows the project functioning (live feed + sequences) ✓

Upload `/tmp/opencode/video/final.mp4` to YouTube as PUBLIC.
