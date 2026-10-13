#!/usr/bin/env python3
"""Render SWF DefineShape / DefineSprite (frame 0) to SVG."""
import sys, os, struct, json
sys.path.insert(0, os.path.dirname(__file__))
from swfx import load
from swftree import collect, Bits, read_matrix


def rgb(b, alpha):
    if alpha:
        r, g, bb, a = b.ub(8), b.ub(8), b.ub(8), b.ub(8)
        return (r, g, bb, a / 255)
    return (b.ub(8), b.ub(8), b.ub(8), 1.0)


def read_rect(b):
    n = b.ub(5)
    r = [b.sb(n) for _ in range(4)]
    b.align()
    return r  # xmin xmax ymin ymax


def read_gradient(b, alpha, focal):
    b.ub(2); b.ub(2); n = b.ub(4)
    stops = []
    for _ in range(n):
        ratio = b.ub(8)
        stops.append((ratio, rgb(b, alpha)))
    if focal:
        b.ub(16)
    return stops


def read_fills(b, ver):
    n = b.ub(8)
    if n == 0xff and ver >= 2:
        lo = b.ub(8); hi = b.ub(8); n = lo | hi << 8
    out = []
    for _ in range(n):
        t = b.ub(8)
        if t == 0:
            out.append(('solid', rgb(b, ver >= 3)))
        elif t in (0x10, 0x12, 0x13):
            m = read_matrix(b)
            g = read_gradient(b, ver >= 3, t == 0x13)
            out.append(('linear' if t == 0x10 else 'radial', m, g))
        elif t in (0x40, 0x41, 0x42, 0x43):
            lo = b.ub(8); hi = b.ub(8)
            m = read_matrix(b)
            out.append(('bitmap', lo | hi << 8, m))
        else:
            raise ValueError('fill type %x' % t)
    return out


def read_lines(b, ver):
    n = b.ub(8)
    if n == 0xff:
        lo = b.ub(8); hi = b.ub(8); n = lo | hi << 8
    out = []
    for _ in range(n):
        lo = b.ub(8); hi = b.ub(8); w = lo | hi << 8
        if ver == 4:
            b.ub(2); join = b.ub(2); hasfill = b.ub(1); b.ub(1); b.ub(1); b.ub(1); b.ub(5); b.ub(1); b.ub(2)
            if join == 2:
                b.ub(16)
            if hasfill:
                f = read_fills_one(b, ver)
                col = f[1] if f[0] == 'solid' else (f[2][0][1] if f[0] in ('linear', 'radial') else (0, 0, 0, 1))
            else:
                col = rgb(b, True)
        else:
            col = rgb(b, ver >= 3)
        out.append((w, col))
    return out


def read_fills_one(b, ver):
    t = b.ub(8)
    if t == 0:
        return ('solid', rgb(b, True))
    if t in (0x10, 0x12, 0x13):
        m = read_matrix(b); g = read_gradient(b, True, t == 0x13)
        return ('linear' if t == 0x10 else 'radial', m, g)
    lo = b.ub(8); hi = b.ub(8); m = read_matrix(b)
    return ('bitmap', lo | hi << 8, m)


def parse_shape(data, code):
    ver = {2: 1, 22: 2, 32: 3, 83: 4}[code]
    b = Bits(data, 2)
    bounds = read_rect(b)
    if ver == 4:
        read_rect(b); b.ub(8)
    fills = read_fills(b, ver)
    lines = read_lines(b, ver)
    nfb = b.ub(4); nlb = b.ub(4)
    x = y = 0
    f0 = f1 = ln = 0
    fofs = 0; lofs = 0
    allfills = list(fills); alllines = list(lines)
    edges = []  # (f0, f1, line, kind, pts)
    while True:
        if b.ub(1) == 0:
            flags = b.ub(5)
            if flags == 0:
                break
            newst, sl, sf1, sf0, mv = (flags >> 4) & 1, (flags >> 3) & 1, (flags >> 2) & 1, (flags >> 1) & 1, flags & 1
            if mv:
                n = b.ub(5); x = b.sb(n); y = b.sb(n)
            if sf0:
                v = b.ub(nfb); f0 = v + fofs if v else 0
            if sf1:
                v = b.ub(nfb); f1 = v + fofs if v else 0
            if sl:
                v = b.ub(nlb); ln = v + lofs if v else 0
            if newst:
                b.align()
                nf = read_fills(b, ver); nl = read_lines(b, ver)
                fofs = len(allfills); lofs = len(alllines)
                allfills += nf; alllines += nl
                nfb = b.ub(4); nlb = b.ub(4)
                # style indices are relative to new arrays
                f0 = f1 = ln = 0
        else:
            straight = b.ub(1)
            n = b.ub(4) + 2
            if straight:
                if b.ub(1):
                    dx = b.sb(n); dy = b.sb(n)
                else:
                    if b.ub(1):
                        dx = 0; dy = b.sb(n)
                    else:
                        dx = b.sb(n); dy = 0
                edges.append((f0, f1, ln, 'L', [(x, y), (x + dx, y + dy)]))
                x += dx; y += dy
            else:
                cx = x + b.sb(n); cy = y + b.sb(n)
                ax = cx + b.sb(n); ay = cy + b.sb(n)
                edges.append((f0, f1, ln, 'Q', [(x, y), (cx, cy), (ax, ay)]))
                x, y = ax, ay
    return dict(bounds=bounds, fills=allfills, lines=alllines, edges=edges)


def chain(segs):
    """Join directed segments into contours (list of segment lists)."""
    from collections import defaultdict
    by_start = defaultdict(list)
    for i, s in enumerate(segs):
        by_start[s[1][0]].append(i)
    used = [False] * len(segs)
    contours = []
    for i in range(len(segs)):
        if used[i]:
            continue
        used[i] = True
        cur = [segs[i]]
        start = segs[i][1][0]
        end = segs[i][1][-1]
        while end != start:
            nxt = None
            for j in by_start.get(end, []):
                if not used[j]:
                    nxt = j; break
            if nxt is None:
                break
            used[nxt] = True
            cur.append(segs[nxt])
            end = segs[nxt][1][-1]
        contours.append(cur)
    return contours


def seg_d(contour, close=True):
    d = []
    p0 = contour[0][1][0]
    d.append('M%g %g' % (p0[0] / 20, p0[1] / 20))
    for kind, pts in contour:
        if kind == 'L':
            d.append('L%g %g' % (pts[1][0] / 20, pts[1][1] / 20))
        else:
            d.append('Q%g %g %g %g' % (pts[1][0] / 20, pts[1][1] / 20, pts[2][0] / 20, pts[2][1] / 20))
    if close and contour[-1][1][-1] == p0:
        d.append('Z')
    return ''.join(d)


def color_css(c):
    r, g, b, a = c
    return '#%02x%02x%02x' % (r, g, b), a


GRAD_ID = [0]


def shape_svg(sh, defs, transform=''):
    out = []
    for fi in range(1, len(sh['fills']) + 1):
        segs = []
        for f0, f1, ln, kind, pts in sh['edges']:
            if f1 == fi:
                segs.append((kind, pts))
            if f0 == fi:
                segs.append((kind, list(reversed(pts))))
        if not segs:
            continue
        d = ''.join(seg_d(c) for c in chain(segs))
        f = sh['fills'][fi - 1]
        if f[0] == 'solid':
            col, a = color_css(f[1])
            attr = 'fill="%s"%s' % (col, '' if a >= 1 else ' fill-opacity="%.3f"' % a)
        elif f[0] in ('linear', 'radial'):
            GRAD_ID[0] += 1
            gid = 'g%d' % GRAD_ID[0]
            m = f[1]
            mt = 'matrix(%g %g %g %g %g %g)' % (m['a'], m['b'], m['c'], m['d'], m['tx'] / 20, m['ty'] / 20)
            stops = ''.join('<stop offset="%.3f" stop-color="%s" stop-opacity="%.3f"/>' % (
                r / 255, color_css(c)[0], c[3]) for r, c in f[2])
            if f[0] == 'linear':
                defs.append('<linearGradient id="%s" gradientUnits="userSpaceOnUse" x1="-819.2" x2="819.2" y1="0" y2="0" gradientTransform="%s">%s</linearGradient>' % (gid, mt, stops))
            else:
                defs.append('<radialGradient id="%s" gradientUnits="userSpaceOnUse" cx="0" cy="0" r="819.2" gradientTransform="%s">%s</radialGradient>' % (gid, mt, stops))
            attr = 'fill="url(#%s)"' % gid
        else:
            attr = 'fill="#888" fill-opacity="0.3"'
        out.append('<path %s fill-rule="evenodd" d="%s"/>' % (attr, d))
    for li in range(1, len(sh['lines']) + 1):
        segs = [(kind, pts) for f0, f1, ln, kind, pts in sh['edges'] if ln == li]
        if not segs:
            continue
        w, c = sh['lines'][li - 1]
        col, a = color_css(c)
        d = ''.join(seg_d(cn, close=False) for cn in chain(segs))
        out.append('<path fill="none" stroke="%s" stroke-opacity="%.3f" stroke-width="%g" stroke-linejoin="round" stroke-linecap="round" d="%s"/>' % (
            col, a, max(w / 20, 0.5), d))
    return out


def mtx(m):
    if not m:
        return ''
    return 'matrix(%g %g %g %g %g %g)' % (m['a'], m['b'], m['c'], m['d'], m['tx'] / 20, m['ty'] / 20)


def render_char(cid, chars, sprites, defs, depth=0, frame=0):
    t, data = chars.get(cid, (None, None))
    if t is None:
        return ''
    if t.startswith('DefineShape'):
        code = {'DefineShape': 2, 'DefineShape2': 22, 'DefineShape3': 32, 'DefineShape4': 83}[t]
        sh = parse_shape(data, code)
        return '<g data-char="%d">%s</g>' % (cid, ''.join(shape_svg(sh, defs)))
    if t == 'DefineSprite' and depth < 8:
        frames = sprites[cid]
        display = {}
        for fi in range(0, min(frame, len(frames) - 1) + 1):
            for kind, v in frames[fi]:
                if kind == 'place':
                    d = v['depth']
                    cur = display.get(d, {}) if v['move'] else {}
                    cur = dict(cur); cur.update({k: v[k] for k in v if k != 'move'})
                    display[d] = cur
                elif kind == 'remove':
                    display.pop(v, None)
        parts = []
        for d in sorted(display):
            v = display[d]
            if 'char' not in v:
                continue
            inner = render_char(v['char'], chars, sprites, defs, depth + 1)
            nm = v.get('name', '')
            op = ''
            if 'cx' in v and 'mul' in v['cx']:
                op = ' opacity="%.3f"' % max(0, min(1, v['cx']['mul'][3] / 256))
            parts.append('<g%s%s transform="%s">%s</g>' % (' id="%s"' % nm if nm else '', op, mtx(v.get('matrix')), inner))
        return ''.join(parts)
    return ''


def main(path, cid, out, frame=0):
    ver, buf, pos = load(path)
    chars, sprites, root = collect(buf, pos)
    defs = []
    body = render_char(cid, chars, sprites, defs, 0, frame)
    svg = ('<svg xmlns="http://www.w3.org/2000/svg" viewBox="-2000 -1000 4000 2000" width="2000" height="1000">'
           '<defs>%s</defs>%s</svg>' % (''.join(defs), body))
    open(out, 'w').write(svg)
    print(out, len(svg))


if __name__ == '__main__':
    main(sys.argv[1], int(sys.argv[2]), sys.argv[3], int(sys.argv[4]) if len(sys.argv) > 4 else 0)
