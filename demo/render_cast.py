#!/usr/bin/env python3
"""Render an asciinema v2 .cast recording to PNG frames + ffmpeg concat list.

Usage: render_cast.py SHOW.cast OUTDIR [--fps 12] [--font-size 16]
Then:  ffmpeg -f concat -safe 0 -i OUTDIR/concat.txt -c:v libx264 -pix_fmt yuv420p -movflags +faststart show.mp4

Only stdlib + pyte + Pillow. Handles SGR colors/bold; ignores exotic sequences.
"""
import json
import os
import sys

import pyte
from PIL import Image, ImageDraw, ImageFont

PALETTE = {
    "black": (0, 0, 0),
    "red": (205, 49, 49),
    "green": (13, 188, 121),
    "brown": (229, 229, 16),  # xterm yellow
    "blue": (36, 114, 200),
    "magenta": (188, 63, 188),
    "cyan": (17, 168, 205),
    "white": (229, 229, 229),
    "default": (229, 229, 229),
    "bg_default": (12, 12, 12),
}


def color(name, is_bg):
    if name in (None, "default"):
        return PALETTE["bg_default"] if is_bg else PALETTE["default"]
    base = name[6:] if name.startswith("bright") else name
    c = PALETTE.get(base, PALETTE["default"] if not is_bg else PALETTE["bg_default"])
    if name.startswith("bright"):
        c = tuple(min(255, v + 70) for v in c)
    return c


def main():
    cast_path, outdir = sys.argv[1], sys.argv[2]
    fps = float(sys.argv[3]) if len(sys.argv) > 3 else 12.0
    font_size = int(sys.argv[4]) if len(sys.argv) > 4 else 16
    os.makedirs(outdir, exist_ok=True)

    with open(cast_path) as f:
        header = json.loads(f.readline())
        events = [(float(d[0]), d[1], d[2]) for d in
                  (json.loads(l) for l in f if l.strip()) if d[1] == "o"]

    width = header.get("width", 80)
    height = header.get("height", 24)
    font = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf", font_size)
    bold = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf", font_size)
    bbox = font.getbbox("M")
    cw, lh = bbox[2] - bbox[0], bbox[3] - bbox[1] + 4
    pad = 12

    screen = pyte.Screen(width, height)
    stream = pyte.Stream(screen)

    frames = []  # (png_path, duration)
    last_t = 0.0

    def snapshot():
        img = Image.new("RGB", (width * cw + pad * 2, height * lh + pad * 2), PALETTE["bg_default"])
        d = ImageDraw.Draw(img)
        for y in range(height):
            line = screen.buffer[y]
            for x, ch in line.items():
                fg = color(ch.fg, False)
                if ch.bold:
                    fg = tuple(min(255, v + 60) for v in fg)
                bg = color(ch.bg, True)
                if bg != PALETTE["bg_default"]:
                    d.rectangle([pad + x * cw, pad + y * lh,
                                 pad + (x + 1) * cw, pad + (y + 1) * lh], fill=bg)
                if ch.data not in ("", " "):
                    d.text((pad + x * cw, pad + y * lh),
                           ch.data, font=bold if ch.bold or ch.reverse else font, fill=fg)
        # cursor (hidden flag means do-not-draw on this pyte version)
        try:
            cur_hidden = bool(screen.cursor.hidden)
        except AttributeError:
            cur_hidden = False
        if not cur_hidden:
            cx, cy = screen.cursor.x, screen.cursor.y
            if 0 <= cx < width and 0 <= cy < height:
                d.rectangle([pad + cx * cw, pad + cy * lh,
                             pad + (cx + 1) * cw, pad + (cy + 1) * lh], outline=(150, 150, 150))
        return img

    pending_img = None
    pending_since = 0.0
    idx = 0

    def flush(t):
        nonlocal idx, pending_img, pending_since, last_t
        if pending_img is None:
            return
        dur = max(t - pending_since, 1.0 / fps)
        p = os.path.join(outdir, "frame_%04d.png" % idx)
        pending_img.save(p)
        frames.append((p, dur))
        idx += 1
        pending_img = None
        last_t = t

    cur_img = snapshot()
    for t, _typ, data in events:
        stream.feed(data)
        if t - last_t >= 1.0 / fps:
            flush(t)
            pending_img = snapshot()
            pending_since = t
            last_t = t
    flush(events[-1][0] + 1.5 if events else 1.5)

    with open(os.path.join(outdir, "concat.txt"), "w") as f:
        for p, dur in frames:
            f.write("file '%s'\nduration %.3f\n" % (os.path.abspath(p), min(dur, 8.0)))
        # concat demuxer needs the last file repeated (no duration)
        f.write("file '%s'\n" % os.path.abspath(frames[-1][0]))
    print("wrote %d frames to %s" % (len(frames), outdir))


if __name__ == "__main__":
    main()
