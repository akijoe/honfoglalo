#!/usr/bin/env python3
"""A térképválasztó kis képei (img/maps/XX.png) a SWF térképeiből.

A MAP sprite (#680) MAP_XX címkéjű képkockáin van a 13 ország; mindegyiknek az alapja egy alakzat
a megyehatárokkal (lent). Ezt SVG-be rajzoljuk (tools/shape2svg.py), képpé alakítjuk (rsvg-convert),
és a játék krémszínére festjük (ImageMagick).

    python3 tools/map_thumbs.py
"""
import os
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(__file__))
import shape2svg

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SWF = os.path.join(ROOT, 'archive', 'swf', 'testclient.swf')

# térkép -> az ország alakzata (a MAP sprite képkockáin; Olaszországnál a 585 a tengeri útvonal)
BASES = {'HU': 48, 'FR': 125, 'BG': 179, 'BE': 230, 'RS': 277, 'RU': 326, 'CZ': 376, 'DE': 423,
         'UA': 471, 'RO': 519, 'SI': 567, 'IT': 616, 'SK': 663}


def main():
    out = os.path.join(ROOT, 'img', 'maps')
    os.makedirs(out, exist_ok=True)
    with tempfile.TemporaryDirectory() as tmp:
        for mapid, cid in BASES.items():
            svg = os.path.join(tmp, mapid + '.svg')
            png = os.path.join(tmp, mapid + '.png')
            shape2svg.main(SWF, cid, svg)
            subprocess.run(['rsvg-convert', svg, '-o', png], check=True)
            subprocess.run(['magick', png, '-trim', '+repage', '-channel', 'RGB', '-colorspace', 'gray',
                            '-auto-level', '+level-colors', '#5b3816,#f7e9c4', '+channel',
                            '-resize', '112x80', '-strip', os.path.join(out, mapid + '.png')], check=True)
            print(mapid, 'ok')


if __name__ == '__main__':
    main()
