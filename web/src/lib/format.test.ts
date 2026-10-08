// Run with `npm test` (Node's built-in runner strips the types; no dependencies).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  basename,
  copyablePath,
  crumbsFor,
  dirname,
  displayName,
  formatSize,
  hashToPath,
  joinPath,
  kindOf,
  modeString,
  parseHash,
  pathToHash,
  queryPath,
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

test('a hash can carry a selection, and awkward names survive', () => {
  for (const [path, select] of [
    ['/Users/me/docs', 'a file.txt'],
    ['/a', '100% real?.md'],
    ['/a/b?c', 'x&y=z#w'],
    ['/café', 'résumé + notes.txt'],
    ['/', 'etc'],
  ] as const) {
    assert.deepEqual(parseHash(pathToHash(path, select)), { path, select }, `${path} ${select}`);
  }
  assert.deepEqual(parseHash(pathToHash('/a/b')), { path: '/a/b', select: '' });
  assert.equal(pathToHash('/a', ''), '#/a', 'an empty selection adds nothing');
  assert.equal(pathToHash('/a', 'b c'), '#/a?select=b%20c');
  // A "?" in the path is encoded, so it can never be mistaken for the options.
  assert.equal(pathToHash('/a?b'), '#/a%3Fb');
  assert.equal(hashToPath('#/a/b?select=x'), '/a/b');
  assert.deepEqual(parseHash('#/a?select=%E0%A4%A'), { path: '/a', select: '' }, 'a malformed selection is ignored, the folder is kept');
  assert.deepEqual(parseHash('#/bad%E0%A4%A?select=x'), { path: '', select: '' }, 'a malformed path is rejected');
  assert.deepEqual(parseHash('#/a?other=1&select=b'), { path: '/a', select: 'b' });
});

test('dirname and basename', () => {
  assert.equal(dirname('/a/b/c'), '/a/b');
  assert.equal(dirname('/a'), '/');
  assert.equal(dirname('/'), '/');
  assert.equal(basename('/a/b/c.txt'), 'c.txt');
  assert.equal(basename('/a'), 'a');
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

// The server sends each byte of a name that is not UTF-8 as NUL and two hex
// digits ("caf\u0000E9.txt" for Latin-1 "café.txt"); see server/wire.go.
const latin1 = '/home/u/caf\u0000E9.txt';

test('queryPath sends escaped bytes as themselves and everything else as UTF-8', () => {
  assert.equal(queryPath(latin1), '%2Fhome%2Fu%2Fcaf%E9.txt');
  assert.equal(queryPath('/a/café & b?.txt'), encodeURIComponent('/a/café & b?.txt'));
  assert.equal(queryPath('\u0000FF\u0000FE'), '%FF%FE');
  // A NUL that is not an escape is passed on as one (the server refuses it).
  assert.equal(queryPath('a\u0000zz'), 'a%00zz');
});

test('displayName shows escaped bytes as a marker', () => {
  assert.equal(displayName('caf\u0000E9.txt'), 'caf\u20390xE9\u203a.txt');
  assert.equal(displayName('\u0000FF\u0000FE'), '\u20390xFF\u203a\u20390xFE\u203a');
  assert.equal(displayName('a\u0000zz'), 'a\u2039U+0000\u203azz');
  assert.equal(displayName('café.txt'), 'café.txt');
});

test('copyablePath quotes a path with escaped bytes for the shell', () => {
  assert.equal(copyablePath('/home/u/notes.txt'), '/home/u/notes.txt');
  assert.equal(copyablePath(latin1), "$'/home/u/caf\\xE9.txt'");
  assert.equal(copyablePath("/it's\\\u0000E9"), "$'/it\\'s\\\\\\xE9'");
});

test('an escaped path survives the address bar', () => {
  assert.equal(hashToPath(pathToHash(latin1)), latin1);
});
