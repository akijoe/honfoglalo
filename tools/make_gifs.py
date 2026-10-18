#!/usr/bin/env python3
"""Az oldal GIF-jei (Pillow kell hozzá):

  img/logo.gif, img/logo-shine.gif          a logó (img/logo.png) álló képe, és rámutatáskor egy
                                            balról jobbra végigfutó, halvány csillanással (egyszer fut le)
  img/online.gif                            a "játékos online" előtti pulzáló zöld fény

A GIF-nek nincs félig átlátszó pixele, ezért a sima széleket a háttér színére keverjük. Ez a bal és a
jobb sáv tetején egyszínű; a telefonos (behúzható) sávoké más, nekik a "-m" változat készül
(img/logo-m.gif, img/logo-shine-m.gif, img/online-m.gif; js/hf.js és index.html választ).

Futtatás: python3 tools/make_gifs.py
"""
import math
import os

from PIL import Image

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
IMG = os.path.join(ROOT, 'img')

# a sávok háttere a logó és a fény helyén: asztali gépen az img/bg.png teteje (61, 19, 0) a sáv
# 25%-os sötétítésével; a behúzható sávok háttere rgba(28, 15, 6, .97) (index.html)
MATTES = {'': (46, 14, 0), '-m': (29, 15, 6)}
CUT = 0.18          # ennél átlátszóbb pixel átlátszó marad (a többi a háttérre keverve)


def matte_frame(rgba, matte):
    """RGBA kép -> (RGB kép a háttérre keverve, átlátszósági maszk)"""
    w, h = rgba.size
    src = rgba.load()
    out = Image.new('RGB', (w, h), matte)
    mask = Image.new('L', (w, h), 0)
    o = out.load()
    m = mask.load()
    for y in range(h):
        for x in range(w):
            r, g, b, a = src[x, y]
            if a / 255 < CUT:
                continue
            k = a / 255
            o[x, y] = tuple(round(c * k + mc * (1 - k)) for c, mc in zip((r, g, b), matte))
            m[x, y] = 255
    return out, mask


def save_gif(path, frames, durations, loop):
    """Közös palettával (ne villogjon), a 255. szín az átlátszó."""
    w, h = frames[0][0].size
    sheet = Image.new('RGB', (w, h * len(frames)))
    for i, (rgb, _) in enumerate(frames):
        sheet.paste(rgb, (0, i * h))
    pal = sheet.quantize(colors=255, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE)
    out = []
    for rgb, mask in frames:
        q = rgb.quantize(palette=pal, dither=Image.Dither.NONE)
        q.paste(255, mask=mask.point(lambda v: 255 - v))
        out.append(q)
    kw = {'save_all': True, 'append_images': out[1:], 'duration': durations, 'transparency': 255,
          'disposal': 2, 'optimize': False}
    if loop is not None:
        kw['loop'] = loop
    out[0].save(path, **kw)
    print('  %-24s %2d kocka, %5d bájt' % (os.path.relpath(path, ROOT), len(out), os.path.getsize(path)))


def shine(logo, pos, half=13, peak=0.42, slant=0.55):
    """A logó betűin egy ferde, puha fénycsík (a középe pos-nál)."""
    w, h = logo.size
    img = logo.copy()
    px = img.load()
    for y in range(h):
        for x in range(w):
            r, g, b, a = px[x, y]
            if a == 0:
                continue
            d = abs(x + (y - h / 2) * slant - pos)
            if d >= half:
                continue
            k = peak * math.cos(math.pi / 2 * d / half) ** 2
            px[x, y] = (round(r + (255 - r) * k), round(g + (255 - g) * k), round(b + (255 - b) * k), a)
    return img


def make_logo():
    logo = Image.open(os.path.join(IMG, 'logo.png')).convert('RGBA')
    w = logo.width
    steps = 18
    for suffix, matte in MATTES.items():
        still = matte_frame(logo, matte)
        save_gif(os.path.join(IMG, 'logo%s.gif' % suffix), [still], [1000], None)
        frames = [still]
        for i in range(steps):
            pos = -20 + (w + 40) * i / (steps - 1)
            frames.append(matte_frame(shine(logo, pos), matte))
        frames.append(still)
        # egyszer fut le (nincs ismétlés): js/hf.js minden rámutatáskor újraindítja
        save_gif(os.path.join(IMG, 'logo-shine%s.gif' % suffix), frames, [40] + [36] * steps + [1000], None)


def make_online():
    size = 14
    c = (size - 1) / 2
    steps = 16
    for suffix, matte in MATTES.items():
        frames = []
        for i in range(steps):
            p = (1 - math.cos(2 * math.pi * i / steps)) / 2      # 0 -> 1 -> 0
            img = Image.new('RGBA', (size, size), (0, 0, 0, 0))
            px = img.load()
            core = 3.1 + 0.5 * p
            for y in range(size):
                for x in range(size):
                    d = math.hypot(x - c, y - c)
                    # a fény: középen világos zöld, a széle felé sötétebb; körülötte a pulzáló dereng
                    if d <= core + 0.5:
                        k = min(1.0, core + 0.5 - d)
                        t = d / (core + 0.5)
                        lo, hi = (62, 160, 40), (150, 245, 110)
                        base = tuple(round(h_ * (1 - t) + l_ * t) for h_, l_ in zip(hi, lo))
                        base = tuple(round(v + (255 - v) * 0.25 * p * (1 - t)) for v in base)
                        glow = 0.55 * p
                        a = k + (1 - k) * glow
                        px[x, y] = base + (round(255 * a),)
                    elif d <= 6.6:
                        g = 0.6 * p * (1 - (d - core - 0.5) / (6.6 - core - 0.5)) ** 2
                        px[x, y] = (91, 190, 60, round(255 * g))
            frames.append(matte_frame(img, matte))
        save_gif(os.path.join(IMG, 'online%s.gif' % suffix), frames, [80] * steps, 0)


def main():
    make_logo()
    make_online()


if __name__ == '__main__':
    main()
