import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { Row } from './api.ts';
import { sortRows, type SortOptions } from './sort.ts';

const row = (name: string, o: Partial<Row> = {}): Row => ({ name, isDir: false, size: 1, modTime: '2026-01-01T00:00:00Z', mode: 0o644, ...o });
const dir = (name: string, o: Partial<Row> = {}) => row(name, { isDir: true, size: 64, ...o });
const order = (rows: Row[], o: Partial<SortOptions> = {}) =>
  sortRows(rows, { key: 'name', asc: true, foldersFirst: false, ...o }).map((r) => r.name);

test('names sort case-insensitively, not in ASCII order', () => {
  assert.deepEqual(order([row('Zed'), row('app'), row('README'), row('alpha'), row('Bob')]), ['alpha', 'app', 'Bob', 'README', 'Zed']);
});

test('numbers inside names sort naturally', () => {
  assert.deepEqual(order([row('file10'), row('file2'), row('file1')]), ['file1', 'file2', 'file10']);
});

test('a leading dot sorts before letters and digits, for files and folders alike', () => {
  const rows = [row('zeta'), row('.bashrc'), dir('.claude'), row('alpha'), dir('src'), row('1st'), row('.zshrc'), dir('Documents')];
  assert.deepEqual(order(rows), ['.bashrc', '.claude', '.zshrc', '1st', 'alpha', 'Documents', 'src', 'zeta']);
});

test('by default folders are NOT grouped: they sit among the files by name', () => {
  const rows = [row('b.txt'), dir('c'), row('a.txt'), dir('a-dir'), row('d.txt')];
  assert.deepEqual(order(rows), ['a-dir', 'a.txt', 'b.txt', 'c', 'd.txt']);
});

test('Folders first groups folders, each group in name order', () => {
  const rows = [row('b.txt'), dir('c'), row('a.txt'), dir('a-dir'), row('.rc'), dir('.cfg')];
  assert.deepEqual(order(rows, { foldersFirst: true }), ['.cfg', 'a-dir', 'c', '.rc', 'a.txt', 'b.txt']);
});

test('Folders first does not flip with the sort direction', () => {
  const rows = [row('b'), dir('x'), row('a'), dir('y')];
  assert.deepEqual(order(rows, { foldersFirst: true, asc: true }), ['x', 'y', 'a', 'b']);
  assert.deepEqual(order(rows, { foldersFirst: true, asc: false }), ['y', 'x', 'b', 'a']);
});

test('a descending sort reverses the order', () => {
  assert.deepEqual(order([row('a'), row('c'), row('b')], { asc: false }), ['c', 'b', 'a']);
});

test('sorting by size treats a folder as smaller than any file, and the direction decides where it lands', () => {
  const rows = [row('big', { size: 500 }), dir('d', { size: 4096 }), row('tiny', { size: 0 }), row('mid', { size: 20 })];
  assert.deepEqual(order(rows, { key: 'size' }), ['d', 'tiny', 'mid', 'big']);
  assert.deepEqual(order(rows, { key: 'size', asc: false }), ['big', 'mid', 'tiny', 'd']);
});

test('Folders first applies to a size sort in the same way as to any other', () => {
  const rows = [row('big', { size: 500 }), dir('d', { size: 4096 }), row('tiny', { size: 1 })];
  assert.deepEqual(order(rows, { key: 'size', foldersFirst: true }), ['d', 'tiny', 'big']);
  assert.deepEqual(order(rows, { key: 'size', foldersFirst: true, asc: false }), ['d', 'big', 'tiny']);
});

test('sorting by modified time, and ties fall back to the name', () => {
  const rows = [
    row('b', { modTime: '2026-03-01T00:00:00Z' }),
    row('a', { modTime: '2026-03-01T00:00:00Z' }),
    row('old', { modTime: '2020-01-01T00:00:00Z' }),
    row('new', { modTime: '2026-09-01T00:00:00Z' }),
  ];
  assert.deepEqual(order(rows, { key: 'modTime' }), ['old', 'a', 'b', 'new']);
  assert.deepEqual(order(rows, { key: 'modTime', asc: false }), ['new', 'b', 'a', 'old']);
});

test('an invalid date does not break the ordering', () => {
  const rows = [row('b', { modTime: 'garbage' }), row('a', { modTime: '2026-01-01T00:00:00Z' }), row('c', { modTime: '' })];
  assert.deepEqual(order(rows, { key: 'modTime' }).sort(), ['a', 'b', 'c']);
  assert.equal(order(rows, { key: 'modTime' }).length, 3);
});

test('sorting by kind groups by kind, then by name', () => {
  const rows = [row('b.txt'), row('a.png'), dir('z'), row('a.txt')];
  assert.deepEqual(order(rows, { key: 'kind' }), ['z', 'a.png', 'a.txt', 'b.txt']); // Folder, PNG file, TXT file
});

test('search results sort by their path relative to the search root', () => {
  const rows = [row('x.txt', { rel: 'b/x.txt' }), row('x.txt', { rel: 'a/x.txt' })];
  assert.deepEqual(sortRows(rows, { key: 'name', asc: true, foldersFirst: false }).map((r) => r.rel), ['a/x.txt', 'b/x.txt']);
});

test('the input is not modified', () => {
  const rows = [row('b'), row('a')];
  sortRows(rows, { key: 'name', asc: true, foldersFirst: false });
  assert.deepEqual(rows.map((r) => r.name), ['b', 'a']);
});

test('numeric spellings that collate equal still sort the same whatever the input order', () => {
  const names = ['file1', 'file01', 'file001'];
  for (const asc of [true, false]) {
    const o: SortOptions = { key: 'name', asc, foldersFirst: false };
    const a = sortRows(names.map((n) => row(n)), o).map((r) => r.name);
    const b = sortRows([...names].reverse().map((n) => row(n)), o).map((r) => r.name);
    assert.deepEqual(a, b, `asc=${asc}`);
  }
});
