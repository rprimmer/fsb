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
  isHidden,
  isSortable,
  moveBy,
  moveColumn,
  parseLayout,
  toggleColumn,
  totalWidth,
  visibleOrder,
  type ColId,
} from './columns.ts';

const order: ColId[] = ['name', 'size', 'modTime', 'kind', 'mode', 'xattr'];

test('clampWidth', () => {
  assert.equal(clampWidth(10), MIN_WIDTH);
  assert.equal(clampWidth(99999), MAX_WIDTH);
  assert.equal(clampWidth(123.6), 124);
  assert.equal(clampWidth(NaN), MIN_WIDTH);
  assert.equal(clampWidth(Infinity), MIN_WIDTH);
});

test('the attributes column is last and hidden by default', () => {
  const l = defaultLayout();
  assert.deepEqual(l.order, order);
  assert.deepEqual(l.hidden, ['xattr']);
  assert.deepEqual(visibleOrder(l), ['name', 'size', 'modTime', 'kind', 'mode']);
});

test('moveColumn uses array-move semantics', () => {
  assert.deepEqual(moveColumn(order, 'name', 'kind'), ['size', 'modTime', 'kind', 'name', 'mode', 'xattr']);
  assert.deepEqual(moveColumn(order, 'mode', 'name'), ['mode', 'name', 'size', 'modTime', 'kind', 'xattr']);
  assert.deepEqual(moveColumn(order, 'size', 'modTime'), ['name', 'modTime', 'size', 'kind', 'mode', 'xattr']);
});

test('moveColumn ignores no-ops and unknown ids, and never mutates its input', () => {
  const copy = [...order];
  assert.equal(moveColumn(order, 'size', 'size'), order);
  assert.equal(moveColumn(order, 'size', 'nope' as ColId), order);
  assert.equal(moveColumn(order, 'nope' as ColId, 'size'), order);
  moveColumn(order, 'name', 'mode');
  assert.deepEqual(order, copy);
});

test('every reorder keeps each column exactly once', () => {
  for (const a of COLUMN_IDS) {
    for (const b of COLUMN_IDS) {
      assert.deepEqual([...moveColumn(order, a, b)].sort(), [...order].sort(), `${a}->${b}`);
    }
  }
});

test('moveBy steps over visible columns and stops at the ends', () => {
  const l = defaultLayout(); // xattr hidden
  assert.deepEqual(moveBy(l, 'size', -1), ['size', 'name', 'modTime', 'kind', 'mode', 'xattr']);
  assert.deepEqual(moveBy(l, 'size', 1), ['name', 'modTime', 'size', 'kind', 'mode', 'xattr']);
  assert.equal(moveBy(l, 'name', -1), l.order);
  assert.equal(moveBy(l, 'mode', 1), l.order, 'mode is the last visible column: the hidden one after it is not a target');
  assert.equal(moveBy(l, 'xattr', -1), l.order, 'a hidden column has no visible position to move from');
});

test('moveBy skips hidden columns so a keypress always changes what is shown', () => {
  const l = { ...defaultLayout(), hidden: ['modTime', 'xattr'] as ColId[] };
  // visible: name size kind mode; moving kind left must jump past hidden modTime to before size
  assert.deepEqual(moveBy(l, 'kind', -1), ['name', 'kind', 'size', 'modTime', 'mode', 'xattr']);
  const moved = { ...l, order: moveBy(l, 'kind', -1) };
  assert.deepEqual(visibleOrder(moved), ['name', 'kind', 'size', 'mode']);
});

test('toggleColumn hides and shows, but never the name', () => {
  let l = defaultLayout();
  l = toggleColumn(l, 'xattr');
  assert.equal(isHidden(l, 'xattr'), false);
  assert.deepEqual(visibleOrder(l).at(-1), 'xattr');
  l = toggleColumn(l, 'kind');
  assert.equal(isHidden(l, 'kind'), true);
  assert.equal(toggleColumn(l, 'name'), l);
  assert.equal(isHidden(l, 'name'), false);
  assert.ok(visibleOrder({ ...l, hidden: [...COLUMN_IDS] }).includes('name'), 'name survives even a hand-edited hidden list');
});

test('parseLayout falls back to defaults for junk', () => {
  const d = defaultLayout();
  for (const junk of [null, '', 'not json', '42', 'null', '"str"', '[]', '{}']) {
    assert.deepEqual(parseLayout(junk), d, String(junk));
  }
});

test('parseLayout round-trips a valid layout', () => {
  const l = defaultLayout();
  l.order = ['kind', 'name', 'size', 'mode', 'xattr', 'modTime'];
  l.widths.name = 500;
  l.hidden = ['mode'];
  assert.deepEqual(parseLayout(JSON.stringify(l)), l);
});

test('parseLayout upgrades an old five-column save without losing the user order', () => {
  const old = { order: ['kind', 'name', 'size', 'modTime', 'mode'], widths: { name: 420, size: 80, modTime: 150, kind: 100, mode: 90 } };
  const l = parseLayout(JSON.stringify(old));
  assert.deepEqual(l.order, ['kind', 'name', 'size', 'modTime', 'mode', 'xattr']);
  assert.equal(l.widths.name, 420);
  assert.equal(l.widths.xattr, DEFAULT_WIDTHS.xattr);
  assert.deepEqual(l.hidden, ['xattr'], 'the new column starts hidden');
});

test('parseLayout repairs bad column lists instead of discarding the whole layout', () => {
  const cases: [unknown, ColId[]][] = [
    [['name', 'size'], ['name', 'size', 'modTime', 'kind', 'mode', 'xattr']], // too short: missing appended
    [['name', 'name', 'size', 'kind', 'mode'], ['name', 'size', 'kind', 'mode', 'modTime', 'xattr']], // duplicate dropped
    [['mode', 'evil', 'name', 7, null], ['mode', 'name', 'size', 'modTime', 'kind', 'xattr']], // unknown/typed junk dropped
    ['name,size', order], // wrong type: default order
    [[], order],
  ];
  for (const [o, want] of cases) {
    const l = parseLayout(JSON.stringify({ order: o, widths: { name: 400 } }));
    assert.deepEqual(l.order, want, JSON.stringify(o));
    assert.equal(l.widths.name, 400);
    assert.equal([...new Set(l.order)].length, COLUMN_IDS.length, 'each column appears exactly once');
  }
});

test('parseLayout clamps and sanitizes widths', () => {
  const l = parseLayout(
    JSON.stringify({ order, widths: { name: 1, size: 1e9, modTime: 'wide', kind: null, mode: 200.4, extra: 5 } }),
  );
  assert.equal(l.widths.name, MIN_WIDTH);
  assert.equal(l.widths.size, MAX_WIDTH);
  assert.equal(l.widths.modTime, DEFAULT_WIDTHS.modTime);
  assert.equal(l.widths.kind, DEFAULT_WIDTHS.kind);
  assert.equal(l.widths.mode, 200);
  assert.equal('extra' in l.widths, false);
});

test('parseLayout sanitizes the hidden list and never hides the name', () => {
  assert.deepEqual(parseLayout(JSON.stringify({ hidden: ['name', 'kind', 'evil', 5] })).hidden, ['kind']);
  assert.deepEqual(parseLayout(JSON.stringify({ hidden: [] })).hidden, [], 'an explicit empty list means show everything');
  assert.deepEqual(parseLayout(JSON.stringify({ hidden: 'kind' })).hidden, ['xattr'], 'a non-array falls back to the default');
});

test('parseLayout is not fooled by prototype keys', () => {
  const l = parseLayout('{"__proto__":{"order":["mode"]},"widths":{"__proto__":{"name":1}}}');
  assert.deepEqual(l, defaultLayout());
  assert.equal(({} as Record<string, unknown>).order, undefined);
});

test('gridTemplate covers only the visible columns, in order, plus a filler', () => {
  const l = defaultLayout();
  assert.equal(gridTemplate(l), '360px 90px 170px 130px 110px minmax(0, 1fr)');
  l.order = ['mode', 'name', 'size', 'modTime', 'kind', 'xattr'];
  assert.equal(gridTemplate(l), '110px 360px 90px 170px 130px minmax(0, 1fr)');
  assert.equal(gridTemplate(toggleColumn(l, 'xattr')), '110px 360px 90px 170px 130px 240px minmax(0, 1fr)');
});

test('totalWidth counts only visible columns', () => {
  assert.equal(totalWidth(defaultLayout()), 360 + 90 + 170 + 130 + 110 + 12 * 5 + 32);
  assert.equal(totalWidth(toggleColumn(defaultLayout(), 'xattr')), 360 + 90 + 170 + 130 + 110 + 240 + 12 * 6 + 32);
});

test('only Permissions and Attributes are unsortable', () => {
  assert.deepEqual(COLUMN_IDS.filter((id) => !isSortable(id)), ['mode', 'xattr']);
});
