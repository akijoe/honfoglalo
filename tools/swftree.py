#!/usr/bin/env python3
"""Dump SWF display-list structure: sprites, their placed children (name, char id, matrix), frame labels."""
import sys, struct, os
sys.path.insert(0, os.path.dirname(__file__))
from swfx import load, iter_tags, TAGNAMES


class Bits:
    def __init__(self, data, pos=0):
        self.d = data; self.p = pos * 8

    def ub(self, n):
        v = 0
        for _ in range(n):
            v = (v << 1) | ((self.d[self.p >> 3] >> (7 - (self.p & 7))) & 1)
            self.p += 1
        return v

    def sb(self, n):
        v = self.ub(n)
        if n and v & (1 << (n - 1)):
            v -= 1 << n
        return v

    def align(self):
        self.p = (self.p + 7) & ~7

    @property
    def byte(self):
        return self.p >> 3


def read_matrix(b):
    m = dict(a=1.0, b=0.0, c=0.0, d=1.0, tx=0, ty=0)
    if b.ub(1):
        n = b.ub(5); m['a'] = b.sb(n) / 65536; m['d'] = b.sb(n) / 65536
    if b.ub(1):
        n = b.ub(5); m['b'] = b.sb(n) / 65536; m['c'] = b.sb(n) / 65536
    n = b.ub(5); m['tx'] = b.sb(n); m['ty'] = b.sb(n)
    b.align()
    return m


def parse_place2(data, code):
    flags = data[0]
    p = 1
    flags2 = 0
    if code == 70:
        flags2 = data[1]; p = 2
    depth = struct.unpack_from('<H', data, p)[0]; p += 2
    res = dict(depth=depth, move=bool(flags & 1))
    if code == 70 and (flags2 & 8 or (flags2 & 16)):
        # class name present
        if flags2 & 8 or (flags2 & 16 and flags & 2):
            e = data.index(b'\0', p); res['class'] = data[p:e].decode('utf-8', 'replace'); p = e + 1
    if flags & 2:
        res['char'] = struct.unpack_from('<H', data, p)[0]; p += 2
    if flags & 4:
        b = Bits(data, p); res['matrix'] = read_matrix(b); p = b.byte
    if flags & 8:
        b = Bits(data, p)
        hasadd = b.ub(1); hasmul = b.ub(1); n = b.ub(4)
        cx = {}
        if hasmul: cx['mul'] = [b.sb(n) for _ in range(4)]
        if hasadd: cx['add'] = [b.sb(n) for _ in range(4)]
        b.align(); p = b.byte; res['cx'] = cx
    if flags & 16:
        res['ratio'] = struct.unpack_from('<H', data, p)[0]; p += 2
    if flags & 32:
        e = data.index(b'\0', p); res['name'] = data[p:e].decode('utf-8', 'replace'); p = e + 1
    return res


def collect(buf, pos):
    chars = {}  # id -> (tagname, data)
    sprites = {}
    def scan(b, p, owner):
        frames = [[]]
        for code, data, _ in iter_tags(b, p):
            if code == 39:
                sid = struct.unpack_from('<H', data, 0)[0]
                sprites[sid] = scan(data, 4, sid)
                chars[sid] = ('DefineSprite', data)
            elif code in (2, 22, 32, 83, 6, 21, 35, 20, 36, 90, 10, 48, 75, 11, 33, 37, 7, 34, 46, 84, 14):
                cid = struct.unpack_from('<H', data, 0)[0]
                chars[cid] = (TAGNAMES[code], data)
            if code in (26, 70):
                frames[-1].append(('place', parse_place2(data, code)))
            elif code == 28:
                frames[-1].append(('remove', struct.unpack_from('<H', data, 0)[0]))
            elif code == 43:
                e = data.index(b'\0')
                frames[-1].append(('label', data[:e].decode('utf-8', 'replace')))
            elif code == 1:
                frames.append([])
        return frames
    root = scan(buf, pos, None)
    return chars, sprites, root


def main(path, roots=None):
    ver, buf, pos = load(path)
    chars, sprites, root = collect(buf, pos)
    def show(frames, indent, depth_limit, seen):
        for fi, fr in enumerate(frames):
            for kind, v in fr:
                if kind == 'label':
                    print(indent + 'F%d label %s' % (fi, v))
                elif kind == 'place' and ('char' in v or 'name' in v):
                    c = v.get('char')
                    t = chars.get(c, ('?',))[0] if c else ''
                    m = v.get('matrix', {})
                    print(indent + 'F%d d%d %s #%s %s tx=%s ty=%s sx=%.2f' % (fi, v['depth'], v.get('name', ''), c, t,
                          m.get('tx', ''), m.get('ty', ''), m.get('a', 1)))
                    if c in sprites and depth_limit > 0 and c not in seen:
                        show(sprites[c], indent + '    ', depth_limit - 1, seen | {c})
    if roots:
        for r in roots:
            print('== sprite', r)
            show(sprites[r], '  ', 2, set())
    else:
        show(root, '', 3, set())


if __name__ == '__main__':
    main(sys.argv[1], [int(x) for x in sys.argv[2:]] or None)
