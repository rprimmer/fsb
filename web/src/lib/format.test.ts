// Run with `npm test` (Node's built-in runner strips the types; no dependencies).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  crumbsFor,
  formatSize,
  hashToPath,
  joinPath,
  kindOf,
  modeString,
  pathToHash,
} from './format.ts';

const file = { name: 'a.txt', isDir: false, size: 1, modTime: '', mode: 0o644 };

test('formatSize', () => {
  assert.equal(formatSize(0), '0 B');
  assert.equal(formatSize(1023), '1023 B');
  assert.equal(formatSize(1024), '1.0 KB');
  assert.equal(formatSize(1536), '1.5 KB');
  assert.equal(formatSize(5 * 1024 * 1024), '5.0 MB');
  assert.equal(formatSize(150 * 1024 * 1024), '150 MB');
});

test('modeString', () => {
  assert.equal(modeString(file), '-rw-r--r--');
  assert.equal(modeString({ ...file, isDir: true, mode: 0o755 }), 'drwxr-xr-x');
  assert.equal(modeString({ ...file, isSymlink: true, mode: 0o777 }), 'lrwxrwxrwx');
  // High (type) bits of Go's fs.FileMode must not leak into the permission string.
  assert.equal(modeString({ ...file, isDir: true, mode: 0x80000000 + 0o700 }), 'drwx------');
});

test('kindOf', () => {
  assert.equal(kindOf(file), 'TXT file');
  assert.equal(kindOf({ ...file, name: 'Makefile' }), 'File');
  assert.equal(kindOf({ ...file, name: '.gitignore' }), 'File'); // a leading dot is not an extension
  assert.equal(kindOf({ ...file, name: 'trailing.' }), 'File');
  assert.equal(kindOf({ ...file, isDir: true }), 'Folder');
  assert.equal(kindOf({ ...file, isDir: true, isSymlink: true }), 'Folder link');
  assert.equal(kindOf({ ...file, isSymlink: true, broken: true }), 'Broken link');
});

test('joinPath', () => {
  assert.equal(joinPath('/', 'etc'), '/etc');
  assert.equal(joinPath('/Users/me', 'docs'), '/Users/me/docs');
});

test('hash round trip, including awkward characters', () => {
  for (const p of ['/Users/me', '/Users/me/My Folder', '/a/b#c', '/a/100%', '/café résumé', '/a/b?c=d&e', '/']) {
    assert.equal(hashToPath(pathToHash(p)), p, p);
  }
  assert.equal(pathToHash('/a/b #c'), '#/a/b%20%23c');
});

test('hashToPath rejects non-paths and malformed escapes', () => {
  assert.equal(hashToPath(''), '');
  assert.equal(hashToPath('#'), '');
  assert.equal(hashToPath('#relative/path'), '');
  assert.equal(hashToPath('#/bad%E0%A4%A'), '');
});

test('crumbsFor starts at the enclosing root', () => {
  const roots = ['/Users/me', '/Volumes/Data'];
  assert.deepEqual(crumbsFor('/Users/me', roots), [{ label: '/Users/me', path: '/Users/me' }]);
  assert.deepEqual(crumbsFor('/Users/me/a/b', roots), [
    { label: '/Users/me', path: '/Users/me' },
    { label: 'a', path: '/Users/me/a' },
    { label: 'b', path: '/Users/me/a/b' },
  ]);
  assert.deepEqual(crumbsFor('/Volumes/Data/x', roots).map((c) => c.label), ['/Volumes/Data', 'x']);
  // A sibling that merely shares a prefix is not inside the root.
  assert.deepEqual(crumbsFor('/Users/meow/x', roots), [{ label: '/Users/meow/x', path: '/Users/meow/x' }]);
  // With nested roots the longest one wins; "/" contains everything.
  assert.deepEqual(crumbsFor('/Users/me/a', ['/', '/Users/me']).map((c) => c.label), ['/Users/me', 'a']);
  assert.deepEqual(crumbsFor('/etc/x', ['/']).map((c) => c.label), ['/', 'etc', 'x']);
});
