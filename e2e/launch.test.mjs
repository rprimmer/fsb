// The Chrome driver's own failure path, without a real browser. A Chrome that
// starts but never reports its DevTools endpoint must be killed when the launch
// gives up: the suite's after() has no driver to close it with, and a live
// Chrome holding its stderr pipe kept CI's test run from ever exiting.
import assert from 'node:assert/strict';
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, test } from 'node:test';
import { launchChrome } from './lib/chrome.mjs';
import { waitFor } from './lib/util.mjs';

const dir = mkdtempSync(join(tmpdir(), 'fsb-e2e-launch-'));
after(() => rmSync(dir, { recursive: true, force: true }));

const alive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
};

test('a Chrome that never reports its endpoint is killed when the launch gives up', async () => {
  const pidFile = join(dir, 'pid');
  const fake = join(dir, 'silent-chrome');
  // Like Chrome's crashpad handler, a helper it started shares its stderr.
  writeFileSync(fake, `#!/bin/sh\nsleep 300 &\necho $$ $! > '${pidFile}'\nexec sleep 300\n`);
  chmodSync(fake, 0o755);
  const saved = process.env.CHROME_BIN;
  process.env.CHROME_BIN = fake;
  try {
    await assert.rejects(launchChrome({ startTimeout: 1000 }), /did not report a DevTools endpoint/);
  } finally {
    if (saved === undefined) delete process.env.CHROME_BIN;
    else process.env.CHROME_BIN = saved;
  }
  const pids = readFileSync(pidFile, 'utf8').trim().split(' ').map(Number);
  try {
    await waitFor(() => !pids.some(alive), { timeout: 3000, message: 'the silent Chrome and its helper to be killed' });
  } finally {
    for (const pid of pids.filter(alive)) process.kill(pid, 'SIGKILL'); // don't let a failure hang this run too
  }
});
