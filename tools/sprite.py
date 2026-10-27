#!/usr/bin/env python3
"""Writes site/locomotive.svg: the console's locomotive as a four-frame sprite
sheet, one frame sixteen units wide, drawn as the console's favicon draws the
engine at rest. Frame two and four bob down one unit and swap the wheel hubs;
a puff of steam rises over frames two to four."""
import sys

ink, red, red_light, red_shadow = "#1b1530", "#e0413f", "#ff8c86", "#9b2335"
blue, cream, steel_hi, steel, steel_dark = "#7fd8ff", "#f0f0f5", "#b9bccb", "#b4bcc8", "#6b7280"
gold, gold_shadow = "#f5c542", "#b8860b"


def body(x, y):
    return [
        (x+10, y+0, 3, 1, cream), (x+9, y+1, 1, 1, ink), (x+10, y+1, 3, 1, steel_hi), (x+13, y+1, 1, 1, ink),
        (x+10, y+2, 3, 1, steel_dark), (x+1, y+3, 5, 1, ink), (x+9, y+3, 5, 1, ink),
        (x+0, y+4, 1, 1, ink), (x+1, y+4, 1, 1, red_light), (x+2, y+4, 4, 1, red), (x+6, y+4, 1, 1, ink), (x+7, y+4, 1, 1, ink), (x+8, y+4, 7, 1, steel), (x+15, y+4, 1, 1, ink),
        (x+0, y+5, 1, 1, ink), (x+1, y+5, 1, 1, red), (x+2, y+5, 3, 1, cream), (x+5, y+5, 1, 1, red), (x+6, y+5, 1, 1, ink), (x+7, y+5, 1, 1, ink), (x+8, y+5, 3, 1, steel), (x+11, y+5, 1, 1, ink), (x+12, y+5, 3, 1, steel), (x+15, y+5, 1, 1, ink),
        (x+0, y+6, 1, 1, ink), (x+1, y+6, 5, 1, blue), (x+6, y+6, 1, 1, ink), (x+7, y+6, 1, 1, ink), (x+8, y+6, 3, 1, steel), (x+11, y+6, 1, 1, ink), (x+12, y+6, 2, 1, steel), (x+14, y+6, 1, 1, gold), (x+15, y+6, 1, 1, ink),
        (x+0, y+7, 1, 1, ink), (x+1, y+7, 5, 1, blue), (x+6, y+7, 1, 1, ink), (x+7, y+7, 1, 1, ink), (x+8, y+7, 6, 1, steel), (x+14, y+7, 1, 1, gold_shadow), (x+15, y+7, 1, 1, ink),
        (x+0, y+8, 1, 1, ink), (x+1, y+8, 1, 1, red), (x+2, y+8, 3, 1, blue), (x+5, y+8, 1, 1, red), (x+6, y+8, 9, 1, ink), (x+15, y+8, 1, 1, ink),
        (x+0, y+9, 1, 1, ink), (x+1, y+9, 5, 1, red_shadow), (x+6, y+9, 1, 1, ink), (x+7, y+9, 1, 1, ink), (x+8, y+9, 6, 1, steel), (x+15, y+9, 1, 1, ink),
        (x+0, y+10, 16, 1, ink),
    ]


def wheels(x, y, phase):
    near, far = (steel_dark, steel) if phase == "A" else (steel, steel_dark)
    return [
        (x+2, y+11, 2, 1, ink), (x+11, y+11, 2, 1, ink),
        (x+1, y+12, 1, 1, ink), (x+2, y+12, 2, 1, steel), (x+4, y+12, 1, 1, ink), (x+10, y+12, 1, 1, ink), (x+11, y+12, 2, 1, steel), (x+13, y+12, 1, 1, ink), (x+14, y+12, 1, 1, red_shadow),
        (x+1, y+13, 1, 1, ink), (x+2, y+13, 1, 1, near), (x+3, y+13, 1, 1, far), (x+4, y+13, 1, 1, ink), (x+10, y+13, 1, 1, ink), (x+11, y+13, 1, 1, near), (x+12, y+13, 1, 1, far), (x+13, y+13, 1, 1, ink), (x+14, y+13, 1, 1, red_shadow),
        (x+1, y+14, 1, 1, ink), (x+2, y+14, 2, 1, steel), (x+4, y+14, 1, 1, ink), (x+10, y+14, 1, 1, ink), (x+11, y+14, 2, 1, steel), (x+13, y+14, 1, 1, ink),
    ]


def puff(x, stage):
    return {
        "none": [],
        "near": [(x+7, 0, 2, 1, cream)],
        "drifting": [(x+5, 0, 2, 1, steel_hi), (x+6, 1, 2, 1, cream)],
        "fading": [(x+2, 0, 2, 1, steel_dark)],
    }[stage]


frames = [(0, "A", "none"), (1, "B", "near"), (0, "A", "drifting"), (1, "B", "fading")]
rects = []
for i, (bob, phase, stage) in enumerate(frames):
    x = i * 16
    rects += body(x, bob) + wheels(x, bob, phase) + puff(x, stage)

out = ['<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 16" shape-rendering="crispEdges">']
out += [f'<rect x="{x}" y="{y}" width="{w}" height="{h}" fill="{f}"/>' for x, y, w, h, f in rects]
out.append("</svg>\n")
open(sys.argv[1], "w").write("".join(out))
