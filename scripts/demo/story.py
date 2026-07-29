#!/usr/bin/env python3
"""Render the README demo as a designed sequence rather than a screen capture.

A terminal recording shows everything at once in six-point type. It is honest
and it is unreadable, and a reader gives a GIF about four seconds before
deciding. This draws the same story — the same sentences, quoted from the two
real sessions in scripts/demo/agent.tape and agent-plain.tape — at a size
someone can actually read, in the palette the site uses: phosphor green on
near-black, scanlines, a vignette.

Four scenes, fifteen seconds:

  1. the question, typed
  2. the same agent answering it twice, without memory and with it
  3. what deja actually handed back
  4. the line and the command

    python3 scripts/demo/story.py --out /tmp/frames
    ffmpeg -framerate 20 -i /tmp/frames/%04d.png ... demo.gif
"""

import argparse
import math
import os
from PIL import Image, ImageDraw, ImageFilter, ImageFont

W, H = 1200, 675
FPS = 20

BG = (5, 8, 7)
PH = (74, 240, 139)
PH_HI = (138, 255, 192)
PH_DIM = (47, 148, 89)
AMBER = (255, 180, 84)
BODY = (169, 203, 182)
FAINT = (93, 138, 110)
COLD = (96, 116, 132)

MONO = "/System/Library/Fonts/Menlo.ttc"


def font(size: int) -> ImageFont.FreeTypeFont:
    return ImageFont.truetype(MONO, size)


def glow(base: Image.Image, layer: Image.Image) -> Image.Image:
    """Phosphor bloom: the text blurred twice under itself — a wide halo and a
    tight one. One pass looked flat, which is what made the first render read as
    plain text on black rather than light coming off a screen."""
    wide = layer.filter(ImageFilter.GaussianBlur(14))
    tight = layer.filter(ImageFilter.GaussianBlur(4))
    out = Image.alpha_composite(base, wide)
    out = Image.alpha_composite(out, tight)
    out = Image.alpha_composite(out, tight)
    return Image.alpha_composite(out, layer)


def centred(d: ImageDraw.ImageDraw, y: int, text: str, f, fill) -> None:
    d.text(((W - d.textlength(text, font=f)) / 2, y), text, font=f, fill=fill)


def scanlines_and_vignette() -> Image.Image:
    """The two effects the site uses to make a page feel like a screen."""
    overlay = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    d = ImageDraw.Draw(overlay)
    for y in range(0, H, 3):
        d.line([(0, y), (W, y)], fill=(0, 0, 0, 40))
    # A gentle vignette. The first attempt used a third of full black and ate
    # the corners of the layout along with the glow.
    vig = Image.new("L", (W, H), 0)
    vd = ImageDraw.Draw(vig)
    cx, cy = W / 2, H * 0.45
    for i in range(90):
        t = i / 89
        r = 0.6 + 0.4 * t
        vd.ellipse(
            [cx - W * 0.85 * r, cy - H * 1.0 * r, cx + W * 0.85 * r, cy + H * 1.0 * r],
            outline=int(70 * t * t),
            width=8,
        )
    shade = Image.new("RGBA", (W, H), (0, 0, 0, 255))
    shade.putalpha(vig)
    return Image.alpha_composite(overlay, shade)


def frame() -> tuple[Image.Image, Image.Image, ImageDraw.ImageDraw]:
    base = Image.new("RGBA", (W, H), BG + (255,))
    ink = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    d = ImageDraw.Draw(ink)
    # The wordmark sits on every frame: a demo people screenshot should say
    # whose it is without a caption.
    d.text((56, 44), "deja", font=font(22), fill=PH)
    d.text((110, 44), "-vu", font=font(22), fill=PH_DIM)
    return base, ink, d


def ease(t: float) -> float:
    return 0 if t <= 0 else 1 if t >= 1 else 1 - math.pow(1 - t, 3)


QUESTION = "prepared statement errors behind pgbouncer. again."


def scene_question(t: float):
    """The question types itself. Nothing else on screen."""
    base, ink, d = frame()
    f = font(29)
    shown = int(len(QUESTION) * min(1.0, t / 1.6))
    text = QUESTION[:shown]
    x, y = 104, H / 2 - 60
    d.text((x, y), "$", font=f, fill=AMBER)
    d.text((x + 40, y), text, font=f, fill=PH_HI)
    if t % 0.8 < 0.5:
        cx = x + 40 + d.textlength(text, font=f)
        d.rectangle([cx + 3, y + 4, cx + 16, y + 36], fill=PH)
    if t > 1.9:
        centred(d, y + 90, "asked of the same agent, twice", font(20), FAINT)
    return glow(base, ink)


def column(d, x, w, title, colour, lines, reveal: float, body_font, title_font):
    d.text((x, 150), title, font=title_font, fill=colour)
    d.line([(x, 186), (x + w, 186)], fill=colour, width=2)
    y = 224
    for i, (line, fill, size) in enumerate(lines):
        if reveal < i * 0.12:
            break
        f = body_font if size == "body" else font(20)
        for chunk in wrap(d, line, f, w):
            d.text((x, y), chunk, font=f, fill=fill)
            y += f.size + 12
        y += 10


def wrap(d, text, f, width):
    words, line, out = text.split(), "", []
    for word in words:
        probe = f"{line} {word}".strip()
        if d.textlength(probe, font=f) <= width:
            line = probe
        else:
            out.append(line)
            line = word
    if line:
        out.append(line)
    return out


def scene_split(t: float):
    """Both answers, side by side. The left one is the whole argument."""
    base, ink, d = frame()
    body = font(22)
    title = font(22)
    left_x, right_x, colw = 96, 640, 464

    fade = ease(min(1.0, t / 0.5))
    column(d, left_x, colw, "WITHOUT MEMORY", COLD, [
        ("“No record of it.", COLD, "body"),
        ("First time here as far as I can see.”", COLD, "body"),
        ("then: five drivers, five knobs,", (78, 92, 104), "small"),
        ("and a question back to you.", (78, 92, 104), "small"),
    ], reveal=fade * 1.5, body_font=body, title_font=title)

    if t > 1.0:
        r = ease(min(1.0, (t - 1.0) / 0.7))
        column(d, right_x, colw, "WITH DEJA", PH, [
            ("“Yes. Nov 29 2025, payments repo.", PH_HI, "body"),
            ("Same bug.”", PH_HI, "body"),
            ("the agent called deja on its own,", FAINT, "small"),
            ("before debugging anything.", FAINT, "small"),
        ], reveal=r * 1.5, body_font=body, title_font=title)
    return glow(base, ink)


def scene_decision(t: float):
    """What was handed back: not the symptom, the decision."""
    base, ink, d = frame()
    centred(d, 118, "what came back", font(20), FAINT)
    lines = [
        ("pgx v5.5 changed prepared-statement caching.", BODY, 26),
        ("pgbouncer cannot hold those across connections.", BODY, 26),
        ("", BODY, 12),
        ("we pinned pgx 5.4.3", PH_HI, 34),
        ("revisit when pgbouncer 1.24 ships support", PH, 26),
    ]
    y = 190
    for i, (text, fill, size) in enumerate(lines):
        if t < 0.25 + i * 0.28:
            break
        if text:
            centred(d, y, text, font(size), fill)
        y += size + 26
    if t > 1.9:
        centred(d, 560, "a decision from eight months ago · reused, not re-derived", font(20), AMBER)
    return glow(base, ink)


def scene_cta(t: float):
    base, ink, d = frame()
    a = ease(min(1.0, t / 0.6))
    if a > 0:
        centred(d, 176, "Your agents already solved this.", font(38), BODY)
        centred(d, 228, "deja finds it.", font(38), PH_HI)
    if t > 0.7:
        centred(d, 330, "seventeen coding agents  ·  84.9% hit@1  ·  no LLM, no embeddings", font(20), FAINT)
        centred(d, 366, "one binary  ·  nothing leaves your machine", font(20), FAINT)
    if t > 1.1:
        cmd = "brew install vshulcz/tap/deja-vu"
        f = font(26)
        w = d.textlength(cmd, font=f)
        d.rounded_rectangle([(W - w) / 2 - 28, 438, (W + w) / 2 + 28, 500], radius=10,
                            outline=PH_DIM, width=2)
        d.text(((W - w) / 2, 452), cmd, font=f, fill=PH_HI)
        centred(d, 534, "github.com/vshulcz/deja-vu", font(20), FAINT)
    return glow(base, ink)


SCENES = [
    (scene_question, 3.0),
    (scene_split, 4.4),
    (scene_decision, 4.2),
    (scene_cta, 3.6),
]


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--out", default="frames")
    a = p.parse_args()
    os.makedirs(a.out, exist_ok=True)
    screen = scanlines_and_vignette()

    n = 0
    for render, seconds in SCENES:
        for i in range(int(seconds * FPS)):
            img = render(i / FPS)
            img = Image.alpha_composite(img, screen)
            # A short cross-fade out of every scene, so cuts do not snap.
            left = seconds - i / FPS
            if left < 0.3:
                img = Image.blend(Image.new("RGBA", (W, H), BG + (255,)), img, max(0.0, left / 0.3))
            img.convert("RGB").save(f"{a.out}/{n:04d}.png")
            n += 1
    print(f"{n} frames -> {a.out}")


if __name__ == "__main__":
    main()
