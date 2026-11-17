#!/usr/bin/env python3
"""Checks that every test and file the specifications, README.md and SECURITY.md cite exists.

Reads every section of the three specifications and looks each citation up:

  \\code{TestX} / \\code{FuzzX} / \\code{BenchmarkX}   a Go function in a *_test.go file
  e2e: ``title''                                    a test title in e2e/suite.mjs
  web: name.test.ts (``title'')                     the file, and the title in it
  linux: ``label''                                  a label printed by e2e/linux/smoke.sh
  \\code{dir/file.ext}, \\code{file.ext}             a file in the repository (below)

and, in the algebra's \\Tests{...}, each item separated by ";":

  rules/laws_test.go::TestA, TestB                  the file (under internal/ or the
                                                    root), and each function defined in it
  web/src/lib/x.test.ts: title (or ::title)         the file, and the title in it
  e2e/linux/crossuser.sh, format.test.ts            the file

A path in \\code{} is checked when its first part is a folder of the
repository, at the root or under internal/ (guard/x_test.go): it must be a
tracked file or folder, or one Git ignores (an output folder such as
e2e/artifacts/linux). A bare name with a source extension must be the name of
some tracked file. Other paths (/proc, ~/.ssh, net/http, api/list) name no
file of this repository and are skipped. Files are judged by `git ls-files`,
so the answer is the same in a fresh checkout.

README.md and specs/SECURITY.md are read too: each word of a code span is
checked like a \\code{} path (also relative to the document's own folder), a
`TestX` must exist, and every relative link must lead to a file or folder of
the repository. Fenced code blocks, whole commands to type, are skipped.

A title may be cut short with \\ldots; the part before it must appear, and in a
path \\ldots stands for any part.  Prints each citation that is not found and
exits 1 if there is any.

  specs/tools/check-traceability.py
"""
import fnmatch
import glob
import os
import re
import subprocess
import sys

ROOT = os.path.normpath(os.path.join(os.path.dirname(__file__), '..', '..'))
SOURCES = sorted(
    os.path.relpath(p, ROOT)
    for d in ('functional', 'design', 'algebra')
    for p in glob.glob(os.path.join(ROOT, 'specs', d, 'sections', '*.tex'))
)
MARKDOWN = ['README.md', 'specs/SECURITY.md']
SOURCE_EXT = ('.go', '.mjs', '.ts', '.sh', '.yml', '.py', '.tex', '.md', '.svelte', '.html', '.json', '.mod', '.sum')


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


def git(*args):
    return subprocess.run(['git', '-C', ROOT, *args], capture_output=True, text=True, check=False)


tracked = git('ls-files').stdout.splitlines()
if not tracked:
    sys.exit('check-traceability: git ls-files listed nothing; run it in a Git checkout')
tracked_set = set(tracked)
tracked_dirs = {os.path.dirname(p) for p in tracked}
tracked_dirs |= {d for p in tracked_dirs for d in [p.rsplit('/', i)[0] for i in range(p.count('/') + 1)]}
tracked_names = {os.path.basename(p) for p in tracked}
top_level = {p.split('/')[0] for p in tracked}


def repo_path_exists(path):
    """A tracked file or folder, or a path Git ignores; \\ldots matches anything."""
    path = path.rstrip('/')
    if '*' in path:
        return any(fnmatch.fnmatch(p, path) for p in tracked)
    if path in tracked_set or path in tracked_dirs:
        return True
    return git('check-ignore', '-q', '--no-index', path).returncode == 0


def cited_path_missing(p, bases):
    """True when p names a folder of the repository under one of bases (''
    is the root) but nothing there; False when it resolves, or names no
    repository folder at all (/proc, ~/.ssh, net/http, api/list)."""
    p = p.removeprefix('./')
    if p.startswith(('/', '~', '#')) or '://' in p:
        return False
    seg = p.split('/')[0]
    cands = []
    for base in bases:
        c = os.path.normpath(os.path.join(base, p))
        if c.startswith('..') or c.split('/')[0] not in top_level:
            continue
        under = os.path.normpath(os.path.join(base, seg))
        if base and seg != '..' and under not in tracked_dirs and under not in tracked_set:
            continue
        cands.append(c)
    return bool(cands) and not any(repo_path_exists(c) for c in cands)


go_funcs = {}  # test function -> the files that define it
for path in tracked:
    if path.endswith('_test.go'):
        for name in re.findall(r'^func ((?:Test|Fuzz|Benchmark)\w*)\(', read(path), re.M):
            go_funcs.setdefault(name, set()).add(path)
suite = read('e2e/suite.mjs').replace("\\'", "'")  # titles are JS strings: fsb\'s
smoke = read('e2e/linux/smoke.sh')


def check_cited_file(rel, p, bases, missing):
    if '/' in p:
        if cited_path_missing(p, bases):
            missing.append(f'{rel}: file {p}')
    elif p.endswith(SOURCE_EXT) and re.fullmatch(r'[\w-]+(\.[\w-]+)*', p) and p not in tracked_names:
        missing.append(f'{rel}: file named {p}')


def tests_items(text):
    """The items of each \\Tests{...}, which may hold one level of braces."""
    for body in re.findall(r'\\Tests\{((?:[^{}]|\{[^{}]*\})*)\}', text):
        for item in body.split(';'):
            yield item.strip()


def check_tests_item(rel, item, missing):
    item = item.replace(r'\_', '_')
    # A Go test file, relative to internal/ or to the root.
    m = re.match(r'([\w/]+_test\.go)\b', item)
    if m:
        cands = [p for p in ('internal/' + m.group(1), m.group(1)) if p in tracked_set]
        if not cands:
            missing.append(f'{rel}: Go test file {m.group(1)}')
            return
        for name in re.findall(r'\b((?:Test|Fuzz|Benchmark)\w+)', item[m.end():]):
            if not go_funcs.get(name, set()) & set(cands):
                missing.append(f'{rel}: Go test {name} in {cands[0]}')
        return
    # A web test file, perhaps with a title after ":" or "::".
    m = re.match(r'(?:web/src/lib/)?([\w.]+\.test\.ts)\b:{0,2}\s*(.*)', item)
    if m:
        path = 'web/src/lib/' + m.group(1)
        if path not in tracked_set:
            missing.append(f'{rel}: web test file {m.group(1)}')
            return
        title = detex(m.group(2)) if not m.group(2).startswith('(') else ''
        if title and title not in read(path):
            missing.append(f'{rel}: web test "{title}" in {m.group(1)}')
        return
    # Any other repository path, such as e2e/linux/crossuser.sh.
    m = re.match(r'([\w.-]+(?:/[\w.-]+)+)', item)
    if m and cited_path_missing(m.group(1), ['', 'internal']):
        missing.append(f'{rel}: file {m.group(1)}')
    # A Go test named alone must exist somewhere.
    if not m:
        for name in re.findall(r'^((?:Test|Fuzz|Benchmark)\w+)', item):
            if name not in go_funcs:
                missing.append(f'{rel}: Go test {name}')


missing = []
for rel in SOURCES:
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
        path = 'web/src/lib/' + fname
        if path not in tracked_set:
            missing.append(f'{rel}: web test file {fname}')
            continue
        body = read(path)
        for title in re.findall(r'``(.+?)\'\'', m.group(2) or ''):
            t = detex(title)
            if t and t not in body:
                missing.append(f'{rel}: web test "{t}" in {fname}')
    for seg in re.findall(r'linux:\s*((?:``.+?\'\'(?:,\s*)?)+)', text):
        for label in re.findall(r'``(.+?)\'\'', seg):
            t = detex(label)
            if t and t not in smoke:
                missing.append(f'{rel}: linux check "{t}"')
    for item in tests_items(text):
        check_tests_item(rel, item, missing)
    # Files named in \code{}.
    for raw in sorted(set(re.findall(r'\\code\{([^{}]*)\}', text))):
        check_cited_file(rel, raw.replace(r'\_', '_').replace(r'\ldots', '*'), ['', 'internal'], missing)

# The Markdown documents: code spans (each word of a command too) and links.
# Fenced blocks are whole commands to type, not citations.
for rel in MARKDOWN:
    here = os.path.dirname(rel)
    text = re.sub(r'^```.*?^```', '', read(rel), flags=re.M | re.S)
    for span in sorted(set(re.findall(r'`([^`\n]+)`', text))):
        for word in span.split():
            if re.fullmatch(r'(?:Test|Fuzz|Benchmark)\w+', word):
                if word not in go_funcs:
                    missing.append(f'{rel}: Go test {word}')
            else:
                check_cited_file(rel, word, ['', here, 'internal'], missing)
    for target in sorted(set(re.findall(r'\]\(([^)\s]+)\)', text))):
        target = target.split('#')[0]
        if not target or re.match(r'[a-z]+:', target):
            continue
        resolved = os.path.normpath(os.path.join(here, target))
        if resolved.startswith('..') or not repo_path_exists(resolved):
            missing.append(f'{rel}: link to {target}')

for m in missing:
    print(m)
print(f'{len(missing)} citation(s) not found' if missing else 'every cited test and file exists')
sys.exit(1 if missing else 0)
