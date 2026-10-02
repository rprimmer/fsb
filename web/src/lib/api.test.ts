import assert from 'node:assert/strict';
import { afterEach, test } from 'node:test';
import { streamList, streamSearch } from './api.ts';

const realFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = realFetch;
});

/** Makes fetch answer with these NDJSON lines. */
function serve(...lines: object[]) {
  globalThis.fetch = (async () =>
    new Response(lines.map((l) => JSON.stringify(l) + '\n').join(''), { headers: { 'content-type': 'application/x-ndjson' } })) as typeof fetch;
}

const signal = new AbortController().signal;
const entry = { name: 'a', isDir: false, size: 1, modTime: '2026-01-01T00:00:00Z', mode: 0o644 };

test('a listing that ends without its done line is an error, not a complete folder', async () => {
  serve({ path: '/x' }, { entries: [entry] });
  await assert.rejects(streamList('/x', () => {}, signal), /stopped answering before it finished/);
  serve({ path: '/x' }, { entries: [entry] }, { done: true });
  let n = 0;
  await streamList('/x', (es) => (n += es.length), signal);
  assert.equal(n, 1);
});

test('a search that ends without its done line is an error', async () => {
  serve({ path: '/x', query: 'a' }, { matches: [entry] });
  await assert.rejects(streamSearch('/x', 'a', false, () => {}, signal), /stopped answering before it finished/);
});

test('a search reports whether some folders could not be read completely', async () => {
  serve({ path: '/x', query: 'a' }, { done: true, visited: 3, truncated: false, incomplete: true });
  assert.deepEqual(await streamSearch('/x', 'a', false, () => {}, signal), { visited: 3, truncated: false, incomplete: true });
  serve({ path: '/x', query: 'a' }, { done: true, visited: 3, truncated: true });
  assert.deepEqual(await streamSearch('/x', 'a', false, () => {}, signal), { visited: 3, truncated: true, incomplete: false });
});
