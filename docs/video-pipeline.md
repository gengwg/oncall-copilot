# Video pipeline (as built)

Real screencast plus rendered cards and piper narration. The earlier version of
this doc described a headless slideshow of static PNGs; that was a workaround
for not having screen capture, and it showed — the Telegram UI never appeared.

## Capture

The host is GNOME on Wayland, so `grim` (no `wlr-screencopy`) and `ffmpeg
-f x11grab` (captures a black XWayland root) both fail. GNOME's built-in
recorder works and has no length cap on Shell 50:

1. Arrange the screen: terminal on the left with a large font, Telegram Desktop
   on the right showing the copilot chat. Close anything private — the capture
   takes the whole screen.
2. `Ctrl+Alt+Shift+R` to start.
3. Run `./docs/demo-drive.sh` — it paces the real pipeline for camera: system
   check, chaos injection, the alert firing with elapsed timestamps, the
   investigation, the delivered brief, then clears the chaos. About 50s.
4. `Ctrl+Alt+Shift+R` to stop. Saves to `~/Videos/Screencasts/`.

## Build

```bash
./docs/narrate-demo.sh                    # piper TTS -> /tmp/opencode/video/v3/narr
./docs/build-video.sh ~/Videos/Screencasts/<file>.mp4 docs/media/demo.mp4
```

`build-video.sh` renders the cards (`docs/make-cards.py`), scales the screencast
to 1920x1080, concatenates title + stack + footage + end card, and places each
narration segment at an absolute timestamp so the speech lands on the matching
on-screen event rather than just playing end to end.

Re-recording changes those timings. The anchors are the `A1`-`A7` variables in
`build-video.sh`, expressed as offsets from the footage start; check them
against the new take's elapsed markers before rebuilding.

## Rules checklist

- duration 84s (< 3:00)
- narration names Token Factory, Nemotron, NemoClaw/OpenShell, Tavily
- audio is piper TTS only (no third-party music)
- shows the project working end to end, including the Telegram page
