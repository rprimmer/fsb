import assert from 'node:assert/strict';
import { test } from 'node:test';
import { DEFAULT_PANE_WIDTH, MAX_PANE_WIDTH, MIN_PANE_WIDTH, RESERVED_FOR_LIST, clampPaneWidth, parsePrefs } from './prefs.ts';

test('clampPaneWidth without a window width falls back to the defensive absolute ceiling', () => {
  assert.equal(clampPaneWidth(10), MIN_PANE_WIDTH);
  assert.equal(clampPaneWidth(99999), MAX_PANE_WIDTH);
  assert.equal(clampPaneWidth(400.4), 400);
  assert.equal(clampPaneWidth(NaN), DEFAULT_PANE_WIDTH);
  assert.equal(clampPaneWidth(Infinity), DEFAULT_PANE_WIDTH);
});

test('clampPaneWidth with a window width grows the ceiling with the window, not a fixed number', () => {
  // A generous, maximized window: the pane may take almost all of it.
  assert.equal(clampPaneWidth(3000, 3200), 3200 - RESERVED_FOR_LIST);
  assert.equal(clampPaneWidth(99999, 3200), 3200 - RESERVED_FOR_LIST);
  // A narrow window: never below the floor, even if that leaves less than
  // RESERVED_FOR_LIST for the list.
  assert.equal(clampPaneWidth(3000, 300), MIN_PANE_WIDTH);
  // An ordinary window: the ceiling tracks it exactly.
  assert.equal(clampPaneWidth(3000, 1400), 1400 - RESERVED_FOR_LIST);
  assert.equal(clampPaneWidth(500, 1400), 500); // well within bounds: unaffected
});

test('defaults depend on the window width for the preview pane only', () => {
  assert.deepEqual(parsePrefs(null, 1400), { preview: true, hover: true, previewWidth: DEFAULT_PANE_WIDTH, foldersFirst: false, matchCase: false });
  assert.deepEqual(parsePrefs(null, 500), { preview: false, hover: true, previewWidth: DEFAULT_PANE_WIDTH, foldersFirst: false, matchCase: false });
  assert.equal(parsePrefs(null, 900).preview, true);
  assert.equal(parsePrefs(null, 899).preview, false);
});

test('junk falls back to defaults', () => {
  const d = parsePrefs(null, 1400);
  for (const junk of ['', 'not json', '42', 'null', '"x"', '[]', '{}']) assert.deepEqual(parsePrefs(junk, 1400), d, junk);
});

test('a valid save round-trips', () => {
  const saved = { preview: false, hover: false, previewWidth: 555, foldersFirst: true, matchCase: true };
  assert.deepEqual(parsePrefs(JSON.stringify(saved), 1400), saved);
});

test('each field is validated on its own', () => {
  const p = parsePrefs(JSON.stringify({ preview: 'yes', hover: false, previewWidth: 'wide' }), 1400);
  assert.deepEqual(p, { preview: true, hover: false, previewWidth: DEFAULT_PANE_WIDTH, foldersFirst: false, matchCase: false });
  assert.equal(parsePrefs(JSON.stringify({ previewWidth: 5 }), 1400).previewWidth, MIN_PANE_WIDTH);
  assert.equal(parsePrefs(JSON.stringify({ previewWidth: 1e9 }), 1400).previewWidth, 1400 - RESERVED_FOR_LIST);
  assert.equal(parsePrefs(JSON.stringify({ previewWidth: null }), 1400).previewWidth, DEFAULT_PANE_WIDTH);
});

test('an old save without a width still loads', () => {
  assert.deepEqual(parsePrefs('{"preview":true,"hover":false}', 500), { preview: true, hover: false, previewWidth: DEFAULT_PANE_WIDTH, foldersFirst: false, matchCase: false });
});

test('Folders first is off unless saved as on, and only a boolean counts', () => {
  assert.equal(parsePrefs(null, 1400).foldersFirst, false);
  assert.equal(parsePrefs('{"foldersFirst":true}', 1400).foldersFirst, true);
  assert.equal(parsePrefs('{"foldersFirst":"yes"}', 1400).foldersFirst, false);
});

test('Match case is off unless saved as on, and only a boolean counts', () => {
  assert.equal(parsePrefs(null, 1400).matchCase, false);
  assert.equal(parsePrefs('{"matchCase":true}', 1400).matchCase, true);
  assert.equal(parsePrefs('{"matchCase":1}', 1400).matchCase, false);
});
