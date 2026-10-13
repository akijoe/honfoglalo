#!/usr/bin/env python3
"""Builds swf/testclient.swf from the original archive/swf/testclient.swf.
Graphics and game rules are untouched; the additions are ActionScript sources in src/, compiled by
tools/as1c.py into DoAction tags:

  src/core.as    start of frame 1, before the game's code: _global.hf, the sound bridge
                 (_global.gsound -> page hfSound) and the network bridge (page hfNet, polled)
  src/tweaks.as  after the game's code: start menu (4th button = feedback, 3rd = rooms), and for the
                 robot / multiplayer game: no tutorial boxes, live clock, result + menu at the end
  src/lobby.as   the Flash windows: room (create / join / wait), feedback, volume control
  src/questions.as  question choice in every mode: random, no repeats, unseen questions first
  src/mp.as      the multiplayer game (only with the flashvar mp=1)

Run:  python3 tools/patch_swf.py
"""
import os
import struct
import sys
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
sys.path.insert(0, HERE)
from as1c import compile_file, CompileError  # noqa: E402

SRC = os.path.join(ROOT, 'archive', 'swf', 'testclient.swf')
DST = os.path.join(ROOT, 'swf', 'testclient.swf')
FIRST = ['core.as']
AFTER_MAIN = ['tweaks.as', 'lobby.as', 'questions.as', 'mp.as']


def tag(code, data):
    if len(data) < 0x3f:
        return struct.pack('<H', code << 6 | len(data)) + data
    return struct.pack('<HI', code << 6 | 0x3f, len(data)) + data


def iter_tags(body, pos):
    while pos < len(body):
        cl = struct.unpack_from('<H', body, pos)[0]
        code, ln, hdr = cl >> 6, cl & 0x3f, 2
        if ln == 0x3f:
            ln, hdr = struct.unpack_from('<I', body, pos + 2)[0], 6
        yield code, pos, hdr + ln, body[pos + hdr:pos + hdr + ln]
        pos += hdr + ln
        if code == 0:
            return


def compile_all(names):
    out = b''
    for n in names:
        code = compile_file(os.path.join(ROOT, 'src', n))
        print('  src/%-10s %6d bájt' % (n, len(code)))
        out += tag(12, code)
    return out


def main():
    raw = open(SRC, 'rb').read()
    assert raw[:3] == b'CWS'
    version = raw[3]
    body = zlib.decompress(raw[8:])
    nbits = body[0] >> 3
    start = (5 + 4 * nbits + 7) // 8 + 4
    first_tag, main_end = None, None
    for code, pos, size, data in iter_tags(body, start):
        if first_tag is None and code not in (69, 77, 9, 65, 64, 58, 24):
            first_tag = pos
        if code == 12 and b'HandleGamePhase' in data and b'AddTutorial' in data:
            main_end = pos + size
    assert first_tag is not None and main_end is not None
    try:
        first, after = compile_all(FIRST), compile_all(AFTER_MAIN)
    except CompileError as e:
        sys.exit('fordítási hiba: %s' % e)
    new_body = body[:first_tag] + first + body[first_tag:main_end] + after + body[main_end:]
    out = b'CWS' + bytes([version]) + struct.pack('<I', len(new_body) + 8) + zlib.compress(new_body, 9)
    os.makedirs(os.path.dirname(DST), exist_ok=True)
    open(DST, 'wb').write(out)
    print('%s -> %s' % (os.path.relpath(SRC, ROOT), os.path.relpath(DST, ROOT)))


if __name__ == '__main__':
    main()
