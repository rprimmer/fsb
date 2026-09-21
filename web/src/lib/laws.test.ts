// Property tests for the laws in algebra/ (sections on pure frontend logic).
// A fixed-seed generator keeps failures reproducible.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { Row } from './api.ts';
import { matchesName } from './filter.ts';
import { parseHash, pathToHash } from './format.ts';
import { resolveGoto, withinRoots } from './goto.ts';
import { resolveLink } from './markdown.ts';
import { compareRows, sortRows, type SortOptions } from './sort.ts';

function rng(seed: number) {
  let s = seed >>> 0;
  return () => {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
    return s / 2 ** 32;
  };
}
const R = rng(42);
const pick = <T>(a: T[]): T => a[Math.floor(R() * a.length)];

const NAMES = ['a', 'A', 'b', '.a', '.B', '1', '10', '2', 'file2', 'file10', 'Straße', 'strasse', 'éa', 'éa', 'ﬁle', 'file', 'z z', 'Ω', 'ω'];
const TIMES = ['2026-01-01T00:00:00Z', '2026-06-01T12:00:00.123456789+02:00', '0001-01-01T00:00:00Z', 'not a date', '', '2026-01-01T00:00:00Z'];

function randomRows(n: number): Row[] {
  const used = new Set<string>();
  const rows: Row[] = [];
  while (rows.length < n) {
    const name = pick(NAMES) + (R() < 0.5 ? '' : String(Math.floor(R() * 5)));
    // A directory cannot hold two names that differ only in normalization (APFS).
    if (used.has(name.normalize('NFC'))) continue;
    used.add(name.normalize('NFC'));
    const isDir = R() < 0.4;
    rows.push({ name, isDir, size: isDir ? 64 : pick([0, 1, 1, 5, 2 ** 40]), modTime: pick(TIMES), mode: 0o644 });
  }
  return rows;
}

const OPTIONS: SortOptions[] = [];
for (const key of ['name', 'size', 'modTime', 'kind'] as const)
  for (const asc of [true, false]) for (const foldersFirst of [true, false]) OPTIONS.push({ key, asc, foldersFirst });

const sign = (n: number) => (n > 0 ? 1 : n < 0 ? -1 : 0);

// Law: the row order is a total preorder (antisymmetric, transitive, total), for
// every key, direction and grouping, even when a modified time cannot be parsed.
test('law: compareRows is antisymmetric and transitive', () => {
  for (let i = 0; i < 40; i++) {
    const rows = randomRows(9);
    for (const o of OPTIONS) {
      for (const a of rows)
        for (const b of rows) {
          assert.equal(sign(compareRows(a, b, o)) + sign(compareRows(b, a, o)), 0, `antisymmetry ${JSON.stringify(o)} ${a.name} ${b.name}`);
          for (const c of rows)
            if (compareRows(a, b, o) <= 0 && compareRows(b, c, o) <= 0)
              assert.ok(compareRows(a, c, o) <= 0, `transitivity ${JSON.stringify(o)}: ${a.name} ${a.modTime} <= ${b.name} ${b.modTime} <= ${c.name} ${c.modTime}`);
        }
    }
  }
});

// Law: the sorted order does not depend on the order the server sent the rows in.
test('law: sortRows is independent of input order', () => {
  for (let i = 0; i < 40; i++) {
    const rows = randomRows(10);
    const shuffled = [...rows].sort(() => R() - 0.5);
    for (const o of OPTIONS) {
      assert.deepEqual(sortRows(rows, o).map((r) => r.name), sortRows(shuffled, o).map((r) => r.name), JSON.stringify(o));
    }
  }
});

// Law: with "Folders first", every folder precedes every file, for every key
// and in both directions.
test('law: Folders first groups folders under every key and direction', () => {
  for (let i = 0; i < 40; i++) {
    const rows = randomRows(12);
    for (const o of OPTIONS.filter((x) => x.foldersFirst)) {
      const out = sortRows(rows, o);
      const firstFile = out.findIndex((r) => !r.isDir);
      if (firstFile >= 0) assert.ok(out.slice(firstFile).every((r) => !r.isDir), JSON.stringify(o));
    }
  }
});

// Law: reversing the direction reverses the order (when nothing is grouped).
test('law: descending is the reverse of ascending', () => {
  for (let i = 0; i < 40; i++) {
    const rows = randomRows(10);
    for (const key of ['name', 'size', 'modTime', 'kind'] as const) {
      const up = sortRows(rows, { key, asc: true, foldersFirst: false }).map((r) => r.name);
      const down = sortRows(rows, { key, asc: false, foldersFirst: false }).map((r) => r.name);
      assert.deepEqual(down, [...up].reverse(), key);
    }
  }
});

// Law: the filter box finds exactly the names that filename search finds. The
// server folds names like APFS does (full Unicode case folding), so the filter
// must treat these spellings as equal too.
test('law: the filter agrees with search on names APFS treats as equal', () => {
  const same: [string, string][] = [
    ['Straße.txt', 'strasse'], ['strasse.txt', 'STRAßE'], ['ſecret', 'secret'], ['x.Key', 'KEY'],
    ['ﬁle.txt', 'file'], ['file.txt', 'ﬁ'], ['ﬃ', 'ffi'], ['Café', 'café'], ['ÉCOLE', 'école'], ['ẞ', 'ss'],
  ];
  for (const [name, query] of same) assert.equal(matchesName(name, query, false), true, `${name} / ${query}`);
  assert.equal(matchesName('Straße.txt', 'strasse', true), false, 'match case stays exact');
  assert.equal(matchesName('abc', 'abd', false), false);
});

// Law: paths survive the trip through the location hash.
test('law: parseHash inverts pathToHash', () => {
  const parts = ['a', 'b c', 'd%e', 'f#g', 'h?i', 'j&k=l', 'ü', 'é', '日本', ' lead', 'trail ', 'q"r', "s'", 'select=x', '..', '.'];
  for (let i = 0; i < 300; i++) {
    const path = '/' + Array.from({ length: 1 + Math.floor(R() * 4) }, () => pick(parts)).join('/');
    const select = R() < 0.5 ? '' : pick(parts);
    assert.deepEqual(parseHash(pathToHash(path, select)), { path, select }, path + ' ' + select);
  }
});

// Law: the go-to box yields a clean absolute path, idempotently, and never
// climbs above the root.
test('law: resolveGoto is clean and idempotent', () => {
  const inputs = ['/a/b', '/a//b/', '/a/./b', '/a/../..', '/../../x', '~', '~/x/../y', '/a/b/../../../c', '  /x  ', '/'];
  for (const s of inputs) {
    const r = resolveGoto(s, '/Users/me');
    if ('error' in r) continue;
    assert.ok(r.path.startsWith('/') && !r.path.split('/').some((c, i) => i > 0 && (c === '' && r.path !== '/' || c === '.' || c === '..')), r.path);
    assert.deepEqual(resolveGoto(r.path, '/Users/me'), r, s);
  }
});

// Law: a Markdown link can only ever name an absolute, normalized path.
test('law: resolveLink yields normalized absolute file paths or nothing', () => {
  const hrefs = ['a.md', '../a.md', '../../../../a', './x/./y/../z', '/abs/../up', 'a%2F..%2F..%2Fb', 'sub/', '%2e%2e/%2e%2e/x', 'a b', 'ü.md'];
  for (const h of hrefs) {
    const t = resolveLink('/Users/me/docs', h);
    if (t.kind !== 'file') continue;
    assert.ok(t.path.startsWith('/') && !t.path.includes('//') && !t.path.split('/').includes('..') && !t.path.split('/').includes('.'), `${h} -> ${t.path}`);
  }
});

test('law: withinRoots is prefix-closed and case-insensitive', () => {
  assert.equal(withinRoots('/Users/me/x', ['/users/ME']), true);
  assert.equal(withinRoots('/Users/mex', ['/Users/me']), false, 'a sibling that shares a prefix is outside');
  assert.equal(withinRoots('/anything', ['/']), true);
});

// Law: names are compared as the file system compares them wherever the client
// compares paths: roots, breadcrumbs and the go-to check.
test('law: roots and breadcrumbs treat names that APFS equates as equal', async () => {
  const { crumbsFor } = await import('./format.ts');
  assert.equal(withinRoots('/USERS/Me/Straße', ['/Users/me']), true);
  assert.equal(withinRoots('/Users/me/x', ['/Users/ME']), true);
  assert.equal(withinRoots('/Users/mé', ['/Users/me']), false);
  assert.equal(withinRoots('/users/meK', ['/Users/mek']), true, 'the Kelvin sign is a k');
  const c = crumbsFor('/users/ME/Docs', ['/Users/me']);
  assert.equal(c[0].path, '/Users/me');
  assert.deepEqual(c.map((x) => x.label), ['/Users/me', 'Docs']);
});

test('law: breadcrumbs split by components when a folded spelling has another length', async () => {
  const { crumbsFor } = await import('./format.ts');
  const c = crumbsFor('/Users/STRASSE/a/b', ['/Users/Straße']);
  assert.deepEqual(c.map((x) => x.label), ['/Users/Straße', 'a', 'b']);
  assert.deepEqual(crumbsFor('/x/y', ['/']).map((x) => x.label), ['/', 'x', 'y']);
});

// Law: every request is relative to the prefix the page was loaded from, so the
// session cookie (scoped to that prefix) accompanies it.
test('law: apiBase keeps the launch prefix', async () => {
  const { apiBase } = await import('./base.ts');
  assert.equal(apiBase('/abc_DEF-123/'), '/abc_DEF-123/');
  assert.equal(apiBase('/abc_DEF-123/index.html'), '/abc_DEF-123/');
  assert.equal(apiBase('/'), '/');
});
