#!/usr/bin/env python3
"""Builds the static site (the whole game, single player included) for Cloudflare Pages or any
static host. The multiplayer rooms need the Go server (server/) running somewhere else; its address
goes into js/config.js.

    python3 tools/build_site.py --api https://api.example.com/      -> dist/

Cloudflare Pages: build command "python3 tools/build_site.py", output directory "dist", and set the
HF_API_URL environment variable (used when --api is not given). Without an API address the game
still works, only the "Játék ismerősökkel" rooms and the feedback form cannot reach a server.
"""
import argparse
import glob
import hashlib
import json
import os
import shutil
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SOUND_EXTS = ('.mp3', '.ogg', '.wav', '.m4a')
MAX_FILE = 25 * 1024 * 1024        # Cloudflare Pages: legfeljebb 25 MiB fájlonként


def copy(rel, out):
    src = os.path.join(ROOT, rel)
    dst = os.path.join(out, rel)
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    shutil.copy2(src, dst)
    if os.path.getsize(dst) > MAX_FILE:
        sys.exit('túl nagy fájl a Cloudflare Pages-hez: %s' % rel)
    return rel


def bust(out, page, tag, rel):
    """A page fájlban a tag (pontosan egyszer) a rel fájl címét tartalmazza: mögé kerül a rel tartalmának
    hash-e (?v=...)."""
    v = hashlib.sha256(open(os.path.join(out, rel), 'rb').read()).hexdigest()[:10]
    path = os.path.join(out, page)
    text = open(path, encoding='utf-8').read()
    if text.count(tag) != 1:
        sys.exit('%s: nem pontosan egyszer szerepel benne: %s' % (page, tag))
    open(path, 'w', encoding='utf-8').write(text.replace(tag, tag.replace(rel, rel + '?v=' + v)))


def sound_manifest():
    """Ugyanaz, amit a Go szerver a /sounds/manifest.json-ra ad: a sounds/ minden hangfájlja a saját
    nevén, plusz a sounds/manifest.json eltérő nevű bejegyzései (csak létező fájlra)."""
    files = sorted(f for f in os.listdir(os.path.join(ROOT, 'sounds'))
                   if f.lower().endswith(SOUND_EXTS) and not f.startswith('.'))
    m = {os.path.splitext(f)[0]: f for f in files}
    path = os.path.join(ROOT, 'sounds', 'manifest.json')
    if os.path.exists(path):
        for k, v in json.load(open(path, encoding='utf-8')).items():
            if v in files:
                m[k] = v
    return files, m


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--api', default=os.environ.get('HF_API_URL', ''),
                    help='a többjátékos szerver címe, pl. https://api.example.com/ (alapból: HF_API_URL)')
    ap.add_argument('--out', default=os.path.join(ROOT, 'dist'))
    args = ap.parse_args()
    out = os.path.abspath(args.out)
    if os.path.exists(out):
        shutil.rmtree(out)
    os.makedirs(out)

    files = ['index.html', 'favicon.ico', 'img/bg.png', 'js/hf.js', 'swf/testclient.swf', 'questions.xml', 'dat/lang.json',
             'vendor/ruffle/ruffle.js', 'vendor/ruffle/LICENSE_MIT', 'vendor/ruffle/LICENSE_APACHE']
    hashed = []
    for p in sorted(glob.glob(os.path.join(ROOT, 'vendor', 'ruffle', '*'))):
        name = os.path.basename(p)
        if name.endswith('.wasm') or name.startswith('core.ruffle.'):
            hashed.append('vendor/ruffle/' + name)
    thumbs = ['img/maps/' + os.path.basename(p) for p in sorted(glob.glob(os.path.join(ROOT, 'img', 'maps', '*.png')))]
    thumbs += ['img/' + os.path.basename(p) for p in sorted(glob.glob(os.path.join(ROOT, 'img', '*.gif')))]   # tools/make_gifs.py
    thumbs += ['img/rules/' + os.path.basename(p) for p in sorted(glob.glob(os.path.join(ROOT, 'img', 'rules', '*.*')))]   # svg: az oldalnak, png: a Flash szoba-ablakának
    for rel in files + hashed + thumbs:
        copy(rel, out)

    sounds, manifest = sound_manifest()
    for f in sounds:
        copy('sounds/' + f, out)
    with open(os.path.join(out, 'sounds', 'manifest.json'), 'w', encoding='utf-8') as f:
        json.dump(manifest, f, indent=1, sort_keys=True)

    api = args.api.strip()
    if api and not api.endswith('/'):
        api += '/'
    with open(os.path.join(out, 'js', 'config.js'), 'w', encoding='utf-8') as f:
        f.write('// tools/build_site.py állította elő: a többjátékos szerver címe\n')
        f.write('window.HF_CONFIG = %s;\n' % json.dumps({'api': api}))

    # A Cloudflare zóna a .js és .swf fájlokat 4 óráig a böngészőben hagyja (Browser Cache TTL), akkor
    # is, ha a Pages mást kér: egy frissítés után az újratöltött oldal is a régi hf.js-t és SWF-et
    # futtatná. Ezért a címükbe a tartalmuk hash-e kerül (?v=), minden változat új címet kap.
    bust(out, 'js/hf.js', "url: 'swf/testclient.swf'", 'swf/testclient.swf')
    for rel in ('vendor/ruffle/ruffle.js', 'js/config.js', 'js/hf.js'):
        bust(out, 'index.html', '<script src="%s">' % rel, rel)

    # Cloudflare Pages fejlécek: a tartalom-hash nevű Ruffle-fájlok egy évig tárolhatók, a többit
    # (index.html, SWF, kérdések, hangok) a böngésző minden betöltéskor ellenőrzi
    with open(os.path.join(out, '_headers'), 'w') as f:
        for rel in hashed:
            f.write('/%s\n  Cache-Control: public, max-age=31536000, immutable\n' % rel)
        f.write('/*\n  X-Content-Type-Options: nosniff\n  Referrer-Policy: strict-origin-when-cross-origin\n')

    total = sum(os.path.getsize(os.path.join(d, n)) for d, _, ns in os.walk(out) for n in ns)
    print('%s: %d fájl, %.1f MB; szerver: %s' % (os.path.relpath(out, ROOT),
          sum(len(ns) for _, _, ns in os.walk(out)), total / 1e6, api or '(nincs megadva: csak egyjátékos)'))


if __name__ == '__main__':
    main()
