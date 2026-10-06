import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mapLimit, readCapped } from './fetchcap.ts';

/** A response whose body yields the given chunks and records whether it was canceled. */
function streamed(chunks: number[], headers: Record<string, string> = {}) {
  const state = { pulled: 0, canceled: false };
  let i = 0;
  const body = new ReadableStream<Uint8Array>({
    pull(c) {
      if (i < chunks.length) {
        state.pulled += chunks[i];
        c.enqueue(new Uint8Array(chunks[i++]));
      } else c.close();
    },
    cancel() {
      state.canceled = true;
    },
  });
  return { r: new Response(body, { headers: { 'content-type': 'image/png', ...headers } }), state };
}

test('readCapped returns the body when it fits, typed from the response', async () => {
  const { r } = streamed([3, 4, 3]);
  const b = await readCapped(r, 10);
  assert.equal(b?.size, 10, 'exactly at the cap is allowed');
  assert.equal(b?.type, 'image/png');
});

test('readCapped refuses on a declared length over the cap without reading the body', async () => {
  const { r, state } = streamed([5, 5, 5], { 'content-length': '15' });
  assert.equal(await readCapped(r, 10), null);
  assert.equal(state.pulled <= 5, true, `read ${state.pulled} bytes`);
});

test('readCapped stops a body that crosses the cap, whatever the header says', async () => {
  const cases: Record<string, string>[] = [{}, { 'content-length': '4' }];
  for (const headers of cases) {
    const { r, state } = streamed([4, 4, 4, 4, 4, 4, 4, 4], headers);
    assert.equal(await readCapped(r, 10), null);
    assert.equal(state.canceled, true, 'the rest of the body is canceled');
    assert.ok(state.pulled < 32, `read ${state.pulled} of 32 bytes`);
  }
});

test('readCapped with a chunk that alone crosses the cap', async () => {
  const { r, state } = streamed([11]);
  assert.equal(await readCapped(r, 10), null);
  assert.equal(state.canceled, true);
});

test('mapLimit keeps order and never runs more than n at once', async () => {
  let running = 0;
  let peak = 0;
  const out = await mapLimit([5, 1, 4, 2, 3, 0, 6], 3, async (x) => {
    running++;
    peak = Math.max(peak, running);
    await new Promise((res) => setTimeout(res, x));
    running--;
    return x * 10;
  });
  assert.deepEqual(out, [50, 10, 40, 20, 30, 0, 60]);
  assert.equal(peak, 3);
});
