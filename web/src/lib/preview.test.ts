import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  extOf,
  firstLines,
  formatFor,
  languageFor,
  looksLikeImage,
  parseDelimited,
  plural,
  prettyJSON,
} from './preview.ts';

test('extOf', () => {
  assert.equal(extOf('a.TXT'), 'txt');
  assert.equal(extOf('archive.tar.gz'), 'gz');
  assert.equal(extOf('.gitignore'), '', 'a leading dot is not an extension');
  assert.equal(extOf('trailing.'), '');
  assert.equal(extOf('noext'), '');
});

test('looksLikeImage is only a hint for the four safe raster types', () => {
  for (const n of ['a.png', 'A.JPG', 'a.jpeg', 'a.gif', 'a.webp']) assert.equal(looksLikeImage(n), true, n);
  for (const n of ['a.svg', 'a.html', 'a.txt', 'a.bmp', 'a.pdf', 'png', '.png']) assert.equal(looksLikeImage(n), false, n);
});

test('languageFor', () => {
  assert.equal(languageFor('main.go'), 'go');
  assert.equal(languageFor('App.TSX'), 'typescript');
  assert.equal(languageFor('Makefile'), 'makefile');
  assert.equal(languageFor('makefile'), 'makefile');
  assert.equal(languageFor('.zshrc'), 'bash');
  assert.equal(languageFor('notes.txt'), '');
  assert.equal(languageFor('README'), '');
});

test('formatFor', () => {
  assert.equal(formatFor('a.csv'), 'csv');
  assert.equal(formatFor('a.tsv'), 'tsv');
  assert.equal(formatFor('a.json'), 'json');
  assert.equal(formatFor('a.py'), 'code');
  assert.equal(formatFor('README.md'), 'markdown');
  assert.equal(formatFor('a.log'), 'text');
});

test('prettyJSON', () => {
  assert.equal(prettyJSON('{"a":1,"b":[1,2]}'), '{\n  "a": 1,\n  "b": [\n    1,\n    2\n  ]\n}');
  assert.equal(prettyJSON('{"a":1,'), null, 'a truncated head is not JSON');
  assert.equal(prettyJSON(''), null);
  assert.equal(prettyJSON('not json'), null);
});

test('parseDelimited handles quotes, embedded delimiters and newlines, and CRLF', () => {
  const csv = 'name,note\r\n"Smith, J","said ""hi"""\r\n"multi\nline",x\r\n';
  assert.deepEqual(parseDelimited(csv, ',', 100).rows, [
    ['name', 'note'],
    ['Smith, J', 'said "hi"'],
    ['multi\nline', 'x'],
  ]);
});

test('parseDelimited edge cases', () => {
  assert.deepEqual(parseDelimited('', ',', 10).rows, []);
  assert.deepEqual(parseDelimited('a,b', ',', 10).rows, [['a', 'b']], 'no trailing newline');
  assert.deepEqual(parseDelimited('a,,c\n', ',', 10).rows, [['a', '', 'c']], 'empty fields');
  assert.deepEqual(parseDelimited('a\tb\n1\t2\n', '\t', 10).rows, [['a', 'b'], ['1', '2']], 'tab-separated');
  assert.deepEqual(parseDelimited('a,"unterminated', ',', 10).rows, [['a', 'unterminated']], 'an unterminated quote does not hang or throw');
  assert.deepEqual(parseDelimited('x"y,z\n', ',', 10).rows, [['x"y', 'z']], 'a quote in the middle of a field is literal');
});

test('parseDelimited stops at maxRows and reports whether there was more', () => {
  const r = parseDelimited('1\n2\n3\n4\n', ',', 2);
  assert.deepEqual(r.rows, [['1'], ['2']]);
  assert.equal(r.more, true);
  const all = parseDelimited('1\n2\n', ',', 2);
  assert.deepEqual(all.rows, [['1'], ['2']]);
  assert.equal(all.more, false, 'nothing left after the last row');
});

test('firstLines', () => {
  assert.deepEqual(firstLines('a\nb\nc\n', 2, 100), { text: 'a\nb', cut: true });
  assert.deepEqual(firstLines('a\nb\n', 5, 100), { text: 'a\nb', cut: false });
  assert.deepEqual(firstLines('no newline', 5, 100), { text: 'no newline', cut: false });
  assert.deepEqual(firstLines('', 5, 100), { text: '', cut: false });
  assert.deepEqual(firstLines('abcdefghij', 5, 4), { text: 'abcd', cut: true });
  // Never split a surrogate pair when capping characters.
  const cut = firstLines('ab\u{1F600}cd', 5, 3);
  assert.equal(cut.text, 'ab');
  assert.equal(cut.cut, true);
});

test('plural', () => {
  assert.equal(plural(1, 'row'), '1 row');
  assert.equal(plural(2, 'row'), '2 rows');
  assert.equal(plural(1000, 'match', 'matches'), '1,000 matches');
});

test('formatFor: Markdown files get the rendered view', async () => {
  const { formatFor } = await import('./preview.ts');
  assert.equal(formatFor('README.md'), 'markdown');
  assert.equal(formatFor('notes.MARKDOWN'), 'markdown');
  assert.equal(formatFor('a.json'), 'json');
});

test('looksLikePdf goes by extension only as a hint', async () => {
  const { looksLikePdf } = await import('./preview.ts');
  assert.equal(looksLikePdf('a.PDF'), true);
  assert.equal(looksLikePdf('a.pdf.txt'), false);
  assert.equal(looksLikePdf('pdf'), false);
});

test('looksLikeArchive is only a hint from the extension', async () => {
  const { looksLikeArchive } = await import('./preview.ts');
  for (const n of ['a.zip', 'a.TAR', 'a.tgz', 'a.tar.gz', 'a.jar']) assert.equal(looksLikeArchive(n), true, n);
  for (const n of ['a.txt', 'zip', 'a.zip.txt']) assert.equal(looksLikeArchive(n), false, n);
});
