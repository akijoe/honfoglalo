#!/usr/bin/env python3
"""Extracts the exported sounds of a SWF (e.g. the full client's client/sounds.swf) into sounds/
and writes sounds/manifest.json, which index.html uses to answer the game's gsound calls.

Usage:  python3 tools/extract_sounds.py path/to/sounds.swf [more.swf ...]
"""
import json, os, struct, sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
sys.path.insert(0, HERE)
from swfx import load, walk, wav_header, RATES  # noqa: E402

OUT = os.path.join(ROOT, 'sounds')


def extract(path, manifest):
    ver, buf, pos = load(path)
    names, sounds = {}, {}
    for code, data, sprite in walk(buf, pos):
        if code in (56, 76):   # ExportAssets / SymbolClass: linkage names
            n = struct.unpack_from('<H', data, 0)[0]
            q = 2
            for _ in range(n):
                cid = struct.unpack_from('<H', data, q)[0]
                e = data.index(b'\0', q + 2)
                names.setdefault(cid, data[q + 2:e].decode('utf-8', 'replace'))
                q = e + 1
        elif code == 14:       # DefineSound
            sid, flags = struct.unpack_from('<HB', data, 0)
            sounds[sid] = (flags, data[7:])
    for sid, (flags, payload) in sorted(sounds.items()):
        name = names.get(sid)
        if not name:
            continue
        fmt = flags >> 4
        if fmt == 2:           # MP3 (skip SeekSamples)
            fn, blob = name + '.mp3', payload[2:]
        elif fmt in (0, 3):    # uncompressed PCM
            fn = name + '.wav'
            blob = wav_header(2 if flags & 1 else 1, RATES[(flags >> 2) & 3], 16 if flags & 2 else 8, len(payload)) + payload
        else:
            print('  kihagyva (formátum %d): %s' % (fmt, name))
            continue
        open(os.path.join(OUT, fn), 'wb').write(blob)
        manifest[name] = fn
        print('  %s' % fn)


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    os.makedirs(OUT, exist_ok=True)
    mpath = os.path.join(OUT, 'manifest.json')
    manifest = json.load(open(mpath)) if os.path.exists(mpath) else {}
    for p in sys.argv[1:]:
        print(p)
        extract(p, manifest)
    json.dump(manifest, open(mpath, 'w'), indent=1, sort_keys=True)
    print('%d hang a sounds/manifest.json-ban' % len(manifest))


if __name__ == '__main__':
    main()
