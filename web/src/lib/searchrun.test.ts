import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { Row } from './api.ts';
import { startSearch, type SearchSink } from './searchrun.ts';

const row = (name: string): Row => ({ name, isDir: false, size: 1, modTime: '2026-01-01T00:00:00Z', mode: 0o644 });
const tick = (ms: number) => new Promise((res) => setTimeout(res, ms));

/** A search stream the test drives: send rows, then finish or fail. */
function controlled() {
  let onRows!: (r: Row[]) => void;
  let finish!: (v: { visited: number; truncated: boolean }) => void;
  let fail!: (e: unknown) => void;
  let signal!: AbortSignal;
  const stream = (on: (r: Row[]) => void, s: AbortSignal) => {
    onRows = on;
    signal = s;
    return new Promise<{ visited: number; truncated: boolean }>((res, rej) => {
      finish = res;
      fail = rej;
    });
  };
  return { stream, send: (r: Row[]) => onRows(r), finish: (t = false) => finish({ visited: 0, truncated: t }), fail: (e: unknown) => fail(e), aborted: () => signal.aborted };
}

function recorder() {
  const log: string[] = [];
  const sink: SearchSink = {
    rows: (r) => log.push('rows:' + r.map((x) => x.name).join(',')),
    done: (t) => log.push('done:' + t),
    error: (e) => log.push('error:' + String(e)),
  };
  return { log, sink };
}

test('results arrive: the first batch at once, later ones batched, all on completion', async () => {
  const c = controlled();
  const { log, sink } = recorder();
  startSearch(c.stream, sink, 20);
  c.send([row('a')]);
  c.send([row('b')]);
  c.send([row('c')]);
  assert.deepEqual(log, ['rows:a']);
  await tick(40);
  assert.deepEqual(log, ['rows:a', 'rows:a,b,c']);
  c.send([row('d')]);
  c.finish(true);
  await tick(0);
  assert.deepEqual(log, ['rows:a', 'rows:a,b,c', 'rows:a,b,c,d', 'done:true']);
});

// The review's reproduction: a batch waits on the flush timer when the search
// is replaced; the old search must never write into the new one's results.
test('a canceled search never delivers anything again, even a pending flush', async () => {
  const old = controlled();
  const o = recorder();
  const cancel = startSearch(old.stream, o.sink, 20);
  old.send([row('old-first')]);
  old.send([row('old-second')]); // waiting on the timer
  cancel();
  assert.equal(old.aborted(), true, 'the request is aborted');
  await tick(40);
  old.send([row('late')]);
  old.finish();
  await tick(0);
  assert.deepEqual(o.log, ['rows:old-first']);
});

test('a canceled search reports no error when its request fails afterwards', async () => {
  const c = controlled();
  const { log, sink } = recorder();
  const cancel = startSearch(c.stream, sink, 20);
  cancel();
  c.fail(new Error('aborted'));
  await tick(0);
  assert.deepEqual(log, []);
});

test('a failure delivers what arrived, then the error', async () => {
  const c = controlled();
  const { log, sink } = recorder();
  startSearch(c.stream, sink, 20);
  c.send([row('a')]);
  c.send([row('b')]);
  c.fail('boom');
  await tick(0);
  assert.deepEqual(log, ['rows:a', 'rows:a,b', 'error:boom']);
});

test('canceling twice, or after completion, is harmless', async () => {
  const c = controlled();
  const { log, sink } = recorder();
  const cancel = startSearch(c.stream, sink, 20);
  c.finish();
  await tick(0);
  cancel();
  cancel();
  assert.deepEqual(log, ['done:false']);
});
