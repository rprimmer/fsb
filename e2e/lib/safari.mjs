// A Safari driver over W3C WebDriver (safaridriver). Dependency-free.
//
// One-time setup, which only you can do: Safari > Settings > Advanced > "Show
// features for web developers", then Developer > "Allow remote automation".
// (CI can use `sudo safaridriver --enable`.)
import { spawn, spawnSync } from 'node:child_process';
import { createServer } from 'node:net';
import { writeFileSync } from 'node:fs';
import { sleep } from './util.mjs';

// W3C "normalised key" code points (private-use characters, written as numbers
// so they stay visible in the source).
const wdKey = (codePoint) => String.fromCharCode(codePoint);
const KEYS = {
  ' ': wdKey(0xe00d),
  ArrowDown: wdKey(0xe015),
  ArrowUp: wdKey(0xe013),
  ArrowLeft: wdKey(0xe012),
  ArrowRight: wdKey(0xe014),
  Enter: wdKey(0xe007),
  Escape: wdKey(0xe00c),
  Backspace: wdKey(0xe003),
  c: 'c',
  s: 's',
  g: 'g',
  '/': '/',
};
const ALT = wdKey(0xe00a);
const SHIFT = wdKey(0xe008);

const freePort = () =>
  new Promise((resolve, reject) => {
    const srv = createServer();
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
    srv.on('error', reject);
  });

export async function launchSafari({ width = 1280, height = 900 } = {}) {
  const port = await freePort();
  const child = spawn('safaridriver', ['-p', String(port)], { stdio: 'ignore' });
  const root = `http://127.0.0.1:${port}`;
  await (async () => {
    for (let i = 0; i < 60; i++) {
      try {
        if ((await fetch(`${root}/status`)).ok) return;
      } catch {}
      await sleep(250);
    }
    throw new Error('safaridriver did not start');
  })();

  const wd = async (method, path, body) => {
    const res = await fetch(`${root}${path}`, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const json = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(`${json.value?.error ?? res.status}: ${json.value?.message ?? ''}`.trim());
    return json.value;
  };

  // safaridriver reuses a Safari that is already running, and Safari refuses
  // ("Safari was not launched for automation"), which shows up only as a
  // 30-second timeout. Say so up front instead of leaving that to be guessed.
  const alreadyRunning = spawnSync('pgrep', ['-x', 'Safari']).status === 0;
  if (alreadyRunning) {
    console.warn('# Safari is already running. safaridriver will try to use it and Safari will refuse it; quit Safari (Cmd-Q) first.');
  }

  let created;
  try {
    created = await wd('POST', '/session', { capabilities: { alwaysMatch: { browserName: 'safari' } } });
  } catch (e) {
    child.kill();
    const hint = alreadyRunning && /timed out/i.test(e.message)
      ? ' Safari was already running and was not launched for automation: quit Safari completely (Cmd-Q) and run again.'
      : '';
    throw new Error(`could not start a Safari session: ${e.message}${hint}`);
  }
  const sid = created.sessionId;
  const S = (method, path, body) => wd(method, `/session/${sid}${path}`, body);
  await S('POST', '/window/rect', { width, height }).catch(() => {});

  const keyActions = (steps) => S('POST', '/actions', { actions: [{ type: 'key', id: 'kbd', actions: steps }] }).then(() => S('DELETE', '/actions'));
  const pointer = (steps) =>
    S('POST', '/actions', { actions: [{ type: 'pointer', id: 'mouse', parameters: { pointerType: 'mouse' }, actions: steps }] }).then(() => S('DELETE', '/actions'));
  const at = (x, y, duration = 0) => ({ type: 'pointerMove', x: Math.round(x), y: Math.round(y), origin: 'viewport', duration });

  const driver = {
    name: 'safari',
    // Safari cannot start a native drag from WebDriver pointer actions (checked
    // at run time), cannot read the clipboard without a prompt, and has no
    // console log API.
    caps: { nativeDrag: 'try', clipboardRead: false, consoleLog: false, hostMapping: false },

    async goto(url) {
      await S('POST', '/url', { url });
    },

    async eval(body) {
      const script = `const done = arguments[arguments.length - 1];
        (async () => {\n${body}\n})().then((v) => done({ v }), (e) => done({ e: String((e && e.message) || e) }));`;
      const r = await S('POST', '/execute/async', { script, args: [] });
      if (r && r.e !== undefined) throw new Error('page exception: ' + r.e);
      return r?.v;
    },

    async key(name, { alt = false, shift = false } = {}) {
      const k = KEYS[name];
      if (!k) throw new Error(`unknown key ${name}`);
      const steps = [];
      if (alt) steps.push({ type: 'keyDown', value: ALT });
      if (shift) steps.push({ type: 'keyDown', value: SHIFT });
      steps.push({ type: 'keyDown', value: k }, { type: 'keyUp', value: k });
      if (shift) steps.push({ type: 'keyUp', value: SHIFT });
      if (alt) steps.push({ type: 'keyUp', value: ALT });
      await keyActions(steps);
    },

    async type(text) {
      const steps = [];
      for (const ch of text) steps.push({ type: 'keyDown', value: ch }, { type: 'keyUp', value: ch });
      await keyActions(steps);
    },

    async mouseMove(x, y) {
      await pointer([at(x, y)]);
    },

    /** A real double click. */
    async doubleClick(x, y) {
      await pointer([at(x, y), { type: 'pointerDown', button: 0 }, { type: 'pointerUp', button: 0 }, { type: 'pointerDown', button: 0 }, { type: 'pointerUp', button: 0 }]);
    },

    /** A real left click. */
    async click(x, y) {
      await pointer([at(x, y), { type: 'pointerDown', button: 0 }, { type: 'pointerUp', button: 0 }]);
    },

    async drag(from, to) {
      const steps = [at(from.x, from.y), { type: 'pointerDown', button: 0 }, { type: 'pause', duration: 50 }];
      for (let i = 1; i <= 8; i++) steps.push(at(from.x + ((to.x - from.x) * i) / 8, from.y + ((to.y - from.y) * i) / 8, 20));
      steps.push({ type: 'pointerUp', button: 0 });
      await pointer(steps);
    },

    /** The same gesture; the caller checks whether the page actually saw a drag. */
    async dragDrop(from, to) {
      await driver.drag(from, to);
      return true;
    },

    async screenshot(path) {
      writeFileSync(path, Buffer.from(await S('GET', '/screenshot'), 'base64'));
    },

    problems() {
      return null; // WebDriver has no console access
    },

    async version() {
      const caps = created.capabilities ?? {};
      return `Safari/${caps.browserVersion ?? '?'}`;
    },

    async close() {
      await S('DELETE', '').catch(() => {});
      child.kill();
    },
  };
  return driver;
}
