#!/usr/bin/env python3
"""Draw the window the README demo sits in, and the caps that round its corners.

A bare terminal capture reads as a screen recording someone made in a hurry.
The chrome is what makes it read as a product shot: a title bar, a quiet label,
a margin, and corners that are not square.

Two files come out. chrome.png is the background — margin, panel, title bar —
and the terminal is composited on top of it. corners.png is drawn on top of the
terminal afterwards; it is transparent everywhere except outside the rounded
panel, so the square corners of the video get covered back up.

    python3 scripts/demo/frame.py --width 1320 --height 880

The height is the terminal cropped to its content, not the height it was
recorded at: the recording needs enough rows that nothing scrolls, and the
leftover blank rows are cut before framing.
"""

import argparse
from PIL import Image, ImageDraw, ImageFont

BACKDROP = (9, 11, 14)
PANEL = (22, 22, 22)  # matches the terminal background exactly, or the seam shows
BORDER = (46, 52, 60)
LABEL = (122, 130, 140)
DOTS = [(255, 95, 86), (255, 189, 46), (39, 201, 63)]
FONT = "/System/Library/Fonts/Menlo.ttc"


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--width", type=int, default=1320)
    p.add_argument("--height", type=int, default=880)
    p.add_argument("--pad", type=int, default=44)
    p.add_argument("--bar", type=int, default=44)
    p.add_argument("--radius", type=int, default=16)
    p.add_argument("--title", default="claude — ~/work-demo")
    p.add_argument("--out", default=".")
    a = p.parse_args()

    w, h = a.width + 2 * a.pad, a.height + a.bar + 2 * a.pad
    panel = [a.pad - 1, a.pad - 1, a.pad + a.width, a.pad + a.bar + a.height]

    chrome = Image.new("RGB", (w, h), BACKDROP)
    d = ImageDraw.Draw(chrome)
    d.rounded_rectangle(panel, radius=a.radius, fill=PANEL, outline=BORDER, width=1)

    cy = a.pad + a.bar // 2
    for i, colour in enumerate(DOTS):
        x = a.pad + 26 + i * 22
        d.ellipse([x - 6, cy - 6, x + 6, cy + 6], fill=colour)

    font = ImageFont.truetype(FONT, 17)
    d.text(
        (a.pad + (a.width - d.textlength(a.title, font=font)) / 2, cy - 10),
        a.title,
        font=font,
        fill=LABEL,
    )
    d.line([a.pad, a.pad + a.bar, a.pad + a.width - 1, a.pad + a.bar], fill=(34, 38, 44))
    chrome.save(f"{a.out}/chrome.png")

    # Everything outside the rounded panel, painted in the backdrop colour, so
    # compositing it over the video restores the corner radius.
    cap = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    ImageDraw.Draw(cap).rounded_rectangle(panel, radius=a.radius, fill=(255, 255, 255, 255))
    corners = Image.new("RGBA", (w, h), BACKDROP + (255,))
    corners.putalpha(cap.split()[3].point(lambda v: 255 - v))
    corners.save(f"{a.out}/corners.png")

    # patch.png replaces the two header lines that name the account with
    # neutral text. Painting them out left grey holes that read as censorship;
    # putting plain text back reads as the header it actually is.
    patch = Image.new("RGBA", (a.width, a.height), (0, 0, 0, 0))
    pd = ImageDraw.Draw(patch)
    body = ImageFont.truetype(FONT, 19)
    pd.rectangle([238, 94, 508, 124], fill=PANEL + (255,))
    pd.text((249, 98), "Welcome back", font=body, fill=(230, 230, 230))
    pd.rectangle([45, 206, 695, 256], fill=PANEL + (255,))
    pd.text((54, 212), "Opus 5 (1M context) · Claude Max", font=body, fill=(138, 138, 138))
    patch.save(f"{a.out}/patch.png")

    print(f"{w}x{h}: chrome.png, corners.png, patch.png")


if __name__ == "__main__":
    main()
