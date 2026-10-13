#!/usr/bin/env python3
"""Tiny AVM1 decompiler -> goto-style pseudo JS. Good enough to read game logic."""
import sys, os, struct, re
sys.path.insert(0, os.path.dirname(__file__))
from swfx import load, walk
from avm1 import OPS, cstr

BIN = {0x0A: '+', 0x0B: '-', 0x0C: '*', 0x0D: '/', 0x0E: '==', 0x0F: '<', 0x10: '&&', 0x11: '||',
       0x47: '+', 0x48: '<', 0x49: '==', 0x3F: '%', 0x60: '&', 0x61: '|', 0x62: '^', 0x63: '<<', 0x64: '>>',
       0x65: '>>>', 0x66: '===', 0x67: '>', 0x13: 'eq', 0x29: 'lt', 0x68: 'gt', 0x21: '..'}


def parse_actions(code, base=0):
    acts = []
    p = 0
    while p < len(code):
        start = p
        op = code[p]; p += 1
        if op == 0:
            acts.append((base + start, 0, b'', 1)); break
        ln = 0
        if op >= 0x80:
            ln = struct.unpack_from('<H', code, p)[0]; p += 2
        d = code[p:p + ln]; p += ln
        if op in (0x9B, 0x8E):
            # function: body follows
            fn, q = cstr(d, 0)
            if op == 0x9B:
                n = struct.unpack_from('<H', d, q)[0]; q += 2
                params = []
                for _ in range(n):
                    s, q = cstr(d, q); params.append((s, None))
                clen = struct.unpack_from('<H', d, q)[0]
                regc = 0; flags = 0
            else:
                n, regc, flags = struct.unpack_from('<HBH', d, q); q += 5
                params = []
                for _ in range(n):
                    r = d[q]; q += 1
                    s, q = cstr(d, q); params.append((s, r))
                clen = struct.unpack_from('<H', d, q)[0]
            body = code[p:p + clen]
            acts.append((base + start, op, (fn, params, regc, flags, body), p + clen - start))
            p += clen
            continue
        acts.append((base + start, op, d, p - start))
    return acts


def fmt_val(v):
    return v


class Dec:
    def __init__(self, pool):
        self.pool = pool

    def preg(self, params, flags):
        regs = {}
        r = 1
        # preloaded registers order: this, arguments, super, _root, _parent, _global
        order = [(0x1, 'this'), (0x4, 'arguments'), (0x10, 'super'), (0x40, '_root'), (0x80, '_parent'), (0x100, '_global')]
        for bit, name in order:
            if flags & bit:
                regs[r] = name; r += 1
        for s, rr in params:
            if rr:
                regs[rr] = s
        return regs

    def dec(self, code, indent='', regs=None):
        regs = dict(regs or {})
        acts = parse_actions(code)
        targets = set()
        for off, op, d, size in acts:
            if op in (0x99, 0x9D):
                rel = struct.unpack_from('<h', d, 0)[0]
                targets.add(off + size + rel)
        out = []
        st = []
        def pop():
            return st.pop() if st else '<?>'
        def R(i):
            return regs.get(i, 'r%d' % i)
        for off, op, d, size in acts:
            if off in targets:
                if st:
                    out.append(indent + '// stack: ' + ', '.join(st))
                out.append(indent[:-2] + 'L%d:' % off)
            if op == 0:
                break
            if op == 0x88:
                n = struct.unpack_from('<H', d, 0)[0]; q = 2; self.pool = []
                for _ in range(n):
                    s, q = cstr(d, q); self.pool.append(s)
            elif op == 0x96:
                q = 0
                while q < len(d):
                    t = d[q]; q += 1
                    if t == 0: s, q = cstr(d, q); st.append(repr(s))
                    elif t == 1: st.append('%g' % struct.unpack_from('<f', d, q)[0]); q += 4
                    elif t == 2: st.append('null')
                    elif t == 3: st.append('undefined')
                    elif t == 4: st.append(R(d[q])); q += 1
                    elif t == 5: st.append('true' if d[q] else 'false'); q += 1
                    elif t == 6:
                        x = struct.unpack_from('<II', d, q); q += 8
                        st.append('%g' % struct.unpack('<d', struct.pack('<II', x[1], x[0]))[0])
                    elif t == 7: st.append(str(struct.unpack_from('<i', d, q)[0])); q += 4
                    elif t == 8: st.append(repr(self.pool[d[q]])); q += 1
                    elif t == 9: st.append(repr(self.pool[struct.unpack_from('<H', d, q)[0]])); q += 2
            elif op in BIN:
                b = pop(); a = pop(); st.append('(%s %s %s)' % (a, BIN[op], b))
            elif op == 0x12:
                st.append('!' + pop())
            elif op == 0x17:
                v = pop()
                if v.endswith(')') or '(' in v:
                    out.append(indent + v + ';')
            elif op == 0x1C:
                n = pop(); st.append(n.strip("'") if n.startswith("'") else 'eval(%s)' % n)
            elif op == 0x1D:
                v = pop(); n = pop(); out.append(indent + '%s = %s;' % (n.strip("'"), v))
            elif op == 0x4E:
                n = pop(); o = pop()
                st.append('%s.%s' % (o, n.strip("'")) if re.match(r"^'[A-Za-z_$][\w$]*'$", n) else '%s[%s]' % (o, n))
            elif op == 0x4F:
                v = pop(); n = pop(); o = pop()
                tgt = '%s.%s' % (o, n.strip("'")) if re.match(r"^'[A-Za-z_$][\w$]*'$", n) else '%s[%s]' % (o, n)
                out.append(indent + '%s = %s;' % (tgt, v))
            elif op == 0x3D:
                n = pop(); c = pop(); c = int(float(c)) if re.match(r'^-?\d', c) else 0
                args = [pop() for _ in range(c)]
                st.append('%s(%s)' % (n.strip("'"), ', '.join(args)))
            elif op == 0x52:
                n = pop(); o = pop(); c = pop(); c = int(float(c)) if re.match(r'^-?\d', c) else 0
                args = [pop() for _ in range(c)]
                if n in ("''", 'undefined'):
                    st.append('%s(%s)' % (o, ', '.join(args)))
                else:
                    st.append('%s.%s(%s)' % (o, n.strip("'"), ', '.join(args)))
            elif op == 0x40:
                n = pop(); c = pop(); c = int(float(c)) if re.match(r'^-?\d', c) else 0
                args = [pop() for _ in range(c)]
                st.append('new %s(%s)' % (n.strip("'"), ', '.join(args)))
            elif op == 0x53:
                n = pop(); o = pop(); c = pop(); c = int(float(c)) if re.match(r'^-?\d', c) else 0
                args = [pop() for _ in range(c)]
                st.append('new %s.%s(%s)' % (o, n.strip("'"), ', '.join(args)))
            elif op == 0x42:
                c = pop(); c = int(float(c)) if re.match(r'^-?\d', c) else 0
                st.append('[%s]' % ', '.join(pop() for _ in range(c)))
            elif op == 0x43:
                c = pop(); c = int(float(c)) if re.match(r'^-?\d', c) else 0
                items = []
                for _ in range(c):
                    v = pop(); k = pop(); items.append('%s: %s' % (k, v))
                st.append('{%s}' % ', '.join(items))
            elif op == 0x3C:
                v = pop(); n = pop(); out.append(indent + 'var %s = %s;' % (n.strip("'"), v))
            elif op == 0x41:
                n = pop(); out.append(indent + 'var %s;' % n.strip("'"))
            elif op == 0x87:
                r = d[0]
                v = st[-1] if st else '<?>'
                out.append(indent + '%s = %s;' % (R(r), v))
                if st:
                    st[-1] = R(r)
            elif op == 0x4C:
                st.append(st[-1] if st else '<?>')
            elif op == 0x4D:
                if len(st) >= 2:
                    st[-1], st[-2] = st[-2], st[-1]
            elif op == 0x50:
                st.append('(%s + 1)' % pop())
            elif op == 0x51:
                st.append('(%s - 1)' % pop())
            elif op == 0x3E:
                out.append(indent + 'return %s;' % pop())
            elif op == 0x99:
                rel = struct.unpack_from('<h', d, 0)[0]
                if st:
                    out.append(indent + '// stack: ' + ', '.join(st))
                out.append(indent + 'goto L%d;' % (off + size + rel))
            elif op == 0x9D:
                rel = struct.unpack_from('<h', d, 0)[0]
                c = pop()
                out.append(indent + 'if %s goto L%d;' % (c, off + size + rel))
            elif op == 0x30:
                st.append('random(%s)' % pop())
            elif op == 0x34:
                st.append('getTimer()')
            elif op == 0x44:
                st.append('typeof(%s)' % pop())
            elif op == 0x26:
                out.append(indent + 'trace(%s);' % pop())
            elif op == 0x18:
                st.append('int(%s)' % pop())
            elif op == 0x4A:
                st.append('Number(%s)' % pop())
            elif op == 0x4B:
                st.append('String(%s)' % pop())
            elif op == 0x14 or op == 0x31:
                st.append('length(%s)' % pop())
            elif op in (0x15, 0x35):
                c = pop(); i = pop(); s = pop(); st.append('substring(%s, %s, %s)' % (s, i, c))
            elif op == 0x3A:
                n = pop(); o = pop(); st.append('delete %s[%s]' % (o, n))
            elif op == 0x3B:
                st.append('delete %s' % pop())
            elif op == 0x54:
                c = pop(); o = pop(); st.append('(%s instanceof %s)' % (o, c))
            elif op in (0x46, 0x55):
                o = pop(); st.append('<enum %s>' % o)
            elif op == 0x22:
                i = pop(); t = pop(); st.append('getProperty(%s, %s)' % (t, i))
            elif op == 0x23:
                v = pop(); i = pop(); t = pop(); out.append(indent + 'setProperty(%s, %s, %s);' % (t, i, v))
            elif op == 0x2B:
                o = pop(); c = pop(); st.append('%s(%s)' % (c, o))
            elif op == 0x69:
                sup = pop(); sub = pop(); out.append(indent + '%s extends %s;' % (sub, sup))
            elif op == 0x2C:
                c = pop(); c = int(float(c)) if re.match(r'^-?\d', c) else 0
                ifs = [pop() for _ in range(c)]; cl = pop()
                out.append(indent + '%s implements %s;' % (cl, ', '.join(ifs)))
            elif op in (0x9B, 0x8E):
                fn, params, regc, flags, body = d
                fregs = self.preg(params, flags) if op == 0x8E else {}
                names = [p[0] for p in params]
                sub = Dec(self.pool).dec(body, indent + '    ', fregs)
                text = 'function %s(%s) {\n%s\n%s}' % (fn, ', '.join(names), '\n'.join(sub), indent)
                if fn:
                    out.append(indent + text)
                else:
                    st.append(text)
            elif op == 0x07:
                out.append(indent + 'stop();')
            elif op == 0x06:
                out.append(indent + 'play();')
            elif op == 0x81:
                out.append(indent + 'gotoFrame(%d);' % struct.unpack_from('<H', d, 0)[0])
            elif op == 0x8C:
                out.append(indent + 'gotoLabel(%r);' % cstr(d, 0)[0])
            elif op == 0x9F:
                out.append(indent + 'gotoFrame2(%s);' % pop())
            elif op == 0x83:
                a, q = cstr(d, 0); b, _ = cstr(d, q); out.append(indent + 'getURL(%r, %r);' % (a, b))
            elif op == 0x9A:
                t = pop(); u = pop(); out.append(indent + 'getURL2(%s, %s);' % (u, t))
            elif op == 0x8B:
                out.append(indent + 'setTarget(%r);' % cstr(d, 0)[0])
            elif op == 0x20:
                out.append(indent + 'setTarget(%s);' % pop())
            elif op == 0x24:
                dp = pop(); tg = pop(); sr = pop(); out.append(indent + 'duplicateMovieClip(%s, %s, %s);' % (sr, tg, dp))
            elif op == 0x25:
                out.append(indent + 'removeMovieClip(%s);' % pop())
            elif op == 0x94:
                out.append(indent + 'with (%s) {...}' % pop())
            elif op == 0x2A:
                out.append(indent + 'throw %s;' % pop())
            elif op == 0x45:
                st.append('targetPath(%s)' % pop())
            elif op == 0x09:
                out.append(indent + 'stopAllSounds();')
            elif op == 0x27:
                out.append(indent + 'startDrag(...);')
            elif op == 0x28:
                out.append(indent + 'stopDrag();')
            elif op == 0x32 or op == 0x36:
                st.append('ord(%s)' % pop())
            elif op == 0x33 or op == 0x37:
                st.append('chr(%s)' % pop())
            else:
                out.append(indent + '/* %s */' % OPS.get(op, hex(op)))
        return out


def main(path):
    ver, buf, pos = load(path)
    for code, data, sprite in walk(buf, pos):
        if code in (12, 59):
            body = data if code == 12 else data[2:]
            print('//==== %s sprite=%s' % ('DoAction' if code == 12 else 'DoInitAction', sprite))
            print('\n'.join(Dec([]).dec(body, '  ')))


if __name__ == '__main__':
    main(sys.argv[1])
