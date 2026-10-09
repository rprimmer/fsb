import assert from 'node:assert/strict';
import test from 'node:test';

import { fillImages, renderMarkdown, resolveLink } from './markdown.ts';

const base = '/Users/me/docs';

test('resolveLink: relative paths are resolved and normalized', () => {
  assert.deepEqual(resolveLink(base, 'a.md'), { kind: 'file', path: '/Users/me/docs/a.md' });
  assert.deepEqual(resolveLink(base, './sub/b.md#top'), { kind: 'file', path: '/Users/me/docs/sub/b.md' });
  assert.deepEqual(resolveLink(base, '../x.md?y=1'), { kind: 'file', path: '/Users/me/x.md' });
  assert.deepEqual(resolveLink(base, '/etc/hosts'), { kind: 'file', path: '/etc/hosts' });
  assert.deepEqual(resolveLink(base, 'my%20file.md'), { kind: 'file', path: '/Users/me/docs/my file.md' });
  assert.deepEqual(resolveLink('/a', '../../../../b'), { kind: 'file', path: '/b' }, '.. cannot climb above /');
});

test('resolveLink: only http(s) is external; every other scheme is dropped', () => {
  assert.equal(resolveLink(base, 'https://example.com/x').kind, 'external');
  assert.equal(resolveLink(base, 'http://example.com').kind, 'external');
  for (const bad of ['javascript:alert(1)', 'JaVaScRiPt:alert(1)', 'data:text/html,x', 'file:///etc/passwd', 'vbscript:x', 'ftp://x', '//evil.example/x', '#frag', '', '  ', 'a\\b', '%zz', 'a%00b']) {
    assert.equal(resolveLink(base, bad).kind, 'none', JSON.stringify(bad));
  }
});

test('formatting renders', () => {
  const { html } = renderMarkdown('# Title\n\nsome **bold** and `code`\n\n- a\n- b\n\n| x | y |\n|---|---|\n| 1 | 2 |\n', base);
  for (const want of ['<h1', 'Title', '<strong>bold</strong>', '<code>code</code>', '<li>a</li>', '<table>']) {
    assert.ok(html.includes(want), want + ' in ' + html);
  }
});

test('raw HTML is text, never markup', () => {
  const src = '<script>alert(1)</script>\n\ntext <img src=x onerror=alert(1)> more\n\n<div onclick="x">hi</div>\n';
  const { html } = renderMarkdown(src, base);
  assert.ok(!/<script/i.test(html) && !/<img/i.test(html) && !/<div/i.test(html), html);
  assert.ok(html.includes('&lt;script&gt;'));
});

test('links carry no href', () => {
  const { html, links } = renderMarkdown('[a](b.md) [c](https://e.com/x?q="1") [d](javascript:alert(1)) [e](<a b.md>) <https://auto.link>', base);
  assert.ok(!/\shref=/i.test(html), html);
  assert.ok(html.includes('data-fsb="file" data-fsb-link="0"'), html);
  assert.deepEqual(links[0], { kind: 'file', path: '/Users/me/docs/b.md' });
  assert.ok(html.includes('data-fsb="external"'));
  assert.ok(!/javascript/i.test(html.replace(/>[^<]*</g, '><')), 'the dropped link keeps only its text');
  assert.ok(html.includes('</a> d <a'), html);
});

// A link's target never goes through HTML: HTML parsing turns NUL, which marks
// a byte that is not UTF-8 (API-10), into U+FFFD, so a link from such a folder
// led elsewhere (independent review of 1.0.11, F1). The attribute holds only an
// index into `links`, as images do.
test('link targets stay out of the HTML, exactly as resolved', () => {
  const r = renderMarkdown('[s](sibling.txt) [p](100%25.txt) [w](https://e.com/a%20b)', '/tmp/caf\u0000E9');
  assert.deepEqual(r.links, [
    { kind: 'file', path: '/tmp/caf\u0000E9/sibling.txt' },
    { kind: 'file', path: '/tmp/caf\u0000E9/100%.txt' },
    { kind: 'external', url: 'https://e.com/a%20b' },
  ]);
  assert.ok(!r.html.includes('\u0000') && !r.html.includes('caf') && !r.html.includes('e.com'), r.html);
  assert.deepEqual([...r.html.matchAll(/data-fsb-link="(\d+)"/g)].map((m) => m[1]), ['0', '1', '2']);
});

test('quotes in link targets cannot break out of the attribute', () => {
  const { html } = renderMarkdown('[x](a"onmouseover="alert(1).md)\n\n[y](https://e.com/"><script>)', base);
  assert.ok(!/\sonmouseover=/.test(html), html);
  assert.ok(!/<script/i.test(html));
});

test('images never get a src; remote ones are blocked', () => {
  const r = renderMarkdown('![logo](img/a.png) ![r](https://evil.example/t.gif) ![p](//evil.example/t.gif) ![d](data:image/png;base64,AAAA)', base);
  assert.ok(!/src=/i.test(r.html), r.html);
  assert.deepEqual(r.images, ['/Users/me/docs/img/a.png']);
  assert.equal((r.html.match(/Remote image blocked/g) ?? []).length, 1);
  assert.equal((r.html.match(/Image not shown/g) ?? []).length, 2);
});

test('fillImages accepts only base64 raster data URLs', () => {
  const html = '<img data-fsb-img="0" alt="a"> <img data-fsb-img="1" alt="b"> <img data-fsb-img="2" alt="c">';
  const out = fillImages(html, ['data:image/png;base64,QUJD', 'data:image/svg+xml;base64,QUJD', null]);
  assert.ok(out.includes('<img src="data:image/png;base64,QUJD" alt="a">'));
  assert.ok(!out.includes('svg'));
  assert.equal((out.match(/Image not shown/g) ?? []).length, 2);
});
