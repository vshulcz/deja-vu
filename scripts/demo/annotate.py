#!/usr/bin/env python3
"""Draw the callouts and the spotlight the README demo fades in over the answer.

A terminal recording shows everything at once and therefore points at nothing.
Two moments carry the whole argument — the agent deciding to ask deja, and the
line where the answer turns out to be a decision from months ago — and a reader
skimming a GIF will miss both unless something says "here".

Three layers come out, each the size of the composited frame:

  callout-asked.png    a label beside "Called deja"
  spotlight.png        everything dimmed except the recalled line
  callout-recall.png   a label under the recalled line

They are faded in and out by ffmpeg at fixed times; nothing moves, because
motion in a loop is noise once you have seen it twice.

    python3 scripts/demo/annotate.py --out /tmp
"""

import argparse
from PIL import Image, ImageDraw, ImageFont

W, H = 1408, 1190
FONT = "/System/Library/Fonts/Menlo.ttc"
CARD = (30, 34, 40)
CARD_EDGE = (74, 82, 94)
TEXT = (226, 230, 236)
ACCENT = (255, 149, 92)


def card(draw: ImageDraw.ImageDraw, xy, text: str, font, pointer=None) -> None:
    """A rounded label, optionally with a small triangle aimed at the line."""
    x, y = xy
    pad_x, pad_y = 18, 11
    w = draw.textlength(text, font=font) + 2 * pad_x
    h = font.size + 2 * pad_y
    draw.rounded_rectangle([x, y, x + w, y + h], radius=10, fill=CARD, outline=CARD_EDGE, width=1)
    draw.text((x + pad_x, y + pad_y - 2), text, font=font, fill=TEXT)
    if pointer == "left":
        cy = y + h / 2
        draw.polygon([(x, cy - 9), (x, cy + 9), (x - 13, cy)], fill=CARD, outline=CARD_EDGE)
    elif pointer == "up":
        cx = x + 44
        draw.polygon([(cx - 9, y), (cx + 9, y), (cx, y - 13)], fill=CARD, outline=CARD_EDGE)


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--out", default=".")
    a = p.parse_args()
    font = ImageFont.truetype(FONT, 19)

    asked = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    d = ImageDraw.Draw(asked)
    card(d, (300, 512), "nobody asked it to — the agent went looking", font, pointer="left")
    asked.save(f"{a.out}/callout-asked.png")

    # The spotlight is a scrim with the recalled lines cut out of it, so the
    # eye lands there without anything being hidden.
    spot = Image.new("RGBA", (W, H), (6, 8, 11, 205))
    sd = ImageDraw.Draw(spot)
    sd.rounded_rectangle([78, 872, 1340, 940], radius=8, fill=(0, 0, 0, 0))
    sd.rounded_rectangle([78, 872, 1340, 940], radius=8, outline=ACCENT + (170,), width=2)
    spot.save(f"{a.out}/spotlight.png")

    recall = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    rd = ImageDraw.Draw(recall)
    card(rd, (560, 968), "a decision from eight months ago, reused", font, pointer="up")
    recall.save(f"{a.out}/callout-recall.png")

    print("callout-asked.png, spotlight.png, callout-recall.png")


if __name__ == "__main__":
    main()
