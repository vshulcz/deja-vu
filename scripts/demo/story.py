#!/usr/bin/env python3
"""Render the README demo as a designed sequence rather than a screen capture.

A terminal recording shows everything at once in six-point type. It is honest
and it is unreadable, and a reader gives a GIF about four seconds before
deciding. This draws the same story — the same sentences, quoted from the two
real sessions in scripts/demo/agent.tape and agent-plain.tape — at a size
someone can actually read, in the brand palette: the coat colour on near-black,
amber for the one thing a reader should recognise, scanlines, a vignette.

Four scenes, sixteen seconds:

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

W, H = 1200, 560
FPS = 20

# The three colours the banner and the SVGs use, plus the greys between them.
# 103, 208 and 234 in the xterm palette; the same numbers are in cmd/deja/cat.go.
BG = (11, 15, 16)
PH = (135, 135, 175)        # coat: the colour the mark is drawn in
PH_HI = (244, 247, 247)     # what the reader is meant to read
PH_DIM = (94, 94, 124)      # the same coat, further back
AMBER = (255, 135, 0)       # recognition, and nothing else
BODY = (169, 178, 180)
FAINT = (85, 98, 106)
COLD = (96, 116, 132)
FEATURE = (28, 28, 28)      # the cat's eyes

MONO = "/System/Library/Fonts/Menlo.ttc"



# The mark, drawn from the same 24x22 grid as everything else. A literal
# because this script is Python and internal/mark is Go — but no longer checked
# by eye: internal/mark/demo_sprite_test.go compares these rows against the
# sprite, and found the tail here three rows short of the real one.
CAT_BODY = [
    "....#............#......",
    "...###..........###.....",
    "...####........####.....",
    "...#####......#####.....",
    "...################.....",
    "..##################....",
    "..##################....",
    "..###oo########oo###....",
    "..###oo########oo###....",
    "..###oo########oo###....",
    "..##################....",
    "..########nn########....",
    "..##################....",
    "...################.....",
    "....##############.##...",
    ".....############..##...",
    ".....############..##...",
    ".....############..##...",
    ".....############..##...",
    "....################....",
    "....################....",
    ".....####....####.......",
]


# The eye rows, in the three states an eyelid passes through. Rows 7-9 of the
# sprite; a blink that cuts from open to shut skips the part that makes it read
# as an eyelid rather than as a light switch.
EYES = {
    "open": ["..###oo########oo###....",
             "..###oo########oo###....",
             "..###oo########oo###...."],
    "low":  ["..##################....",
             "..###oo########oo###....",
             "..###oo########oo###...."],
    "shut": ["..##################....",
             "..##################....",
             "..##oooo######oooo##...."],
}


def between(stops: list[tuple[float, float]], u: float) -> float:
    """Read a value off a list of (position, value) stops, eased between them."""
    for i in range(len(stops) - 1):
        (p0, v0), (p1, v1) = stops[i], stops[i + 1]
        if p0 <= u <= p1:
            k = 0.0 if p1 == p0 else (u - p0) / (p1 - p0)
            return v0 + (v1 - v0) * (k * k * (3 - 2 * k))
    return stops[-1][1]


# What the body does over one breath and over one perk, as height multipliers.
# The perk squashes before it stretches and rocks to a stop afterwards: the dip
# before a movement is what makes it read as intent rather than as a jolt, and
# the shrinking bounces after are what make it read as weight.
BREATH = [(0.0, 1.0), (0.40, 1.045), (0.56, 1.045), (1.0, 1.0)]
PERK = [(0.0, 1.0), (0.86, 1.0), (0.884, 0.945), (0.908, 1.068),
        (0.932, 0.975), (0.954, 1.014), (0.974, 1.0), (1.0, 1.0)]


def cat_state(t: float) -> tuple[float, str]:
    """How the cat sits at film time t: how tall it is, and what its eyes do.

    Height carries the whole of it. Squash and stretch keep the volume, so the
    width follows from the height — a body that grows taller without growing
    narrower is inflating, not breathing. Earlier versions slid a slab of the
    sprite up and down instead, which took the paws off the ground in one and
    stretched the face away from the nose in the next: a slice of a body moving
    is a cut, not a deformation, and it reads as one.

    The cycles do not divide each other, so the combination never repeats."""
    tall = between(BREATH, (t % 4.9) / 4.9) * between(PERK, (t % 18.7) / 18.7)
    eyes = "open"
    # One blink, lid down and back up, a fifth of a second shut. At twenty frames
    # a second the first version's ninety-millisecond steps lasted under two
    # frames, and it came in pairs, which reads as a flutter rather than a blink.
    for at in (3.1, 9.6):
        if at <= t < at + 0.15 or at + 0.35 <= t < at + 0.5:
            eyes = "low"
        elif at + 0.15 <= t < at + 0.35:
            eyes = "shut"
    return tall, eyes


def draw_cat(d, x, y, px=4, t=0.0):
    """One rectangle a pixel. At px=4 the sprite is 96x88, which is the size it
    is shown at on the site.

    Every edge is rounded to a whole screen pixel, so a cat halfway through a
    breath is still made of square pixels rather than of blurred ones. The film
    has no transforms to lean on the way the SVG mark does; this is the same
    deformation done by hand."""
    tall, eyes = cat_state(t)
    wide = 1 / tall
    rows = list(CAT_BODY)
    rows[7:10] = EYES[eyes]
    feet, middle = y + len(rows) * px, x + len(rows[0]) * px / 2
    for r, row in enumerate(rows):
        top = feet + round((r - len(rows)) * px * tall)
        bottom = feet + round((r + 1 - len(rows)) * px * tall) - 1
        for c, ch in enumerate(row):
            fill = {"#": PH, "n": AMBER, "o": FEATURE}.get(ch)
            if fill:
                left = middle + round((c - len(row) / 2) * px * wide)
                right = middle + round((c + 1 - len(row) / 2) * px * wide) - 1
                d.rectangle([left, top, right, bottom], fill=fill)


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


# Where we are in the whole film, not in the current scene. The breath has to
# carry across a cut: a creature that restarts its breathing every scene is back
# to being a drawing.
FILM_T = 0.0


def frame(t: float = None) -> tuple[Image.Image, Image.Image, ImageDraw.ImageDraw]:
    base = Image.new("RGBA", (W, H), BG + (255,))
    ink = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    d = ImageDraw.Draw(ink)
    # The wordmark sits on every frame: a demo people screenshot should say
    # whose it is without a caption.
    draw_cat(d, 52, 30, px=2, t=FILM_T if t is None else t)
    d.text((110, 44), "deja", font=font(22), fill=PH_HI)
    d.text((164, 44), "-vu", font=font(22), fill=PH_DIM)
    return base, ink, d


def ease(t: float) -> float:
    return 0 if t <= 0 else 1 if t >= 1 else 1 - math.pow(1 - t, 3)


QUESTION = "prepared statement errors behind pgbouncer. again."


def must_fit(d, text, f, x, margin=40):
    """A line that runs off the canvas is invisible until someone looks at a
    frame, and raising a type size without re-checking the width has now done it
    three times. This turns that into a crash."""
    end = x + d.textlength(text, font=f)
    if end > W - margin:
        raise SystemExit(f"{text[:40]!r} ends at {end:.0f} of {W}")


def scene_question(t: float):
    """The question types itself. Nothing else on screen — so it has to be big
    enough to be the screen, or the frame reads as empty."""
    base, ink, d = frame()
    f = font(31)
    shown = int(len(QUESTION) * min(1.0, t / 1.6))
    text = QUESTION[:shown]
    x, y = 104, H / 2 - 96
    d.text((x, y), "$", font=f, fill=AMBER)
    must_fit(d, QUESTION, f, x + 56)
    d.text((x + 56, y), text, font=f, fill=PH_HI)
    if t % 0.8 < 0.5:
        cx = x + 56 + d.textlength(text, font=f)
        d.rectangle([cx + 4, y + 6, cx + 22, y + 52], fill=PH)
    if t > 1.4:
        centred(d, y + 120, "asked of the same agent, twice", font(24), FAINT)
    return glow(base, ink)


def dim(colour, a: float):
    """Fade towards the background rather than using alpha: the glow pass reads
    the ink layer, and half-transparent ink blooms wrong."""
    a = max(0.0, min(1.0, a))
    return tuple(int(BG[i] + (colour[i] - BG[i]) * a) for i in range(3))


def column(d, x, w, title, colour, lines, t: float, start: float, body_font):
    """One side of the split. Lines arrive one at a time — the pause between
    them is the point, not decoration: a reader needs a beat to notice that the
    agent has just admitted it knows nothing."""
    head = ease((t - start) / 0.35)
    if head <= 0:
        return
    d.text((x, 168), title, font=font(21), fill=dim(colour, head))
    d.line([(x, 204), (x + w * head, 204)], fill=dim(colour, head), width=2)
    y = 242
    for i, (line, fill, size, accent) in enumerate(lines):
        a = ease((t - start - 0.45 - i * 0.42) / 0.4)
        if a <= 0:
            break
        f = body_font if size == "body" else font(19)
        for chunk in wrap(d, line, f, w):
            cx = x
            # Dates and versions are what a developer's eye lands on, so they
            # get the amber the site keeps for exactly that.
            for piece, is_accent in split_accent(chunk, accent):
                col = AMBER if is_accent else fill
                d.text((cx, y), piece, font=f, fill=dim(col, a))
                cx += d.textlength(piece, font=f)
            y += f.size + 12
        y += 8


def split_accent(text: str, accent: str):
    if not accent or accent not in text:
        return [(text, False)]
    head, _, tail = text.partition(accent)
    return [(head, False), (accent, True), (tail, False)]


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
    """Both answers, side by side — but not at the same time. The left one is
    given room to land before the right one arrives to contradict it."""
    base, ink, d = frame()
    body = font(21)
    left_x, right_x, colw = 92, 636, 472

    column(d, left_x, colw, "WITHOUT MEMORY", COLD, [
        ("\u201cNo record of it. First time here", COLD, "body", ""),
        ("as far as I can see.\u201d", COLD, "body", ""),
        ("then five drivers, five knobs,", (74, 88, 100), "small", ""),
        ("and a question back to you.", (74, 88, 100), "small", ""),
    ], t, start=0.0, body_font=body)

    # The divider sweeps down as the second column is about to answer.
    sweep = ease((t - 2.05) / 0.45)
    if sweep > 0:
        d.line([(586, 168), (586, 168 + 214 * sweep)], fill=dim(PH_DIM, 0.8), width=1)

    column(d, right_x, colw, "WITH DEJA", PH, [
        ("\u201cYes. Nov 29 2025, payments repo.", PH_HI, "body", "Nov 29 2025"),
        ("Same bug.\u201d", PH_HI, "body", ""),
        # Measured end to end on the demo corpus, process start included —
        # not the 12 ms search figure from the benchmarks, which would be the
        # flattering number rather than the true one for this call.
        ("\u2192 deja/recall  \u00b7  20 ms  \u00b7  no LLM", FAINT, "small", "20 ms"),
        ("called by the agent, unprompted.", FAINT, "small", ""),
    ], t, start=2.2, body_font=body)
    return glow(base, ink)


def scene_decision(t: float):
    """What was handed back: not the symptom, the decision."""
    base, ink, d = frame()
    centred(d, 96, "what deja handed back", font(20), FAINT)
    lines = [
        ("pgx v5.5 changed prepared-statement caching.", BODY, 26),
        ("pgbouncer cannot hold those across connections.", BODY, 26),
        ("", BODY, 12),
        ("we pinned pgx 5.4.3", AMBER, 34),
        ("revisit when pgbouncer 1.24 ships support", PH, 26),
    ]
    y = 162
    for i, (text, fill, size) in enumerate(lines):
        if t < 0.25 + i * 0.28:
            break
        if text:
            centred(d, y, text, font(size), fill)
        y += size + 26
    if t > 1.9:
        centred(d, 452, "a decision from eight months ago · reused, not re-derived", font(20), AMBER)
    return glow(base, ink)


def scene_cta(t: float):
    base, ink, d = frame()
    a = ease(min(1.0, t / 0.6))
    if a > 0:
        centred(d, 136, "Your agents already solved this.", font(38), BODY)
        centred(d, 188, "deja finds it.", font(38), PH_HI)
    if t > 0.7:
        # No harness count here. Nothing rebuilds this GIF and no test reads it,
        # so a number that moves every release would go quietly wrong. What is
        # left is architecture, which does not.
        centred(d, 274, "one binary  ·  no LLM  ·  no embeddings", font(20), FAINT)
        centred(d, 310, "nothing leaves your machine", font(20), FAINT)
    if t > 1.1:
        cmd = "brew install vshulcz/tap/deja-vu"
        f = font(26)
        w = d.textlength(cmd, font=f)
        d.rounded_rectangle([(W - w) / 2 - 28, 368, (W + w) / 2 + 28, 430], radius=10,
                            outline=PH_DIM, width=2)
        d.text(((W - w) / 2, 382), cmd, font=f, fill=PH_HI)
        centred(d, 466, "github.com/vshulcz/deja-vu", font(20), FAINT)
    return glow(base, ink)


SCENES = [
    (scene_question, 2.6),
    (scene_split, 5.6),
    (scene_decision, 4.2),
    (scene_cta, 3.6),
]


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--out", default="frames")
    a = p.parse_args()
    os.makedirs(a.out, exist_ok=True)
    screen = scanlines_and_vignette()

    global FILM_T
    n = 0
    for render, seconds in SCENES:
        for i in range(int(seconds * FPS)):
            FILM_T = n / FPS
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
