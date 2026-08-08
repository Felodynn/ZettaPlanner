#!/usr/bin/env python3
"""
Rebuilds the header wordmark from the brand originals.

The files under `docs/assets/` are what the brand hands over: ~5200px wide and
a quarter of a megabyte each. The header renders the mark at 20px tall, so
shipping those would spend half a megabyte to draw something the size of a
line of text. These derivatives are 4x the rendered height — crisp on any
display density in use — and a few kilobytes.

Run after replacing either original:

    python3 scripts/make-wordmark.py
"""

import pathlib
from PIL import Image

TARGET_H = 88
PAIRS = [
    ('docs/assets/short_dark_logo_white_text.png', 'src/assets/felodynn-dark.png'),
    ('docs/assets/short_light_logo_black_text.png', 'src/assets/felodynn-light.png'),
]

root = pathlib.Path(__file__).resolve().parent.parent
for src, out in PAIRS:
    im = Image.open(root / src).convert('RGBA')
    # Trim the transparent margin so the mark's own edges are what get
    # centred, rather than the whitespace the export happened to include.
    im = im.crop(im.getbbox())
    w = round(im.width * TARGET_H / im.height)
    im.resize((w, TARGET_H), Image.LANCZOS).save(root / out, optimize=True)
    print(f'{out}  {w}x{TARGET_H}  {(root / out).stat().st_size / 1024:.1f} kB')
