#!/usr/bin/env python3
"""Render the demo video's title, stack, and end cards to <workdir>/card-*.png."""
import sys
from PIL import Image, ImageDraw, ImageFont

W, H = 1920, 1080
BG = (13, 17, 23)
FG = (230, 237, 243)
MUTED = (125, 143, 161)
ACCENT = (118, 208, 140)

SANS = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
BOLD = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"
MONO = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"


def font(path, size):
    return ImageFont.truetype(path, size)


def card(path, draw_fn):
    im = Image.new("RGB", (W, H), BG)
    draw_fn(ImageDraw.Draw(im))
    im.save(path)


def centered(d, y, text, f, fill):
    w = d.textbbox((0, 0), text, font=f)[2]
    d.text(((W - w) / 2, y), text, font=f, fill=fill)


def title(d):
    centered(d, 380, "OnCall Copilot", font(BOLD, 104), FG)
    centered(d, 520, "It investigates before it pages you", font(SANS, 46), MUTED)
    d.line([(760, 620), (1160, 620)], fill=ACCENT, width=3)
    centered(d, 660, "Nebius x NVIDIA Global AI Hackathon  ·  Personal AI",
             font(SANS, 30), MUTED)


def stack(d):
    d.text((240, 240), "How it works", font=font(BOLD, 64), fill=FG)
    d.line([(240, 340), (600, 340)], fill=ACCENT, width=3)
    rows = [
        ("Kubernetes service", "Prometheus · Alertmanager · Loki · Grafana"),
        ("Agent runtime", "NVIDIA NemoClaw + OpenShell, deny-by-default egress"),
        ("Reasoning", "NVIDIA Nemotron 3 Ultra on Nebius Token Factory"),
        ("Web context", "Tavily search for unfamiliar errors"),
        ("Output", "Telegram brief: cause, evidence, first action"),
    ]
    y = 420
    for label, value in rows:
        d.text((240, y), label, font=font(BOLD, 34), fill=ACCENT)
        d.text((700, y), value, font=font(MONO, 30), fill=FG)
        y += 92


def end(d):
    centered(d, 400, "Investigated, then paged.", font(BOLD, 72), FG)
    centered(d, 520, "github.com/gengwg/oncall-copilot", font(MONO, 38), ACCENT)
    centered(d, 620, "Open models · sandboxed · your telemetry stays yours",
             font(SANS, 32), MUTED)


def main():
    work = sys.argv[1] if len(sys.argv) > 1 else "."
    card(f"{work}/card-title.png", title)
    card(f"{work}/card-stack.png", stack)
    card(f"{work}/card-end.png", end)
    print(f"cards written to {work}")


if __name__ == "__main__":
    main()
