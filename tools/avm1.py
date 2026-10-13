#!/usr/bin/env python3
"""AVM1 (ActionScript 1/2) disassembler for DoAction / DoInitAction / button+clip actions."""
import sys, struct
sys.path.insert(0, __import__('os').path.dirname(__file__))
from swfx import load, walk, TAGNAMES

OPS = {0x04:'nextFrame',0x05:'prevFrame',0x06:'play',0x07:'stop',0x08:'toggleQuality',0x09:'stopSounds',
0x0A:'add',0x0B:'subtract',0x0C:'multiply',0x0D:'divide',0x0E:'equals',0x0F:'less',0x10:'and',0x11:'or',
0x12:'not',0x13:'stringEquals',0x14:'stringLength',0x15:'stringExtract',0x17:'pop',0x18:'toInteger',
0x1C:'getVariable',0x1D:'setVariable',0x20:'setTarget2',0x21:'stringAdd',0x22:'getProperty',
0x23:'setProperty',0x24:'cloneSprite',0x25:'removeSprite',0x26:'trace',0x27:'startDrag',0x28:'endDrag',
0x29:'stringLess',0x2A:'throw',0x2B:'castOp',0x2C:'implementsOp',0x30:'randomNumber',0x31:'mbStringLength',
0x32:'charToAscii',0x33:'asciiToChar',0x34:'getTime',0x35:'mbStringExtract',0x36:'mbCharToAscii',
0x37:'mbAsciiToChar',0x3A:'delete',0x3B:'delete2',0x3C:'defineLocal',0x3D:'callFunction',0x3E:'return',
0x3F:'modulo',0x40:'newObject',0x41:'defineLocal2',0x42:'initArray',0x43:'initObject',0x44:'typeOf',
0x45:'targetPath',0x46:'enumerate',0x47:'add2',0x48:'less2',0x49:'equals2',0x4A:'toNumber',0x4B:'toString',
0x4C:'pushDuplicate',0x4D:'stackSwap',0x4E:'getMember',0x4F:'setMember',0x50:'increment',0x51:'decrement',
0x52:'callMethod',0x53:'newMethod',0x54:'instanceOf',0x55:'enumerate2',0x60:'bitAnd',0x61:'bitOr',
0x62:'bitXor',0x63:'bitLShift',0x64:'bitRShift',0x65:'bitURShift',0x66:'strictEquals',0x67:'greater',
0x68:'stringGreater',0x69:'extends',0x81:'gotoFrame',0x83:'getURL',0x87:'storeRegister',0x88:'constantPool',
0x8A:'waitForFrame',0x8B:'setTarget',0x8C:'gotoLabel',0x8D:'waitForFrame2',0x8E:'defineFunction2',
0x8F:'try',0x94:'with',0x96:'push',0x99:'jump',0x9A:'getURL2',0x9B:'defineFunction',0x9D:'if',0x9E:'call',
0x9F:'gotoFrame2'}


def cstr(b, p):
    e = b.index(b'\0', p)
    return b[p:e].decode('utf-8', 'replace'), e + 1


def disasm(code, out, indent=''):
    pool = []
    p = 0
    while p < len(code):
        op = code[p]
        p += 1
        if op == 0:
            out.append(indent + 'end')
            break
        ln = 0
        if op >= 0x80:
            ln = struct.unpack_from('<H', code, p)[0]
            p += 2
        d = code[p:p + ln]
        p += ln
        name = OPS.get(op, 'op_%02x' % op)
        arg = ''
        if op == 0x88:
            n = struct.unpack_from('<H', d, 0)[0]
            q = 2
            pool = []
            for _ in range(n):
                s, q = cstr(d, q)
                pool.append(s)
            arg = '[%d strings]' % n
        elif op == 0x96:
            q = 0
            vals = []
            while q < len(d):
                t = d[q]
                q += 1
                if t == 0:
                    s, q = cstr(d, q); vals.append(repr(s))
                elif t == 1:
                    vals.append(repr(struct.unpack_from('<f', d, q)[0])); q += 4
                elif t == 2: vals.append('null')
                elif t == 3: vals.append('undefined')
                elif t == 4: vals.append('r%d' % d[q]); q += 1
                elif t == 5: vals.append('true' if d[q] else 'false'); q += 1
                elif t == 6:
                    x = struct.unpack_from('<II', d, q); q += 8
                    vals.append(repr(struct.unpack('<d', struct.pack('<II', x[1], x[0]))[0]))
                elif t == 7: vals.append(str(struct.unpack_from('<i', d, q)[0])); q += 4
                elif t == 8: vals.append(repr(pool[d[q]]) if d[q] < len(pool) else 'c%d' % d[q]); q += 1
                elif t == 9:
                    i = struct.unpack_from('<H', d, q)[0]; q += 2
                    vals.append(repr(pool[i]) if i < len(pool) else 'c%d' % i)
                else:
                    vals.append('?%d' % t); break
            arg = ', '.join(vals)
        elif op in (0x9B, 0x8E):
            fn, q = cstr(d, 0)
            if op == 0x9B:
                n = struct.unpack_from('<H', d, q)[0]; q += 2
                params = []
                for _ in range(n):
                    s, q = cstr(d, q); params.append(s)
                clen = struct.unpack_from('<H', d, q)[0]
            else:
                n, regc, flags = struct.unpack_from('<HBH', d, q); q += 5
                params = []
                for _ in range(n):
                    r = d[q]; q += 1
                    s, q = cstr(d, q); params.append('%s(r%d)' % (s, r))
                clen = struct.unpack_from('<H', d, q)[0]
            arg = '%s(%s) len=%d' % (fn, ', '.join(params), clen)
            out.append(indent + '%s %s' % (name, arg))
            body = code[p:p + clen]
            sub = []
            disasm_with_pool(body, sub, indent + '    ', pool)
            out.extend(sub)
            p += clen
            continue
        elif op in (0x99, 0x9D):
            arg = 'off %d' % struct.unpack_from('<h', d, 0)[0]
        elif op in (0x8C, 0x8B):
            arg = cstr(d, 0)[0]
        elif op == 0x83:
            a, q = cstr(d, 0); b, _ = cstr(d, q); arg = '%r %r' % (a, b)
        elif op == 0x81:
            arg = str(struct.unpack_from('<H', d, 0)[0])
        elif op == 0x87:
            arg = 'r%d' % d[0]
        elif ln:
            arg = d.hex()
        out.append(indent + ('%s %s' % (name, arg)).rstrip())
    return pool


def disasm_with_pool(code, out, indent, pool):
    """Function bodies inherit the enclosing constant pool."""
    disasm2(code, out, indent, pool)


def disasm2(code, out, indent, pool):
    # prepend a synthetic constantPool action so disasm() starts with the inherited pool
    fake = bytearray()
    if pool:
        payload = struct.pack('<H', len(pool)) + b''.join(s.encode('utf-8') + b'\0' for s in pool)
        if len(payload) < 65536:
            fake += bytes([0x88]) + struct.pack('<H', len(payload)) + payload
    fake += code
    tmp = []
    disasm(bytes(fake), tmp, indent)
    if pool and tmp and tmp[0].strip().startswith('constantPool'):
        tmp = tmp[1:]
    out.extend(tmp)


def main(path):
    ver, buf, pos = load(path)
    frame = {}
    for code, data, sprite in walk(buf, pos):
        if code in (12, 59):
            out = []
            body = data if code == 12 else data[2:]
            hdr = 'DoAction' if code == 12 else 'DoInitAction(sprite %d)' % struct.unpack_from('<H', data, 0)[0]
            print('==== %s in sprite %s' % (hdr, sprite))
            disasm(body, out, '  ')
            print('\n'.join(out))


if __name__ == '__main__':
    main(sys.argv[1])
