#!/usr/bin/env python3
"""
Draws two pages of a deliberately scrappy architecture sketch.

F5's manual test asks for a badly photographed notebook page and a multi-page
document whose arrows cross pages. Nobody can photograph a whiteboard from a
build script, so this synthesises the properties that actually matter to the
review UI and cannot be got from a clean render:

  - hand-wobbled strokes and a ruled-paper background, so the vision model is
    reading a drawing rather than a diagram file;
  - a box whose label is scribbled over, so it comes back `legible: false` and
    exercises the gate that refuses an invented name;
  - an arrow that leaves page 1 towards a box drawn on page 2, so the merge
    step has a real cross-page join to make;
  - an arrow to a box that exists on neither page, so there is an unplaceable
    arrow to keep as a note;
  - a box drawn on both pages with different kinds implied, so the pages
    disagree about something and the merge has to record it.

Usage:  python3 scripts/draw-sketch.py <outdir>
"""

import math
import random
import sys
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter, ImageFont

W, H = 1400, 1000
INK = (28, 32, 46)
PAPER = (247, 244, 236)

random.seed(20260808)


def font(size):
    for path in (
        "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
        "/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
    ):
        if Path(path).exists():
            return ImageFont.truetype(path, size)
    return ImageFont.load_default()


def wobble(p, amount=2.5):
    return (p[0] + random.uniform(-amount, amount), p[1] + random.uniform(-amount, amount))


def hand_line(d, a, b, width=3, segments=10):
    """A straight line drawn by a human: never straight."""
    pts = []
    for i in range(segments + 1):
        t = i / segments
        pts.append(wobble((a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t)))
    d.line(pts, fill=INK, width=width, joint="curve")


def hand_box(d, x, y, w, h, label, size=30, kind_hint=None, double=False):
    corners = [(x, y), (x + w, y), (x + w, y + h), (x, y + h)]
    for i in range(4):
        hand_line(d, corners[i], corners[(i + 1) % 4], width=3)
    if double:  # a second pass, the way people thicken a box they mean
        for i in range(4):
            hand_line(d, wobble(corners[i], 3), wobble(corners[(i + 1) % 4], 3), width=2)
    f = font(size)
    tw = d.textlength(label, font=f)
    d.text((x + (w - tw) / 2, y + h / 2 - size * 0.7), label, font=f, fill=INK)
    if kind_hint:
        fh = font(int(size * 0.62))
        thw = d.textlength(kind_hint, font=fh)
        d.text((x + (w - thw) / 2, y + h / 2 + size * 0.15), kind_hint, font=fh, fill=INK)


def hand_cylinder(d, x, y, w, h, label):
    d.arc([x, y, x + w, y + h * 0.42], 0, 360, fill=INK, width=3)
    hand_line(d, (x, y + h * 0.21), (x, y + h * 0.79), width=3)
    hand_line(d, (x + w, y + h * 0.21), (x + w, y + h * 0.79), width=3)
    d.arc([x, y + h * 0.58, x + w, y + h], 0, 180, fill=INK, width=3)
    f = font(28)
    tw = d.textlength(label, font=f)
    d.text((x + (w - tw) / 2, y + h * 0.45), label, font=f, fill=INK)


def arrow(d, a, b, label=None, dashed=False):
    if dashed:
        n = 12
        for i in range(0, n, 2):
            t0, t1 = i / n, (i + 1) / n
            hand_line(
                d,
                (a[0] + (b[0] - a[0]) * t0, a[1] + (b[1] - a[1]) * t0),
                (a[0] + (b[0] - a[0]) * t1, a[1] + (b[1] - a[1]) * t1),
                width=3,
                segments=2,
            )
    else:
        hand_line(d, a, b, width=3, segments=14)
    ang = math.atan2(b[1] - a[1], b[0] - a[0])
    for s in (-0.42, 0.42):
        hand_line(
            d,
            b,
            (b[0] - 22 * math.cos(ang + s), b[1] - 22 * math.sin(ang + s)),
            width=3,
            segments=3,
        )
    if label:
        f = font(22)
        mx, my = (a[0] + b[0]) / 2, (a[1] + b[1]) / 2
        d.text((mx + 6, my - 30), label, font=f, fill=INK)


def paper():
    img = Image.new("RGB", (W, H), PAPER)
    d = ImageDraw.Draw(img)
    for y in range(90, H, 46):  # ruled lines
        d.line([(40, y), (W - 40, y)], fill=(205, 214, 222), width=1)
    d.line([(120, 20), (120, H - 20)], fill=(226, 190, 190), width=2)  # margin
    return img, d


def photograph(img, angle, dim):
    """Make it look taken with a phone, badly."""
    img = img.rotate(angle, expand=True, fillcolor=PAPER, resample=Image.BICUBIC)
    img = img.filter(ImageFilter.GaussianBlur(0.9))
    px = img.load()
    w, h = img.size
    for y in range(0, h, 2):  # a soft shadow across one corner
        for x in range(0, w, 2):
            f = dim + 0.28 * (1 - (x / w) * (y / h))
            r, g, b = px[x, y]
            px[x, y] = (int(r * f), int(g * f), int(b * f))
    return img


# ── page 1 ───────────────────────────────────────────────────────────────────
img1, d1 = paper()
d1.text((150, 40), "Zetta platform - rough HLD", font=font(38), fill=INK)

hand_box(d1, 170, 170, 300, 110, "Web App", size=32, kind_hint="(service)", double=True)
hand_box(d1, 640, 170, 300, 110, "API Gateway", size=32, kind_hint="(service)")
hand_box(d1, 640, 400, 300, 110, "Auth Service", size=30, kind_hint="(service)")
hand_cylinder(d1, 640, 640, 300, 150, "Postgres")

# A box whose label was scribbled out. This is the one that must come back
# illegible rather than guessed at.
hand_box(d1, 170, 620, 280, 120, "", size=30)
f = font(30)
d1.text((205, 660), "Ratelim", font=f, fill=INK)
for i in range(9):  # the scribble
    hand_line(d1, (195 + i * 9, 650), (215 + i * 9, 705), width=5, segments=3)
    hand_line(d1, (195 + i * 9, 705), (215 + i * 9, 650), width=5, segments=3)

arrow(d1, (470, 225), (640, 225), "HTTPS")
arrow(d1, (790, 280), (790, 400), "verifies")
arrow(d1, (790, 510), (790, 640), "reads/writes")
arrow(d1, (450, 680), (640, 620), "calls")

# Leaves the page, towards something drawn on page 2.
arrow(d1, (320, 280), (320, 560), "publishes")
d1.text((250, 570), "-> events (page 2)", font=font(24), fill=INK)

# Points at something that is on no page at all.
arrow(d1, (940, 225), (1200, 300), "calls")
d1.text((1150, 320), "Billing?", font=font(28), fill=INK)

photograph(img1, -3.4, 0.72).save(sys.argv[1] + "/sketch-page-1.jpg", quality=72)

# ── page 2 ───────────────────────────────────────────────────────────────────
img2, d2 = paper()
d2.text((150, 40), "page 2 - async side", font=font(38), fill=INK)

hand_box(d2, 200, 200, 320, 110, "Event Bus", size=32, kind_hint="(queue)")
hand_box(d2, 720, 200, 300, 110, "Indexer", size=32, kind_hint="(job)")
hand_box(d2, 720, 470, 300, 110, "Search Index", size=28, kind_hint="(datastore)")

# Drawn again, and this time it reads as a component rather than a service.
# The merge has to record the disagreement instead of picking quietly.
hand_box(d2, 200, 470, 320, 110, "Auth Service", size=30, kind_hint="(component)")

d2.text((160, 150), "from Web App (page 1) ->", font=font(24), fill=INK)
arrow(d2, (170, 175), (250, 200))
arrow(d2, (520, 255), (720, 255), "subscribes")
arrow(d2, (870, 310), (870, 470), "writes")
arrow(d2, (520, 525), (720, 525), "calls")

photograph(img2, 2.1, 0.66).save(sys.argv[1] + "/sketch-page-2.jpg", quality=70)

print(sys.argv[1] + "/sketch-page-1.jpg")
print(sys.argv[1] + "/sketch-page-2.jpg")
