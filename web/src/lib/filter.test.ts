import assert from 'node:assert/strict';
import { test } from 'node:test';
import { matchesName } from './filter.ts';

test('an empty or blank query matches everything', () => {
  assert.equal(matchesName('anything', '', false), true);
  assert.equal(matchesName('anything', '   ', true), true);
});

test('by default case is ignored', () => {
  assert.equal(matchesName('Report-final.txt', 'report', false), true);
  assert.equal(matchesName('report-draft.txt', 'REPORT', false), true);
  assert.equal(matchesName('notes.txt', 'report', false), false);
});

test('with Match case, case matters', () => {
  assert.equal(matchesName('Report-final.txt', 'Report', true), true);
  assert.equal(matchesName('report-draft.txt', 'Report', true), false);
  assert.equal(matchesName('REPORT.txt', 'report', true), false);
});

test('it is a substring match, and surrounding spaces in the query are ignored', () => {
  assert.equal(matchesName('my-report.txt', '  port ', false), true);
  assert.equal(matchesName('my report.txt', 'y r', false), true);
});

test('accented names match however they are stored, with and without Match case', () => {
  const decomposed = 'Cafe\u0301 Menu.txt';
  const composed = 'Caf\u00e9';
  for (const matchCase of [false, true]) {
    assert.equal(matchesName(decomposed, composed, matchCase), true, `decomposed name, composed query, matchCase=${matchCase}`);
    assert.equal(matchesName('Caf\u00e9 Menu.txt', 'Cafe\u0301', matchCase), true, `composed name, decomposed query, matchCase=${matchCase}`);
  }
  assert.equal(matchesName(decomposed, 'caf\u00e9', true), false, 'Match case still tells C from c');
});

test('regular-expression characters in the query are literal', () => {
  assert.equal(matchesName('a.b', 'a.b', false), true);
  assert.equal(matchesName('aXb', 'a.b', false), false);
  assert.equal(matchesName('(x)', '(x)', false), true);
});
