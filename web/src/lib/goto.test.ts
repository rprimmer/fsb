import assert from 'node:assert/strict';
import { test } from 'node:test';
import { resolveGoto, withinRoots } from './goto.ts';

const HOME = '/Users/me';

test('an absolute path is accepted and cleaned', () => {
  assert.deepEqual(resolveGoto('/Users/me/Documents', HOME), { path: '/Users/me/Documents' });
  assert.deepEqual(resolveGoto('  /Users/me/Documents/  ', HOME), { path: '/Users/me/Documents' });
  assert.deepEqual(resolveGoto('/Users//me/./Documents', HOME), { path: '/Users/me/Documents' });
  assert.deepEqual(resolveGoto('/Users/me/Documents/../Desktop', HOME), { path: '/Users/me/Desktop' });
  assert.deepEqual(resolveGoto('/', HOME), { path: '/' });
});

test('.. cannot climb above the root of the filesystem', () => {
  assert.deepEqual(resolveGoto('/../../etc', HOME), { path: '/etc' });
  assert.deepEqual(resolveGoto('/..', HOME), { path: '/' });
});

test('~ expands to the home folder', () => {
  assert.deepEqual(resolveGoto('~', HOME), { path: '/Users/me' });
  assert.deepEqual(resolveGoto('~/', HOME), { path: '/Users/me' });
  assert.deepEqual(resolveGoto('~/Documents', HOME), { path: '/Users/me/Documents' });
  assert.deepEqual(resolveGoto('~/Documents/../Desktop', HOME), { path: '/Users/me/Desktop' });
});

test('names with spaces and odd characters are kept as typed', () => {
  assert.deepEqual(resolveGoto('/Users/me/My Documents/café #1', HOME), { path: '/Users/me/My Documents/café #1' });
});

test('problems come back as messages, not as guesses', () => {
  for (const bad of ['', '   ']) assert.match((resolveGoto(bad, HOME) as { error: string }).error, /Type a folder path/);
  for (const rel of ['Documents', './Documents', '../x', 'a/b']) {
    assert.match((resolveGoto(rel, HOME) as { error: string }).error, /absolute path/, rel);
  }
  assert.match((resolveGoto('~someone/x', HOME) as { error: string }).error, /not another user/);
  assert.match((resolveGoto('/a\0b', HOME) as { error: string }).error, /not a valid path/);
  assert.match((resolveGoto('~/x', '') as { error: string }).error, /home folder is not known/);
});

test('withinRoots is lexical, case-insensitive, and respects folder boundaries', () => {
  const roots = ['/Users/me', '/Volumes/Data'];
  assert.equal(withinRoots('/Users/me', roots), true);
  assert.equal(withinRoots('/Users/me/a/b', roots), true);
  assert.equal(withinRoots('/users/ME/A', roots), true);
  assert.equal(withinRoots('/Volumes/Data/x', roots), true);
  assert.equal(withinRoots('/Users/meow', roots), false, 'a sibling that shares a prefix is not inside');
  assert.equal(withinRoots('/etc', roots), false);
  assert.equal(withinRoots('/', roots), false);
  assert.equal(withinRoots('/anything', ['/']), true, 'a root of / contains everything');
  assert.equal(withinRoots('/anything', ['/', '/Users/me']), true);
});
