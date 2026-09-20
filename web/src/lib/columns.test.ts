import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  COLUMN_IDS,
  DEFAULT_WIDTHS,
  MAX_WIDTH,
  MIN_WIDTH,
  clampWidth,
  defaultLayout,
  gridTemplate,
  isSortable,
  moveBy,
  moveColumn,
  parseLayout,
  totalWidth,
  type ColId,
} from './columns.ts';

const order: ColId[] = ['name', 'size', 'modTime', 'kind', 'mode'];

test('clampWidth', () => {
  assert.equal(clampWidth(10), MIN_WIDTH);
  assert.equal(clampWidth(99999), MAX_WIDTH);
  assert.equal(clampWidth(123.6), 124);
  assert.equal(clampWidth(NaN), MIN_WIDTH);
  assert.equal(clampWidth(Infinity), MIN_WIDTH);
});

test('moveColumn uses array-move semantics', () => {
  assert.deepEqual(moveColumn(order, 'name', 'kind'), ['size', 'modTime', 'kind', 'name', 'mode']);
  assert.deepEqual(moveColumn(order, 'mode', 'name'), ['mode', 'name', 'size', 'modTime', 'kind']);
  assert.deepEqual(moveColumn(order, 'size', 'modTime'), ['name', 'modTime', 'size', 'kind', 'mode']);
});

test('moveColumn ignores no-ops and unknown ids, and never mutates its input', () => {
  const copy = [...order];
  assert.equal(moveColumn(order, 'size', 'size'), order);
  assert.equal(moveColumn(order, 'size', 'nope' as ColId), order);
  assert.equal(moveColumn(order, 'nope' as ColId, 'size'), order);
  moveColumn(order, 'name', 'mode');
  assert.deepEqual(order, copy);
});

test('moveBy steps and stops at the ends', () => {
  assert.deepEqual(moveBy(order, 'size', -1), ['size', 'name', 'modTime', 'kind', 'mode']);
  assert.deepEqual(moveBy(order, 'size', 1), ['name', 'modTime', 'size', 'kind', 'mode']);
  assert.equal(moveBy(order, 'name', -1), order);
  assert.equal(moveBy(order, 'mode', 1), order);
  assert.deepEqual(moveBy(order, 'name', 99), ['size', 'modTime', 'kind', 'mode', 'name']);
});

test('every reorder keeps each column exactly once', () => {
  for (const a of COLUMN_IDS) {
    for (const b of COLUMN_IDS) {
      assert.deepEqual([...moveColumn(order, a, b)].sort(), [...order].sort(), `${a}->${b}`);
    }
  }
});

test('parseLayout falls back to defaults for junk', () => {
  const d = defaultLayout();
  for (const junk of [null, '', 'not json', '42', 'null', '"str"', '[]', '{}']) {
    assert.deepEqual(parseLayout(junk), d, String(junk));
  }
});

test('parseLayout round-trips a valid layout', () => {
  const l = defaultLayout();
  l.order = ['kind', 'name', 'size', 'mode', 'modTime'];
  l.widths.name = 500;
  assert.deepEqual(parseLayout(JSON.stringify(l)), l);
});

test('parseLayout rejects bad column lists but keeps good widths', () => {
  const bad = [
    ['name', 'size'], // too short
    ['name', 'name', 'size', 'kind', 'mode'], // duplicate
    ['name', 'size', 'modTime', 'kind', 'evil'], // unknown id
    'name,size,modTime,kind,mode', // wrong type
  ];
  for (const o of bad) {
    const l = parseLayout(JSON.stringify({ order: o, widths: { name: 400 } }));
    assert.deepEqual(l.order, order, JSON.stringify(o));
    assert.equal(l.widths.name, 400);
  }
});

test('parseLayout clamps and sanitizes widths', () => {
  const l = parseLayout(
    JSON.stringify({ order, widths: { name: 1, size: 1e9, modTime: 'wide', kind: null, mode: 200.4, extra: 5 } }),
  );
  assert.equal(l.widths.name, MIN_WIDTH);
  assert.equal(l.widths.size, MAX_WIDTH);
  assert.equal(l.widths.modTime, DEFAULT_WIDTHS.modTime); // non-number ignored
  assert.equal(l.widths.kind, DEFAULT_WIDTHS.kind);
  assert.equal(l.widths.mode, 200);
  assert.equal('extra' in l.widths, false);
});

test('parseLayout is not fooled by prototype keys', () => {
  const l = parseLayout('{"__proto__":{"order":["mode"]},"widths":{"__proto__":{"name":1}}}');
  assert.deepEqual(l, defaultLayout());
  assert.equal(({} as Record<string, unknown>).order, undefined);
});

test('gridTemplate follows the current order and ends with a flexible filler', () => {
  const l = defaultLayout();
  assert.equal(gridTemplate(l), '360px 90px 170px 130px 110px minmax(0, 1fr)');
  l.order = ['mode', 'name', 'size', 'modTime', 'kind'];
  assert.equal(gridTemplate(l), '110px 360px 90px 170px 130px minmax(0, 1fr)');
});

test('totalWidth adds gaps and padding to the column widths', () => {
  assert.equal(totalWidth(defaultLayout()), 360 + 90 + 170 + 130 + 110 + 12 * 5 + 32);
});

test('only Permissions is unsortable', () => {
  assert.deepEqual(COLUMN_IDS.filter((id) => !isSortable(id)), ['mode']);
});
