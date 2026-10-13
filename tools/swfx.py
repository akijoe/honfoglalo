#!/usr/bin/env python3
"""Minimal SWF extractor: sounds (DefineSound, stream), images, export names, tag stats."""
import sys, zlib, struct, os, io, json, collections

TAGNAMES = {0:'End',1:'ShowFrame',2:'DefineShape',4:'PlaceObject',5:'RemoveObject',6:'DefineBits',
 7:'DefineButton',8:'JPEGTables',9:'SetBackgroundColor',10:'DefineFont',11:'DefineText',12:'DoAction',
 13:'DefineFontInfo',14:'DefineSound',15:'StartSound',17:'DefineButtonSound',18:'SoundStreamHead',
 19:'SoundStreamBlock',20:'DefineBitsLossless',21:'DefineBitsJPEG2',22:'DefineShape2',24:'Protect',
 26:'PlaceObject2',28:'RemoveObject2',32:'DefineShape3',33:'DefineText2',34:'DefineButton2',
 35:'DefineBitsJPEG3',36:'DefineBitsLossless2',37:'DefineEditText',39:'DefineSprite',43:'FrameLabel',
 45:'SoundStreamHead2',46:'DefineMorphShape',48:'DefineFont2',56:'ExportAssets',57:'ImportAssets',
 58:'EnableDebugger',59:'DoInitAction',60:'DefineVideoStream',61:'VideoFrame',62:'DefineFontInfo2',
 64:'EnableDebugger2',65:'ScriptLimits',66:'SetTabIndex',69:'FileAttributes',70:'PlaceObject3',
 71:'ImportAssets2',73:'DefineFontAlignZones',74:'CSMTextSettings',75:'DefineFont3',76:'SymbolClass',
 77:'Metadata',78:'DefineScalingGrid',82:'DoABC',83:'DefineShape4',84:'DefineMorphShape2',
 86:'DefineSceneAndFrameLabelData',87:'DefineBinaryData',88:'DefineFontName',89:'StartSound2',
 90:'DefineBitsJPEG4',91:'DefineFont4'}

RATES = [5512, 11025, 22050, 44100]
FMT = {0:'pcm_ne',1:'adpcm',2:'mp3',3:'pcm_le',4:'nelly16',5:'nelly8',6:'nelly',11:'speex'}


def load(path):
    data = open(path, 'rb').read()
    sig = data[:3]
    ver = data[3]
    if sig == b'CWS':
        body = zlib.decompress(data[8:])
    elif sig == b'FWS':
        body = data[8:]
    else:
        raise ValueError('unsupported ' + repr(sig))
    # skip RECT
    nbits = body[0] >> 3
    rect_len = (5 + 4 * nbits + 7) // 8
    pos = rect_len + 4  # frame rate + count
    return ver, body, pos


def iter_tags(buf, pos, end=None):
    end = len(buf) if end is None else end
    while pos < end:
        if pos + 2 > end:
            return
        code_len = struct.unpack_from('<H', buf, pos)[0]
        pos += 2
        code = code_len >> 6
        ln = code_len & 0x3f
        if ln == 0x3f:
            ln = struct.unpack_from('<I', buf, pos)[0]
            pos += 4
        yield code, buf[pos:pos + ln], pos
        pos += ln
        if code == 0:
            return


def walk(buf, pos, depth=0, sprite=None):
    for code, data, p in iter_tags(buf, pos):
        yield code, data, sprite
        if code == 39:
            sid = struct.unpack_from('<H', data, 0)[0]
            # sprite body: id(2) framecount(2) tags
            for t in walk(data, 4, depth + 1, sid):
                yield t


def wav_header(nch, rate, bits, datalen):
    ba = nch * bits // 8
    return (b'RIFF' + struct.pack('<I', 36 + datalen) + b'WAVEfmt ' +
            struct.pack('<IHHIIHH', 16, 1, nch, rate, rate * ba, ba, bits) +
            b'data' + struct.pack('<I', datalen))


def main(path, outdir):
    os.makedirs(outdir, exist_ok=True)
    ver, buf, pos = load(path)
    stats = collections.Counter()
    exports = {}
    sounds = []
    streams = collections.defaultdict(lambda: {'head': None, 'blocks': []})
    jpegtables = None
    images = []
    for code, data, sprite in walk(buf, pos):
        stats[TAGNAMES.get(code, code)] += 1
        if code == 56 or code == 76:
            n = struct.unpack_from('<H', data, 0)[0]
            q = 2
            for _ in range(n):
                cid = struct.unpack_from('<H', data, q)[0]
                q += 2
                e = data.index(b'\0', q)
                exports.setdefault(cid, []).append(data[q:e].decode('utf-8', 'replace'))
                q = e + 1
        elif code == 14:
            sid, flags = struct.unpack_from('<HB', data, 0)
            cnt = struct.unpack_from('<I', data, 3)[0]
            fmt = flags >> 4
            rate = RATES[(flags >> 2) & 3]
            bits = 16 if flags & 2 else 8
            ch = 2 if flags & 1 else 1
            sounds.append((sid, fmt, rate, bits, ch, cnt, data[7:]))
        elif code in (18, 45):
            # stream head: playback flags(1) stream flags(1) samplecount(2) [latency(2)]
            f2 = data[1]
            streams[sprite]['head'] = (f2 >> 4, RATES[(f2 >> 2) & 3], 16 if f2 & 2 else 8, 2 if f2 & 1 else 1)
        elif code == 19:
            streams[sprite]['blocks'].append(data)
        elif code == 8:
            jpegtables = data
        elif code in (6, 21, 35, 20, 36, 90):
            images.append((code, data))

    names = {}
    for sid, fmt, rate, bits, ch, cnt, sdata in sounds:
        nm = exports.get(sid, ['snd%d' % sid])[0]
        nm = ''.join(c if c.isalnum() or c in '-_.' else '_' for c in nm)
        base = os.path.join(outdir, 'sound_%03d_%s' % (sid, nm))
        if fmt == 2:
            open(base + '.mp3', 'wb').write(sdata[2:])
            out = base + '.mp3'
        elif fmt in (0, 3):
            open(base + '.wav', 'wb').write(wav_header(ch, rate, bits, len(sdata)) + sdata)
            out = base + '.wav'
        else:
            open(base + '.%s.raw' % FMT.get(fmt, fmt), 'wb').write(sdata)
            out = base + '.raw'
        names[sid] = dict(file=os.path.basename(out), fmt=FMT.get(fmt, fmt), rate=rate, bits=bits,
                          ch=ch, samples=cnt, dur=round(cnt / rate, 2), export=exports.get(sid))
    for sp, st in streams.items():
        if not st['blocks'] or not st['head']:
            continue
        fmt, rate, bits, ch = st['head']
        base = os.path.join(outdir, 'stream_%s' % (sp if sp is not None else 'main'))
        if fmt == 2:
            # each block: samplecount(2) seek(2) mp3frames
            open(base + '.mp3', 'wb').write(b''.join(b[4:] for b in st['blocks']))
        elif fmt in (0, 3):
            pcm = b''.join(st['blocks'])
            open(base + '.wav', 'wb').write(wav_header(ch, rate, bits, len(pcm)) + pcm)
        else:
            open(base + '.%s.raw' % FMT.get(fmt, fmt), 'wb').write(b''.join(st['blocks']))
        names['stream_%s' % sp] = dict(fmt=FMT.get(fmt, fmt), rate=rate, blocks=len(st['blocks']), sprite=sp,
                                        export=exports.get(sp))
    json.dump(dict(version=ver, stats=stats, sounds=names, exports={str(k): v for k, v in exports.items()}),
              open(os.path.join(outdir, 'info.json'), 'w'), indent=1, ensure_ascii=False)
    print(path, 'ver', ver)
    print(' tags:', dict(stats.most_common()))
    print(' sounds:', len(sounds), 'streams:', sum(1 for s in streams.values() if s['blocks']))
    for k, v in names.items():
        print('  ', k, v)


if __name__ == '__main__':
    main(sys.argv[1], sys.argv[2])
