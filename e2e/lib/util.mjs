import { basename } from 'node:path';

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/** Polls fn until it returns something truthy, or throws with the last value after the timeout. */
export async function waitFor(fn, { timeout = 8000, interval = 50, message = 'condition' } = {}) {
  const deadline = Date.now() + timeout;
  let last;
  for (;;) {
    try {
      last = await fn();
      if (last) return last;
    } catch (e) {
      last = e;
    }
    if (Date.now() > deadline) {
      throw new Error(`timed out after ${timeout}ms waiting for ${message} (last: ${last instanceof Error ? last.message : JSON.stringify(last)})`);
    }
    await sleep(interval);
  }
}

/** "#/Users/me/My%20Docs" for an absolute path, as the app builds it. */
export const hashFor = (path) => '#' + path.split('/').map(encodeURIComponent).join('/');

export { basename };
