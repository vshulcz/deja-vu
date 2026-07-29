#!/usr/bin/env python3
"""Draw every layer the README demo fades in over the recordings.

The demo is two takes of one question. In the first the agent has no memory and
says so; in the second it has deja and answers with a decision from months ago.
Neither half means much alone: without the first, a viewer assumes the model is
clever; without the second, there is nothing to sell.

These layers do the pointing. Nothing moves — motion in a loop is noise on the
second viewing — they only fade in and out at fixed times.

    python3 scripts/demo/annotate.py --out /tmp

Coordinates are in the composited frame, which is the terminal offset by the
window chrome (44 across, 88 down). Re-measure them when the recording changes:
a line that moves by one row puts a spotlight on the wrong sentence.
"""

import argparse
from PIL import Image, ImageDraw, ImageFont

W, H = 1408, 1190          # composited frame, before the caption strip
CAPTION_H = 96             # strip added under the window
FONT = "/System/Library/Fonts/Menlo.ttc"
BACKDROP = (9, 11, 14)
CARD = (30, 34, 40)
CARD_EDGE = (74, 82, 94)
TEXT = (226, 230, 236)
MUTED = (128, 136, 148)
ACCENT = (255, 149, 92)
COLD = (108, 132, 168)


def card(d, xy, text, font, pointer=None, edge=CARD_EDGE):
    x, y = xy
    pad_x, pad_y = 18, 11
    w = d.textlength(text, font=font) + 2 * pad_x
    h = font.size + 2 * pad_y
    d.rounded_rectangle([x, y, x + w, y + h], radius=10, fill=CARD, outline=edge, width=1)
    d.text((x + pad_x, y + pad_y - 2), text, font=font, fill=TEXT)
    if pointer == "left":
        cy = y + h / 2
        d.polygon([(x, cy - 9), (x, cy + 9), (x - 13, cy)], fill=CARD, outline=edge)
    elif pointer == "up":
        cx = x + 44
        d.polygon([(cx - 9, y), (cx + 9, y), (cx, y - 13)], fill=CARD, outline=edge)
    return w, h


def layer():
    return Image.new("RGBA", (W, H + CAPTION_H), (0, 0, 0, 0))


def caption(text, colour, out, name):
    """The act label, in the strip under the window."""
    img = layer()
    d = ImageDraw.Draw(img)
    font = ImageFont.truetype(FONT, 24)
    y = H + 26
    dot = 10
    tw = d.textlength(text, font=font)
    x = (W - tw - dot * 2 - 16) / 2
    d.ellipse([x, y + 8, x + dot, y + 8 + dot], fill=colour)
    d.text((x + dot + 16, y), text, font=font, fill=TEXT)
    img.save(f"{out}/{name}")


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--out", default=".")
    a = p.parse_args()
    font = ImageFont.truetype(FONT, 19)

    caption("without deja — the agent starts from nothing", COLD, a.out, "caption-plain.png")
    caption("with deja — same question, same agent", ACCENT, a.out, "caption-deja.png")

    # Act one: the agent admits it has never seen this.
    img = layer()
    d = ImageDraw.Draw(img)
    d.rounded_rectangle([90, 200, 1350, 250], radius=8, outline=COLD + (170,), width=2)
    card(d, (560, 286), "no memory — so it starts over", font, pointer="up", edge=COLD)
    img.save(f"{a.out}/callout-norecord.png")

    # Act two: the agent decided to look, unprompted.
    img = layer()
    d = ImageDraw.Draw(img)
    card(d, (300, 468), "nobody asked it to — the agent went looking", font, pointer="left")
    img.save(f"{a.out}/callout-asked.png")

    # Act two: the line that carries the whole argument.
    spot = Image.new("RGBA", (W, H + CAPTION_H), (6, 8, 11, 205))
    sd = ImageDraw.Draw(spot)
    sd.rectangle([0, H, W, H + CAPTION_H], fill=(0, 0, 0, 0))
    sd.rounded_rectangle([78, 832, 1340, 874], radius=8, fill=(0, 0, 0, 0))
    sd.rounded_rectangle([78, 832, 1340, 874], radius=8, outline=ACCENT + (180,), width=2)
    spot.save(f"{a.out}/spotlight.png")

    img = layer()
    d = ImageDraw.Draw(img)
    card(d, (540, 906), "a decision from eight months ago, reused", font, pointer="up")
    img.save(f"{a.out}/callout-recall.png")

    # The closing card: the one line and the one command.
    end = Image.new("RGBA", (W, H + CAPTION_H), BACKDROP + (255,))
    ed = ImageDraw.Draw(end)
    big = ImageFont.truetype(FONT, 34)
    mid = ImageFont.truetype(FONT, 22)
    small = ImageFont.truetype(FONT, 19)
    cy = (H + CAPTION_H) / 2
    line1 = "Your agents already solved this."
    line2 = "deja finds it."
    ed.text(((W - ed.textlength(line1, font=big)) / 2, cy - 150), line1, font=big, fill=TEXT)
    ed.text(((W - ed.textlength(line2, font=big)) / 2, cy - 100), line2, font=big, fill=ACCENT)
    facts = "seventeen coding agents · 84.9% hit@1 · no LLM, no embeddings · fully local"
    ed.text(((W - ed.textlength(facts, font=small)) / 2, cy - 20), facts, font=small, fill=MUTED)
    # The real one. An invented short URL on a card people screenshot is a lie
    # with a long half-life.
    cmd = "brew install vshulcz/tap/deja-vu"
    cw = ed.textlength(cmd, font=mid)
    box = [(W - cw) / 2 - 26, cy + 40, (W + cw) / 2 + 26, cy + 96]
    ed.rounded_rectangle(box, radius=10, fill=CARD, outline=CARD_EDGE, width=1)
    ed.text(((W - cw) / 2, cy + 55), cmd, font=mid, fill=TEXT)
    repo = "github.com/vshulcz/deja-vu"
    ed.text(((W - ed.textlength(repo, font=small)) / 2, cy + 130), repo, font=small, fill=MUTED)
    end.save(f"{a.out}/endcard.png")

    print("captions, callouts, spotlight, endcard")


if __name__ == "__main__":
    main()
