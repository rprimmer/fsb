#!/usr/bin/env python3
"""Checks that every test the specifications cite exists.

Reads the traceability table of the design specification and the \\Tests{...}
citations of the algebraic one, and looks each citation up:

  \\code{TestX} / \\code{FuzzX} / \\code{BenchmarkX}   a Go function in a *_test.go file
  e2e: ``title''                                    a test title in e2e/suite.mjs
  web: name.test.ts (``title'')                     the file, and the title in it
  linux: ``label''                                  a label printed by e2e/linux/smoke.sh

A title may be cut short with \\ldots; the part before it must appear.  Prints
each citation that is not found and exits 1 if there is any.

  specs/tools/check-traceability.py
"""
import glob
import os
import re
import sys

ROOT = os.path.normpath(os.path.join(os.path.dirname(__file__), '..', '..'))
SOURCES = [
    'specs/design/sections/11-traceability.tex',
    *sorted(glob.glob(os.path.join(ROOT, 'specs/algebra/sections/*.tex'))),
]


def read(path):
    with open(os.path.join(ROOT, path), encoding='utf-8') as f:
        return f.read()


def detex(s):
    """LaTeX text of a title as it appears in the source of the tests."""
    s = s.split(r'\ldots')[0]
    s = s.replace(r'\textasciitilde', '~').replace(r'\_', '_').replace(r'\#', '#')
    s = s.replace(r'\&', '&').replace(r'\%', '%').replace('{}', '')
    s = re.sub(r'\\code\{([^}]*)\}', r'\1', s)
    s = s.replace('``', '"').replace("''", '"')
    return s.strip().rstrip('.').strip()


go_funcs = set()
for path in glob.glob(os.path.join(ROOT, '**/*_test.go'), recursive=True):
    if '/node_modules/' in path:
        continue
    with open(path, encoding='utf-8') as f:
        go_funcs.update(re.findall(r'^func ((?:Test|Fuzz|Benchmark)\w*)\(', f.read(), re.M))
suite = read('e2e/suite.mjs').replace("\\'", "'")  # titles are JS strings: fsb\'s
smoke = read('e2e/linux/smoke.sh')

missing = []
for src in SOURCES:
    rel = os.path.relpath(src if os.path.isabs(src) else os.path.join(ROOT, src), ROOT)
    text = read(rel)
    for name in sorted(set(re.findall(r'\\code\{((?:Test|Fuzz|Benchmark)[A-Za-z0-9_\\]*)\}', text))):
        name = name.replace('\\_', '_').replace('\\', '')
        if name not in go_funcs:
            missing.append(f'{rel}: Go test {name}')
    # Every title of a list (e2e: ``a'', ``b'' and ``c''), not only the first.
    for seg in re.findall(r'e2e:?\s*((?:``.+?\'\'(?:,?\s*(?:and\s+)?(?=``))?)+)', text):
        for title in re.findall(r'``(.+?)\'\'', seg):
            t = detex(title)
            if t and t not in suite:
                missing.append(f'{rel}: e2e test "{t}"')
    for m in re.finditer(r'web:\s*([\w.]+\.test\.ts)(?:\}?\s*\(([^)]*)\))?', text):
        fname = m.group(1)
        path = os.path.join(ROOT, 'web/src/lib', fname)
        if not os.path.exists(path):
            missing.append(f'{rel}: web test file {fname}')
            continue
        body = read(os.path.relpath(path, ROOT))
        for title in re.findall(r'``(.+?)\'\'', m.group(2) or ''):
            t = detex(title)
            if t and t not in body:
                missing.append(f'{rel}: web test "{t}" in {fname}')
    for seg in re.findall(r'linux:\s*((?:``.+?\'\'(?:,\s*)?)+)', text):
        for label in re.findall(r'``(.+?)\'\'', seg):
            t = detex(label)
            if t and t not in smoke:
                missing.append(f'{rel}: linux check "{t}"')

for m in missing:
    print(m)
print(f'{len(missing)} citation(s) not found' if missing else 'every cited test exists')
sys.exit(1 if missing else 0)
