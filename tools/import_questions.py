#!/usr/bin/env python3
"""Builds questions.xml (the file the game loads) from the question lists, or adds questions to it.

    python3 tools/import_questions.py --rebuild          questions.xml újra a forrásokból:
                                                         questions_2013.csv + questions_extra.csv
    python3 tools/import_questions.py kerdesek.csv       kérdések hozzáadása (a meglévőket kihagyja)
    ... --dry-run                                        csak ellenőriz, nem ír

CSV (UTF-8; the separator may be ; , or tab; a first row starting with "kérdés" is a header,
lines starting with # are comments):
    # kategória: Földrajz                           -> a következő kérdések kategóriája
    # nehézség: könnyű (közepes, nehéz vagy 1-3)    -> a következő kérdések nehézsége (LEVEL)
    kérdés;helyes válasz;rossz 1;rossz 2;rossz 3      -> feleletválasztós kérdés
    kérdés;szám                                     -> tippkérdés (egész szám)
    kérdés;szám;legkisebb;legnagyobb                -> tippkérdés, a robotok tippjeinek határaival

Kategóriák (a játék előbb kategóriát sorsol, aztán kérdést, így mindegyik nagyjából egyforma
gyakran jön): Földrajz, Sport, Irodalom, Művészet, Természettudományok, Matematika/fizika,
Életmód/mindennapok, Szórakozás.

JSON: a list of
    {"question": "...", "answers": ["helyes", "rossz", "rossz", "rossz"], "category": "Sport", "level": 2}
    {"question": "...", "answer": 1987, "min": 1960, "max": 2000, "category": "Sport", "level": 3}

Questions already present (same text, ignoring case and spaces) are skipped.
"""
import csv
import io
import json
import os
import re
import sys
from xml.sax.saxutils import escape

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
QFILE = os.path.join(ROOT, 'questions.xml')
SOURCES = ['questions_2013.csv', 'questions_extra.csv']
ATTR = {'"': '&quot;', '\n': ' ', '\r': ' ', '\t': ' '}

# kategórianév eleje -> a CAT attribútum értéke
CATEGORIES = [('földrajz', 'foldrajz'), ('sport', 'sport'), ('irodalom', 'irodalom'),
              ('művészet', 'muveszet'), ('természettudomány', 'termtud'), ('biológia', 'termtud'),
              ('matematika', 'matfiz'), ('életmód', 'eletmod'), ('szórakozás', 'szorakozas')]
CAT_LINE = re.compile(r'^#\s*kategória\s*:\s*(.+?)\s*$', re.I)
LEVEL_LINE = re.compile(r'^#\s*nehézség\s*:\s*(.+?)\s*$', re.I)
LEVELS = {'könnyű': 1, 'közepes': 2, 'nehéz': 3, '1': 1, '2': 2, '3': 3}


def norm(s):
    return re.sub(r'\s+', ' ', s).strip().casefold()


def attr(s):
    return escape(re.sub(r'\s+', ' ', str(s)).strip(), ATTR)


def category(name):
    n = norm(name)
    for prefix, cat in CATEGORIES:
        if n.startswith(prefix):
            return cat
    raise ValueError('ismeretlen kategória: %r' % name)


def level(name):
    n = norm(str(name))
    if n not in LEVELS:
        raise ValueError('ismeretlen nehézség: %r (könnyű, közepes vagy nehéz)' % name)
    return LEVELS[n]


def to_int(s):
    s = str(s).strip().replace(' ', '').replace(' ', '')
    if not re.fullmatch(r'\d+', s):
        raise ValueError('nem egész szám: %r' % s)
    return int(s)


def from_row(row):
    row = [c.strip() for c in row]
    while row and row[-1] == '':
        row.pop()
    if not row:
        return None
    if len(row) == 5:
        return {'question': row[0], 'answers': row[1:5]}
    if len(row) in (2, 4):
        q = {'question': row[0], 'answer': to_int(row[1])}
        if len(row) == 4:
            q['min'], q['max'] = to_int(row[2]), to_int(row[3])
        return q
    raise ValueError('%d oszlop (5 kell a feleletválasztóshoz, 2 vagy 4 a tippkérdéshez)' % len(row))


def read_input(path):
    text = open(path, encoding='utf-8-sig').read()
    if path.lower().endswith('.json'):
        out = json.loads(text)
        for q in out:
            if q.get('category'):
                q['cat'] = category(q['category'])
            if q.get('level'):
                q['level'] = level(q['level'])
        return out
    sep = None
    cat = lvl = None
    out = []
    for n, line in enumerate(text.splitlines(), 1):
        m = CAT_LINE.match(line.strip())
        k = LEVEL_LINE.match(line.strip())
        if m or k:
            try:
                if m:
                    cat, lvl = category(m.group(1)), None
                else:
                    lvl = level(k.group(1))
            except ValueError as e:
                sys.exit('%s, %d. sor: %s' % (path, n, e))
            continue
        if not line.strip() or line.lstrip().startswith('#'):
            continue                                   # üres sor, megjegyzés
        if sep is None:
            sep = ';' if ';' in line else ('\t' if '\t' in line else ',')
        row = next(csv.reader([line], delimiter=sep))
        if not out and row and norm(row[0]) in ('kérdés', 'kerdes', 'question'):
            continue                                   # fejléc
        try:
            q = from_row(row)
        except ValueError as e:
            sys.exit('%s, %d. sor: %s' % (path, n, e))
        if q:
            if cat:
                q['cat'] = cat
            if lvl:
                q['level'] = lvl
            out.append(q)
    return out


def to_xml(q):
    text = q.get('question', '')
    if not str(text).strip():
        raise ValueError('üres kérdés')
    cat = ' CAT="%s"' % q['cat'] if q.get('cat') else ''
    if q.get('level'):
        cat += ' LEVEL="%d"' % q['level']
    if 'answers' in q:
        ans = q['answers']
        if len(ans) != 4 or any(not str(a).strip() for a in ans):
            raise ValueError('pontosan 4 nem üres válasz kell (az első a helyes)')
        if len({norm(str(a)) for a in ans}) != 4:
            raise ValueError('két egyforma válasz')
        return '<QUESTION QUESTION="%s" %s%s />' % (attr(text), ' '.join(
            'A%d="%s"' % (i + 1, attr(a)) for i, a in enumerate(ans)), cat)
    n = to_int(q['answer'])
    extra = ''
    if 'min' in q or 'max' in q:
        lo, hi = to_int(q['min']), to_int(q['max'])
        if not lo <= n <= hi:
            raise ValueError('a válasz nincs a határok között')
        extra = ' MINVAL="%d" MAXVAL="%d"' % (lo, hi)
    return '<TIPQUESTION QUESTION="%s" ANSWER="%d"%s%s />' % (attr(text), n, extra, cat)


def convert(questions, seen, where):
    """XML-sorok a kérdésekből; a már látott (seen) szövegűeket kihagyja."""
    lines, mc, tip, dup = [], 0, 0, 0
    for i, q in enumerate(questions):
        key = norm(attr(q.get('question', '')))
        if key in seen:
            dup += 1
            continue
        try:
            line = to_xml(q)
        except (ValueError, KeyError) as e:
            sys.exit('%s, %d. kérdés (%s): %s' % (where, i + 1, q.get('question', '?'), e))
        seen.add(key)
        lines.append(line)
        if line.startswith('<QUESTION'):
            mc += 1
        else:
            tip += 1
    return lines, mc, tip, dup


def stats(lines):
    count = {}
    for line in lines:
        m = re.search(r' CAT="([^"]*)"', line)
        lv = re.search(r' LEVEL="(\d)"', line)
        kind = 'mc' if line.startswith('<QUESTION') else 'tip'
        for key in ((m.group(1) if m else '-', kind), ('szint', kind, lv.group(1) if lv else '-')):
            count[key] = count.get(key, 0) + 1
    print('  %-11s %20s %24s' % ('', 'feleletválasztós', 'tipp'))
    for cat in sorted({k[0] for k in count if k[0] != 'szint'}):
        print('  %-11s %20d %24d' % (cat, count.get((cat, 'mc'), 0), count.get((cat, 'tip'), 0)))
    levels = sorted({k[2] for k in count if k[0] == 'szint'})
    print('  %-11s %20s %24s' % ('nehézség', ' / '.join('%d' % count.get(('szint', 'mc', l), 0) for l in levels),
                                 ' / '.join('%d' % count.get(('szint', 'tip', l), 0) for l in levels)))


def main():
    args = [a for a in sys.argv[1:] if not a.startswith('--')]
    dry = '--dry-run' in sys.argv
    if '--rebuild' in sys.argv:
        seen, lines = set(), []
        for src in SOURCES:
            got, mc, tip, dup = convert(read_input(os.path.join(ROOT, src)), seen, src)
            print('%s: %d feleletválasztós + %d tippkérdés%s' % (src, mc, tip, ', kétszer szerepel: %d' % dup if dup else ''))
            lines += got
        stats(lines)
        if not dry:
            with open(QFILE, 'w', encoding='utf-8') as f:
                f.write('\n'.join(lines) + '\n')
            print('kész: %s' % os.path.relpath(QFILE, ROOT))
        return
    if len(args) != 1:
        sys.exit(__doc__)
    existing = open(QFILE, encoding='utf-8').read()
    seen = {norm(m) for m in re.findall(r'QUESTION="([^"]*)"', existing)}
    lines, mc, tip, dup = convert(read_input(args[0]), seen, args[0])
    print('új: %d feleletválasztós + %d tippkérdés, már megvolt: %d' % (mc, tip, dup))
    if dry or not lines:
        return
    with open(QFILE, 'a', encoding='utf-8') as f:
        if not existing.endswith('\n'):
            f.write('\n')
        f.write('\n'.join(lines) + '\n')
    print('hozzáadva: %s' % os.path.relpath(QFILE, ROOT))


if __name__ == '__main__':
    main()
