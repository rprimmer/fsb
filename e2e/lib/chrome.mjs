// A Chrome/Chromium driver over the DevTools protocol, using real (trusted)
// keyboard and mouse events. Dependency-free: Node's global WebSocket and fetch.
import { spawn } from 'node:child_process';
import { existsSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { sleep } from './util.mjs';

function findChrome() {
  const candidates = [
    process.env.CHROME_BIN,
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    '/Applications/Chromium.app/Contents/MacOS/Chromium',
    '/usr/bin/google-chrome',
    '/usr/bin/google-chrome-stable',
    '/usr/bin/chromium',
    '/usr/bin/chromium-browser',
  ].filter(Boolean);
  const found = candidates.find((c) => existsSync(c));
  if (!found) throw new Error('no Chrome found; set CHROME_BIN to a Chrome or Chromium executable');
  return found;
}

const KEYS = {
  ' ': { key: ' ', code: 'Space', vk: 32, text: ' ' },
  ArrowDown: { key: 'ArrowDown', code: 'ArrowDown', vk: 40 },
  ArrowUp: { key: 'ArrowUp', code: 'ArrowUp', vk: 38 },
  ArrowLeft: { key: 'ArrowLeft', code: 'ArrowLeft', vk: 37 },
  ArrowRight: { key: 'ArrowRight', code: 'ArrowRight', vk: 39 },
  Enter: { key: 'Enter', code: 'Enter', vk: 13, text: '\r' },
  Escape: { key: 'Escape', code: 'Escape', vk: 27 },
  Backspace: { key: 'Backspace', code: 'Backspace', vk: 8 },
  c: { key: 'c', code: 'KeyC', vk: 67, text: 'c' },
  s: { key: 's', code: 'KeyS', vk: 83, text: 's' },
};

export async function launchChrome({ width = 1280, height = 800 } = {}) {
  const bin = findChrome();
  const profile = mkdtempSync(join(tmpdir(), 'fsb-chrome-'));
  const args = [
    '--headless=new',
    '--remote-debugging-port=0',
    `--user-data-dir=${profile}`,
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-extensions',
    // Any *.test name resolves to loopback, so a page can be reached under a hostile hostname (DNS rebinding).
    '--host-resolver-rules=MAP *.test 127.0.0.1',
    ...(process.env.CI ? ['--no-sandbox'] : []),
    'about:blank',
  ];
  const child = spawn(bin, args, { stdio: ['ignore', 'ignore', 'pipe'] });
  const browserWS = await new Promise((resolve, reject) => {
    let err = '';
    const timer = setTimeout(() => reject(new Error(`Chrome did not report a DevTools endpoint in 20s.\n${err}`)), 20000);
    child.stderr.on('data', (d) => {
      err += d;
      const m = err.match(/DevTools listening on (ws:\/\/\S+)/);
      if (m) {
        clearTimeout(timer);
        resolve(m[1]);
      }
    });
    child.on('exit', (code) => reject(new Error(`Chrome exited early (${code}).\n${err}`)));
  });

  const ws = new WebSocket(browserWS);
  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = () => reject(new Error('could not connect to the DevTools websocket'));
  });
  let nextId = 0;
  const pending = new Map();
  const events = [];
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.id && pending.has(m.id)) {
      const { res, rej } = pending.get(m.id);
      pending.delete(m.id);
      m.error ? rej(new Error(`${m.error.message}`)) : res(m.result);
    } else events.push(m);
  };
  const send = (method, params = {}, sessionId) =>
    new Promise((res, rej) => {
      const id = ++nextId;
      pending.set(id, { res, rej });
      ws.send(JSON.stringify({ id, method, params, sessionId }));
    });

  const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
  const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
  const S = (method, params) => send(method, params, sessionId);
  await S('Page.enable');
  await S('Runtime.enable');
  await S('Log.enable');
  await S('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false });
  // A headless page can lose focus across navigations, and real key presses then go nowhere.
  await S('Emulation.setFocusEmulationEnabled', { enabled: true });

  let grantedOrigin = '';
  const eventsAfter = (mark, method) => events.slice(mark).find((m) => m.method === method);

  const driver = {
    name: 'chrome',
    caps: { nativeDrag: true, clipboardRead: true, consoleLog: true, hostMapping: true },

    async goto(url) {
      const origin = url.startsWith('http') ? new URL(url).origin : '';
      if (origin && origin !== grantedOrigin) {
        // Only fsb's own origin needs clipboard access; Chrome refuses the grant for insecure hostnames.
        await send('Browser.grantPermissions', { origin, permissions: ['clipboardReadWrite', 'clipboardSanitizedWrite'] }).catch(() => {});
        grantedOrigin = origin;
      }
      const mark = events.length;
      await S('Page.navigate', { url });
      const deadline = Date.now() + 15000;
      while (!eventsAfter(mark, 'Page.loadEventFired')) {
        if (Date.now() > deadline) throw new Error(`page did not finish loading: ${url}`);
        await sleep(25);
      }
    },

    /** Runs the body of an async function in the page and returns its (JSON) result. */
    async eval(body) {
      const r = await S('Runtime.evaluate', { expression: `(async () => {\n${body}\n})()`, awaitPromise: true, returnByValue: true });
      if (r.exceptionDetails) {
        throw new Error('page exception: ' + (r.exceptionDetails.exception?.description ?? r.exceptionDetails.text));
      }
      return r.result.value;
    },

    /** A real key press. modifiers: { alt, shift }. */
    async key(name, { alt = false, shift = false } = {}) {
      const k = KEYS[name];
      if (!k) throw new Error(`unknown key ${name}`);
      const modifiers = (alt ? 1 : 0) | (shift ? 8 : 0);
      const base = { key: k.key, code: k.code, windowsVirtualKeyCode: k.vk, modifiers };
      await S('Input.dispatchKeyEvent', { type: k.text ? 'keyDown' : 'rawKeyDown', ...base, text: k.text });
      await S('Input.dispatchKeyEvent', { type: 'keyUp', ...base });
    },

    async type(text) {
      await S('Input.insertText', { text });
    },

    async mouseMove(x, y) {
      await S('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
    },

    /** A real left click. */
    async click(x, y) {
      await driver.mouseMove(x, y);
      await S('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', buttons: 1, clickCount: 1 });
      await S('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', buttons: 0, clickCount: 1 });
    },

    /** Press, move in steps, release. The button must be named on every move or Chrome drops pointer capture. */
    async drag(from, to) {
      await driver.mouseMove(from.x, from.y);
      await S('Input.dispatchMouseEvent', { type: 'mousePressed', x: from.x, y: from.y, button: 'left', buttons: 1, clickCount: 1 });
      for (let i = 1; i <= 8; i++) {
        await S('Input.dispatchMouseEvent', {
          type: 'mouseMoved',
          x: from.x + ((to.x - from.x) * i) / 8,
          y: from.y + ((to.y - from.y) * i) / 8,
          button: 'left',
          buttons: 1,
        });
        await sleep(15);
      }
      await S('Input.dispatchMouseEvent', { type: 'mouseReleased', x: to.x, y: to.y, button: 'left', buttons: 0, clickCount: 1 });
    },

    /**
     * A native HTML5 drag-and-drop. Headless Chrome will not start one from
     * synthetic mouse events, so this uses the protocol's drag interception.
     * Returns false if the browser never started a drag.
     */
    async dragDrop(from, to) {
      await S('Input.setInterceptDrags', { enabled: true });
      const mark = events.length;
      await driver.mouseMove(from.x, from.y);
      await S('Input.dispatchMouseEvent', { type: 'mousePressed', x: from.x, y: from.y, button: 'left', buttons: 1, clickCount: 1 });
      for (let i = 1; i <= 5; i++) {
        await S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: from.x + i * 6, y: from.y, button: 'left', buttons: 1 });
        await sleep(30);
      }
      const deadline = Date.now() + 2000;
      let intercepted;
      while (!(intercepted = eventsAfter(mark, 'Input.dragIntercepted'))) {
        if (Date.now() > deadline) break;
        await sleep(25);
      }
      if (!intercepted) {
        await S('Input.dispatchMouseEvent', { type: 'mouseReleased', x: from.x, y: from.y, button: 'left', buttons: 0, clickCount: 1 });
        await S('Input.setInterceptDrags', { enabled: false });
        return false;
      }
      const data = intercepted.params.data;
      for (const type of ['dragEnter', 'dragOver', 'drop']) await S('Input.dispatchDragEvent', { type, x: to.x, y: to.y, data });
      await S('Input.dispatchMouseEvent', { type: 'mouseReleased', x: to.x, y: to.y, button: 'left', buttons: 0, clickCount: 1 });
      await S('Input.setInterceptDrags', { enabled: false });
      return true;
    },

    async screenshot(path) {
      const { data } = await S('Page.captureScreenshot', { format: 'png' });
      writeFileSync(path, Buffer.from(data, 'base64'));
    },

    /** Console errors, warnings and uncaught exceptions seen so far. */
    problems() {
      const out = [];
      for (const m of events) {
        if (m.method === 'Runtime.exceptionThrown') out.push('exception: ' + (m.params.exceptionDetails.exception?.description ?? m.params.exceptionDetails.text));
        if (m.method === 'Log.entryAdded' && ['error', 'warning'].includes(m.params.entry.level)) out.push(`${m.params.entry.level}: ${m.params.entry.text} ${m.params.entry.url ?? ''}`.trim());
        if (m.method === 'Runtime.consoleAPICalled' && ['error', 'warning'].includes(m.params.type)) out.push(`console.${m.params.type}: ${m.params.args.map((a) => a.value ?? a.description).join(' ')}`);
      }
      return out;
    },

    async version() {
      return (await (await fetch(browserWS.replace('ws://', 'http://').replace(/\/devtools\/browser\/.*/, '/json/version'))).json()).Browser;
    },

    async close() {
      try {
        ws.close();
      } catch {}
      child.kill();
      await new Promise((r) => (child.exitCode !== null ? r() : child.once('exit', r)));
      rmSync(profile, { recursive: true, force: true });
    },
  };
  return driver;
}
