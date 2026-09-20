// The browser test suite. It is written once against a small driver interface
// (see lib/chrome.mjs and lib/safari.mjs) and run by chrome.test.mjs and
// safari.test.mjs. Tests use REAL keyboard and mouse events, and poll for state
// instead of sleeping, so they hold up on slow CI machines.
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, before, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { startAttacker } from './lib/attacker.mjs';
import { makeFixture } from './lib/fixture.mjs';
import { buildFsb, startServer } from './lib/server.mjs';
import { basename, hashFor, sleep, waitFor } from './lib/util.mjs';

const artifacts = fileURLToPath(new URL('./artifacts/', import.meta.url));

export function defineSuite({ label, launch }) {
  describe(label, { timeout: 300_000 }, () => {
    const big = !!process.env.FSB_E2E_BIG;
    let fx, server, d, base, buildDir, attacker;

    // ---- helpers -------------------------------------------------------------
    const rows = () => d.eval(`return [...document.querySelectorAll('.vrow .name')].map(n => n.textContent)`);
    const previewOpen = () => d.eval(`return !!document.querySelector('.preview')`);
    const title = () => d.eval(`return document.title`);
    const evalJSON = (body) => d.eval(body);

    /** Loads the app fresh (default preferences) at a folder. */
    async function open(path) {
      await d.eval(`try { localStorage.clear(); } catch {}`).catch(() => {});
      await d.goto('about:blank');
      await d.goto(`${base}/${hashFor(path)}`);
      await waitFor(
        () => d.eval(`return document.title !== 'fsb' && !!document.querySelector('.status') && !document.querySelector('.status').textContent.includes('Loading')`),
        { message: `the app to load ${path}` },
      );
    }

    async function pressTimes(name, n) {
      for (let i = 0; i < n; i++) await d.key(name);
    }

    async function selectByKeys(name) {
      await d.eval(`document.activeElement?.blur?.(); document.body.focus();`);
      await d.key('Escape'); // clears any selection
      const list = await rows();
      const idx = list.indexOf(name);
      assert.ok(idx >= 0, `${name} is not listed in ${JSON.stringify(list)}`);
      await pressTimes('ArrowDown', idx + 1);
      await waitFor(async () => (await d.eval(`return document.querySelector('.row.selected .name')?.textContent`)) === name, { message: `${name} to be selected` });
    }

    const paneSummary = (name) =>
      d.eval(`
        const p = document.querySelector('.preview');
        if (!p) return null;
        const ready = p.querySelector('.ptitle')?.textContent === ${JSON.stringify(name)}
          && !!p.querySelector('.meta dl')
          && !(p.querySelector('.pbody > .hint')?.textContent ?? '').startsWith('Loading');
        if (!ready) return { ready: false };
        // An image still loading, or one that failed and is about to fall back to text, is not settled.
        const img = p.querySelector('.pimg');
        if (img && !(img.complete && img.naturalWidth > 0)) return { ready: false };
        const code = p.querySelector('pre.code');
        const csv = p.querySelector('table.csv');
        return {
          ready: true,
          view: p.querySelector('.pimg') ? 'image' : csv ? 'csv' : code ? (code.classList.contains('hljs') ? 'highlighted' : 'plain') : 'message',
          tokens: code ? code.querySelectorAll('[class^=hljs]').length : 0,
          csvRows: csv ? csv.querySelectorAll('tr').length : 0,
          text: (code ?? p.querySelector('.pbody > .hint') ?? { innerText: '' }).innerText.replace(/\\s+/g, ' ').trim(),
          scripts: p.querySelectorAll('script').length,
          xattrs: [...p.querySelectorAll('.xname')].map((x) => x.textContent),
          linkTo: [...p.querySelectorAll('.meta dt')].find((t) => t.textContent === 'Link to')?.nextElementSibling?.textContent ?? null,
        };`);

    async function preview(name) {
      await selectByKeys(name);
      if (!(await previewOpen())) await d.key(' ');
      return waitFor(async () => {
        const s = await paneSummary(name);
        return s?.ready ? s : null;
      }, { message: `the preview of ${name} to settle` });
    }

    const pointOf = (selector) =>
      d.eval(`const b = document.querySelector(${JSON.stringify(selector)})?.getBoundingClientRect(); return b ? { x: b.x + b.width / 2, y: b.y + b.height / 2 } : null`);

    const rowPoint = (name) =>
      d.eval(`const el = [...document.querySelectorAll('.vrow')].find((r) => r.querySelector('.name')?.textContent === ${JSON.stringify(name)});
              const b = el?.querySelector('.name').getBoundingClientRect(); return b ? { x: b.x + 10, y: b.y + b.height / 2 } : null`);

    const columnWidths = () =>
      d.eval(`const o = {}; for (const h of document.querySelectorAll('.head [data-col]')) o[h.dataset.col] = Math.round(h.getBoundingClientRect().width); return o`);

    const columnOrder = () => d.eval(`return [...document.querySelectorAll('.head [data-col]')].map((h) => h.dataset.col)`);

    // ---- lifecycle -----------------------------------------------------------
    before(async () => {
      mkdirSync(artifacts, { recursive: true });
      buildDir = mkdtempSync(join(tmpdir(), 'fsb-e2e-bin-'));
      fx = makeFixture({ big });
      server = await startServer(buildFsb(buildDir), fx.home);
      base = server.base;
      attacker = await startAttacker();
      d = await launch();
      // The launch URL is single-use: this exchanges it for the session cookie.
      await d.goto(server.url);
      await waitFor(() => d.eval(`return !!document.querySelector('.crumbs')`), { message: 'the first page load' });
      console.log(`# ${label}: ${await d.version()}`);
    });

    after(async () => {
      await d?.screenshot(join(artifacts, `${d.name}-final.png`)).catch(() => {});
      await d?.close().catch(() => {});
      await server?.stop().catch(() => {});
      await attacker?.close().catch(() => {});
      if (fx) rmSync(fx.root, { recursive: true, force: true });
      if (buildDir) rmSync(buildDir, { recursive: true, force: true });
    });

    /** Like `it`, but saves a screenshot when the test fails (uploaded as a CI artifact). */
    const test = (name, fn) =>
      it(name, async (t) => {
        try {
          return await fn(t);
        } catch (e) {
          await d?.screenshot(join(artifacts, `${d.name}-FAILED-${name.replace(/\W+/g, '-').slice(0, 60)}.png`)).catch(() => {});
          throw e;
        }
      });

    // ---- security: what must never be visible ----------------------------------
    test('lists the home folder without denied or ignored entries', async () => {
      await open(fx.home);
      assert.deepEqual(await rows(), fx.homeRows); // .ssh and .aws are denied, node_modules is hidden
      // Showing hidden files must never reveal a denied folder.
      await d.eval(`document.querySelector('.toggle input').click()`);
      await waitFor(async () => (await d.eval(`return document.querySelector('.toggle input').checked`)) === true);
      assert.deepEqual(await rows(), fx.homeRows);
    });

    test('never lists secret files, even with hidden files shown', async () => {
      await open(fx.work);
      await d.eval(`document.querySelector('.toggle input').click()`);
      await waitFor(async () => (await rows()).includes('.envrc'), { message: 'hidden files to appear' });
      const list = await rows();
      for (const secret of ['.env', 'server.pem', 'id.key']) assert.ok(!list.includes(secret), `${secret} must not be listed: ${JSON.stringify(list)}`);
      assert.ok(list.includes('.envrc'), 'a look-alike that is not a secret must still be shown');
    });

    test('answers 404, with no content, for denied paths on every endpoint', async () => {
      await open(fx.work);
      const denied = [
        `${fx.home}/.ssh`,
        `${fx.home}/.ssh/id_test`,
        `${fx.home}/.aws/credentials`,
        `${fx.work}/.env`,
        `${fx.work}/server.pem`,
        `${fx.work}/id.key`,
        `${fx.work}/src/../../.ssh/id_test`, // traversal
        `${fx.home}/.SSH/id_test`, // case variant
      ];
      const r = await evalJSON(`
        const out = [];
        const get = async (ep, p) => { const res = await fetch('/api/' + ep + '?path=' + encodeURIComponent(p)); return { status: res.status, body: await res.text() }; };
        for (const ep of ['list', 'file', 'head', 'meta', 'preview']) {
          const missing = await get(ep, ${JSON.stringify(fx.work + '/does-not-exist')});
          for (const p of ${JSON.stringify(denied)}) {
            const got = await get(ep, p);
            out.push({ ep, p, status: got.status, leak: /SECRET|hunter2/.test(got.body), sameAsMissing: got.status === missing.status && got.body === missing.body });
          }
        }
        return out;`);
      for (const x of r) {
        assert.equal(x.status, 404, `${x.ep} ${x.p}`);
        assert.equal(x.leak, false, `${x.ep} ${x.p} leaked content`);
        assert.equal(x.sameAsMissing, true, `${x.ep} ${x.p} is distinguishable from a missing path`);
      }
    });

    test('shows the same message for a denied folder and a missing one', async () => {
      await open(fx.home);
      const msg = async (path) => {
        await d.eval(`location.hash = ${JSON.stringify(hashFor(path))}`);
        return waitFor(() => d.eval(`return document.querySelector('.msg.error')?.textContent ?? null`), { message: `an error for ${path}` });
      };
      const denied = await msg(`${fx.home}/.ssh`);
      await d.eval(`location.hash = ${JSON.stringify(hashFor(fx.home))}`); // leave, so the next error is a fresh one
      await waitFor(async () => !(await d.eval(`return !!document.querySelector('.msg.error')`)));
      const missing = await msg(`${fx.home}/nope`);
      assert.equal(denied, missing);
      assert.match(denied, /Not found/);
    });

    // ---- keyboard ------------------------------------------------------------
    test('toggles the preview pane with a real Space key', async () => {
      await open(fx.work);
      const start = await previewOpen();
      await d.key(' ');
      await waitFor(async () => (await previewOpen()) === !start, { message: 'the pane to toggle' });
      await d.key(' ');
      await waitFor(async () => (await previewOpen()) === start, { message: 'the pane to toggle back' });
    });

    test('selects, opens and goes up with the keyboard, re-selecting the folder it left', async () => {
      await open(fx.home);
      await selectByKeys('pics/');
      await d.key('ArrowRight');
      await waitFor(async () => (await title()) === 'pics - fsb', { message: 'to open pics/' });
      assert.deepEqual(await rows(), ['gradient.png', 'no-extension']);
      await d.key('ArrowLeft');
      await waitFor(async () => (await title()) === `${basename(fx.home)} - fsb`, { message: 'to go back up' });
      await waitFor(async () => (await d.eval(`return document.querySelector('.row.selected .name')?.textContent`)) === 'pics/', { message: 'pics/ to be re-selected' });
    });

    // ---- previews ------------------------------------------------------------
    test('previews each file type correctly and safely', async () => {
      await open(fx.work);
      const json = await preview('data.json');
      assert.equal(json.view, 'highlighted');
      assert.ok(json.tokens > 5, 'JSON should be syntax highlighted');
      assert.match(json.text, /"name": "fsb"/, 'JSON should be pretty-printed');
      if (fx.xattrs) assert.ok(json.xattrs.some((x) => x.includes('com.example.note')), `xattrs: ${json.xattrs}`);

      const csv = await preview('people.csv');
      assert.equal(csv.view, 'csv');
      assert.equal(csv.csvRows, 3); // header + 2 records; the quoted comma and doubled quotes are one cell each

      const log = await preview('app.log');
      assert.equal(log.view, 'plain');
      assert.match(log.text, /plain log line 1/);

      const md = await preview('README.md');
      assert.equal(md.view, 'highlighted'); // Markdown source, highlighted (not rendered)

      const bin = await preview('random.bin');
      assert.equal(bin.view, 'message');
      assert.match(bin.text, /Binary file/);

      // HTML with a .png name is refused as an image and shown as inert text.
      const disguised = await preview('disguised.png');
      assert.equal(disguised.view, 'plain');
      assert.equal(disguised.scripts, 0, 'no script element may ever reach the page');
      assert.match(disguised.text, /<script>alert\(1\)<\/script>/);

      // A symlink is presented according to what it points at.
      const link = await preview('link-to-main');
      assert.equal(link.view, 'highlighted');
      assert.equal(link.linkTo, 'src/main.go');
    });

    test('previews an image through the sandboxed endpoint', async () => {
      await open(`${fx.home}/pics`);
      await d.key('ArrowDown');
      if (!(await previewOpen())) await d.key(' ');
      const img = await waitFor(
        () => d.eval(`const i = document.querySelector('.preview .pimg'); return i && i.complete && i.naturalWidth ? { w: i.naturalWidth, h: i.naturalHeight } : null`),
        { message: 'the image to load' },
      );
      assert.deepEqual(img, { w: 60, h: 40 });
      const headers = await d.eval(`const r = await fetch('/api/preview?path=' + encodeURIComponent(${JSON.stringify(fx.home + '/pics/gradient.png')}));
        return { type: r.headers.get('content-type'), csp: r.headers.get('content-security-policy'), nosniff: r.headers.get('x-content-type-options'), disposition: r.headers.get('content-disposition') };`);
      assert.equal(headers.type, 'image/png');
      assert.match(headers.csp, /sandbox/);
      assert.equal(headers.nosniff, 'nosniff');
      assert.match(headers.disposition, /^inline/);
      // Identified by its bytes, not its name: no extension means no image preview.
      await d.key('ArrowDown');
      const bin = await waitFor(async () => {
        const s = await paneSummary('no-extension');
        return s?.ready ? s : null;
      });
      assert.equal(bin.view, 'message');
    });

    // ---- hover peek ----------------------------------------------------------
    test('shows a hover bubble for text files only', async () => {
      await open(fx.work);
      const peek = () => d.eval(`const p = document.querySelector('.peek'); if (!p) return null; const b = p.getBoundingClientRect();
        return { text: p.innerText.replace(/\\s+/g, ' ').trim(), inViewport: b.left >= 0 && b.right <= innerWidth && b.top >= 0 && b.bottom <= innerHeight, role: p.getAttribute('role') };`);
      const hover = async (name) => {
        const p = await rowPoint(name);
        await d.mouseMove(5, 5);
        await sleep(150);
        await d.mouseMove(p.x, p.y);
      };
      await hover('app.log');
      const bubble = await waitFor(peek, { timeout: 4000, message: 'a hover bubble for app.log' });
      assert.match(bubble.text, /plain log line 1/);
      assert.equal(bubble.inViewport, true);
      assert.equal(bubble.role, 'tooltip');

      for (const name of ['random.bin', 'src/']) {
        await hover(name);
        await sleep(1300); // longer than the hover delay
        assert.equal(await peek(), null, `no bubble expected for ${name}`);
      }
      // A denied file cannot be listed, so it can never be hovered; check the request itself.
      const denied = await d.eval(`return (await fetch('/api/head?path=' + encodeURIComponent(${JSON.stringify(fx.work + '/.env')}))).status`);
      assert.equal(denied, 404);
    });

    // ---- columns -------------------------------------------------------------
    test('resizes a column by dragging its edge and with the keyboard, and remembers it', async () => {
      await open(fx.work);
      const before = (await columnWidths()).name;
      const handle = await pointOf('.head [data-col="name"] .resize');
      await d.drag(handle, { x: handle.x - 80, y: handle.y });
      await waitFor(async () => (await columnWidths()).name === before - 80, { message: 'the drag to resize the column' });
      assert.equal(await d.eval(`return JSON.parse(localStorage.getItem('fsb.columns.v1')).widths.name`), before - 80);

      await d.eval(`document.querySelector('.head [data-col="name"] .resize').focus()`);
      await pressTimes('ArrowRight', 3);
      await waitFor(async () => (await columnWidths()).name === before - 50, { message: 'the keyboard to widen the column' });
      await d.key('ArrowLeft', { shift: true });
      await waitFor(async () => (await columnWidths()).name === before - 100);

      // The minimum is enforced.
      await pressTimes('ArrowLeft', 1);
      await d.eval(`for (let i = 0; i < 30; i++) document.querySelector('.head [data-col="name"] .resize').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', shiftKey: true, bubbles: true }))`);
      await waitFor(async () => (await columnWidths()).name === 60, { message: 'the minimum width' });
    });

    test('reorders columns with Alt+Arrow', async () => {
      await open(fx.work);
      assert.deepEqual(await columnOrder(), ['name', 'size', 'modTime', 'kind', 'mode']);
      await d.eval(`document.querySelector('.head [data-col="kind"] button').focus()`);
      await d.key('ArrowLeft', { alt: true });
      await waitFor(async () => (await columnOrder()).join() === 'name,size,kind,modTime,mode', { message: 'Alt+Left to move Kind' });
      await d.key('ArrowLeft', { alt: true });
      await waitFor(async () => (await columnOrder()).join() === 'name,kind,size,modTime,mode');
      assert.deepEqual(await d.eval(`return JSON.parse(localStorage.getItem('fsb.columns.v1')).order`), ['name', 'kind', 'size', 'modTime', 'mode', 'xattr']);
      // The rows follow the header.
      assert.deepEqual(await d.eval(`return [...document.querySelector('.vrow .row').children].map((c) => c.dataset.col)`), ['name', 'kind', 'size', 'modTime', 'mode']);
    });

    test('reorders columns by dragging a header', async (t) => {
      if (d.caps.nativeDrag === false) return t.skip('this driver cannot perform native drag-and-drop');
      await open(fx.work);
      await d.eval(`window.__drag = []; for (const ev of ['dragstart', 'dragover', 'drop', 'dragend']) document.addEventListener(ev, () => window.__drag.push(ev), true);`);
      const kind = await pointOf('.head [data-col="kind"] .hdr');
      const size = await pointOf('.head [data-col="size"] .hdr');
      const started = await d.dragDrop(kind, size);
      // A native drop should arrive promptly. Some drivers never start the drag (Chrome
      // headless without interception) and others start it but cannot complete the drop
      // (Safari under WebDriver): both are driver limits, not app faults.
      const dropped = started
        ? await waitFor(async () => (await d.eval(`return window.__drag`)).includes('drop'), { timeout: 1500 }).then(() => true, () => false)
        : false;
      if (!dropped) {
        // Exercise the app's drag handlers with synthetic events instead.
        const seen = await d.eval(`return window.__drag`);
        t.diagnostic(`${d.name} did not complete a native drop (saw: ${seen.join(',') || 'nothing'}); falling back to synthetic drag events`);
        await d.eval(`
          const dt = new DataTransfer();
          const from = document.querySelector('.head [data-col="kind"] .hdr');
          const to = document.querySelector('.head [data-col="size"] .hdr');
          from.dispatchEvent(new DragEvent('dragstart', { bubbles: true, dataTransfer: dt }));
          to.dispatchEvent(new DragEvent('dragover', { bubbles: true, cancelable: true, dataTransfer: dt }));
          to.dispatchEvent(new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: dt }));
          from.dispatchEvent(new DragEvent('dragend', { bubbles: true, dataTransfer: dt }));`);
      }
      await waitFor(async () => (await columnOrder()).join() === 'name,kind,size,modTime,mode', { message: 'the header drop to reorder' });
      assert.equal(await d.eval(`return document.querySelectorAll('.hdr.drop, .hdr.dragging').length`), 0, 'drag markers must be cleared');
    });

    test('keeps the layout across a reload, and Reset restores the defaults', async () => {
      await open(fx.work);
      const handle = await pointOf('.head [data-col="name"] .resize');
      await d.drag(handle, { x: handle.x - 60, y: handle.y });
      await waitFor(async () => (await columnWidths()).name === 300);
      await d.goto('about:blank');
      await d.goto(`${base}/${hashFor(fx.work)}`);
      await waitFor(async () => (await columnWidths()).name === 300, { message: 'the saved width to be restored' });
      await d.eval(`document.querySelector('.colmenu').open = true; [...document.querySelectorAll('.colmenu .textbtn')].find((b) => b.textContent.includes('Reset')).click()`);
      await waitFor(async () => (await columnWidths()).name === 360, { message: 'Reset to restore the default width' });
      assert.equal(await d.eval(`return localStorage.getItem('fsb.columns.v1')`), null);
    });

    test('fetches the Attributes column lazily, only for rows on screen', async () => {
      await open(fx.work);
      assert.equal(await d.eval(`return !!document.querySelector('.head [data-col="xattr"]')`), false, 'hidden by default');
      const metaRequests = () => d.eval(`return performance.getEntriesByType('resource').filter((r) => r.name.includes('/api/meta')).length`);
      const before = await metaRequests();
      await d.eval(`document.querySelector('.colmenu').open = true; [...document.querySelectorAll('.colmenu label')].find((l) => l.textContent.includes('Attributes')).querySelector('input').click()`);
      const onScreen = await d.eval(`return document.querySelectorAll('.vrow').length`);
      await waitFor(async () => !(await d.eval(`return [...document.querySelectorAll('.vrow [data-col="xattr"]')].some((c) => c.textContent === '…')`)), { message: 'attribute names to load' });
      assert.equal((await metaRequests()) - before, onScreen, 'one request per visible row, nothing more');
      if (fx.xattrs) {
        const cell = await d.eval(`return [...document.querySelectorAll('.vrow')].find((r) => r.querySelector('.name')?.textContent === 'data.json')?.querySelector('[data-col="xattr"]')?.textContent`);
        assert.match(cell, /com\.example\.note/);
      }
    });

    // ---- search and copy -----------------------------------------------------
    test('searches by name, shallowest first, without denied or hidden results', async () => {
      await open(fx.home);
      await d.eval(`document.activeElement?.blur?.(); document.body.focus();`);
      await d.key('s');
      await waitFor(async () => (await d.eval(`return document.activeElement?.getAttribute('aria-label')`)) === 'Search subfolders by name', { message: 'the search box to take focus' });
      await d.type('needle');
      await d.key('Enter');
      await waitFor(async () => (await rows()).length === 2 && (await d.eval(`return !document.querySelector('.status').textContent.includes('Searching')`)), { message: 'the search to finish' });
      assert.deepEqual(await rows(), ['work/needle-shallow.txt', 'work/src/deep/er/Needle-Deep.txt']); // case-insensitive, shallowest first
      // node_modules (hidden) and .ssh (denied) matches are absent.

      // Denied names find nothing at all.
      for (const q of ['id_test', 'credentials', 'server.pem']) {
        await d.eval(`const box = document.querySelector('input[aria-label="Search subfolders by name"]'); box.focus(); box.select();`);
        await d.type(q);
        await d.key('Enter');
        await waitFor(async () => (await d.eval(`return !document.querySelector('.status').textContent.includes('Searching')`)), { message: `search for ${q} to finish` });
        assert.deepEqual(await rows(), [], `searching for ${q} must find nothing`);
      }
    });

    test('opens a search result in its folder with the file selected', async () => {
      await open(fx.home);
      await d.eval(`const box = document.querySelector('input[aria-label="Search subfolders by name"]'); box.focus();`);
      await d.type('needle');
      await d.key('Enter');
      await waitFor(async () => (await rows()).length === 2);
      await d.eval(`document.activeElement?.blur?.(); document.body.focus();`);
      await pressTimes('ArrowDown', 2);
      await d.key('Enter');
      await waitFor(async () => (await title()) === 'er - fsb', { message: 'to open the result folder' });
      await waitFor(async () => (await d.eval(`return document.querySelector('.row.selected .name')?.textContent`)) === 'Needle-Deep.txt', { message: 'the file to be selected' });
    });

    test('copies the selected path with the c key', async (t) => {
      await open(fx.work);
      await selectByKeys('app.log');
      await d.key('c');
      const toast = await waitFor(() => d.eval(`return document.querySelector('.toast')?.textContent ?? null`), { message: 'a toast' });
      assert.match(toast, /Copied/);
      if (d.caps.clipboardRead) {
        const clip = await d.eval(`return await navigator.clipboard.readText()`);
        assert.equal(clip, `${fx.work}/app.log`);
      } else t.diagnostic('clipboard contents not read back with this driver');
    });


    // ---- attacks from other websites --------------------------------------------
    // The visitor's browser holds a valid fsb session cookie. A malicious page on
    // another site must not be able to use it. The "attacker" is a real second
    // origin (see lib/attacker.mjs), so these exercise real browser behaviour:
    // SameSite cookies, Sec-Fetch-Site, CORS and frame protections.
    const loadsImage = (url) =>
      `return await new Promise((resolve) => { const i = new Image(); i.onload = () => resolve('loaded'); i.onerror = () => resolve('error'); i.src = ${JSON.stringify(url)}; setTimeout(() => resolve('timeout'), 6000); })`;

    test('another site cannot load fsb images with the visitor session', async () => {
      const url = `${base}/api/preview?path=${encodeURIComponent(fx.home + '/pics/gradient.png')}`;
      await open(fx.home); // control: the same request from fsb's own origin works
      assert.equal(await d.eval(loadsImage(url)), 'loaded', 'control: fsb serves the image to its own page');
      await d.goto(attacker.url);
      assert.equal(await d.eval(loadsImage(url)), 'error', 'a page on another site must not get the image');
    });

    test('another site cannot read fsb responses with fetch', async () => {
      await open(fx.home); // make sure the session cookie is in place
      await d.goto(attacker.url);
      const r = await d.eval(`try { const res = await fetch(${JSON.stringify(base + '/api/status')}, { credentials: 'include' }); return { ok: true, status: res.status, body: await res.text() }; } catch (e) { return { ok: false, error: e.name }; }`);
      assert.equal(r.ok, false, `a cross-origin read must be refused, got ${JSON.stringify(r)}`);
    });

    test('a navigation started by another site is not authorized by the session', async () => {
      await open(fx.home);
      await d.goto(attacker.url);
      await d.eval(`setTimeout(() => { location.href = ${JSON.stringify(base + '/api/list?path=' + encodeURIComponent(fx.home))}; }, 0); return true;`);
      const body = await waitFor(async () => {
        const here = await d.eval(`return location.origin`);
        return here === base ? d.eval(`return document.body.innerText`) : null;
      }, { message: 'the cross-site navigation to land' });
      assert.match(body, /forbidden/i);
      assert.doesNotMatch(body, /"entries"|"path"|pics/);
      // Control: the visitor typing the same address is served (a browser-initiated navigation).
      await d.goto(`${base}/api/status`);
      assert.match(await d.eval(`return document.body.innerText`), /readOnly/);
    });

    test('a hostile hostname pointing at fsb is refused (DNS rebinding)', async (t) => {
      if (!d.caps.hostMapping) return t.skip('this driver cannot map extra hostnames to loopback');
      const port = new URL(base).port;
      for (const host of ['evil.test', '127.0.0.1.evil.test', 'localhost.evil.test']) {
        await d.goto(`http://${host}:${port}/api/status`);
        const body = await d.eval(`return document.body.innerText`);
        assert.match(body, /forbidden/i, `${host}: ${body.slice(0, 80)}`);
        assert.doesNotMatch(body, /readOnly|roots/);
      }
    });

    test('the session cookie cannot be read by page scripts, and fsb cannot be framed', async () => {
      await open(fx.home);
      assert.ok(!(await d.eval(`return document.cookie`)).includes('fsb_session'), 'the session cookie must be HttpOnly');
      const h = await d.eval(`const r = await fetch('/'); return { xfo: r.headers.get('x-frame-options'), csp: r.headers.get('content-security-policy'), cache: r.headers.get('cache-control'), ref: r.headers.get('referrer-policy') };`);
      assert.equal(h.xfo, 'DENY');
      assert.match(h.csp, /frame-ancestors 'none'/);
      assert.match(h.csp, /default-src 'self'/);
      assert.equal(h.cache, 'no-store');
      assert.equal(h.ref, 'no-referrer');
    });

    // ---- health --------------------------------------------------------------
    test('produces no script errors or CSP violations', async (t) => {
      const problems = d.problems();
      if (problems === null) return t.skip('this driver has no console access');
      const bad = problems.filter((p) => /Content Security Policy|exception|Refused to/i.test(p));
      assert.deepEqual(bad, []);
      // The only expected resource errors are the intentional 404s/415s from denied, missing and disguised
      // paths, and requests from the simulated attacker page that fsb's own headers block.
      for (const p of problems.filter((x) => /Failed to load resource/.test(x))) {
        assert.match(p, /status of (404|415|403)|ERR_BLOCKED_BY_RESPONSE\.NotSameOrigin|ERR_FAILED http:\/\/127\.0\.0\.1:\d+\/api\/status/, `unexpected resource error: ${p}`);
      }
    });

    // ---- scale (opt-in: creates 100,000 files) --------------------------------
    test('lists a 100,000-entry folder quickly with a virtualized list', async (t) => {
      if (!big) return t.skip('set FSB_E2E_BIG=1 to run the 100,000-file test');
      await d.eval(`try { localStorage.clear(); } catch {}`).catch(() => {});
      await d.goto('about:blank');
      const started = Date.now();
      await d.goto(`${base}/${hashFor(`${fx.home}/big`)}`);
      await waitFor(() => d.eval(`return document.querySelectorAll('.vrow').length > 0`), { timeout: 15000, message: 'the first rows' });
      const firstRowsMs = Date.now() - started;
      await waitFor(() => d.eval(`return document.querySelector('.status').textContent.trim().startsWith('100,000 of 100,000')`), { timeout: 30000, message: 'all 100,000 entries' });
      const domRows = await d.eval(`return document.querySelectorAll('.vrow').length`);
      t.diagnostic(`first rows after ${firstRowsMs}ms; ${domRows} DOM rows for 100,000 entries`);
      assert.ok(domRows < 80, `only visible rows may be rendered, got ${domRows}`);
      assert.ok(firstRowsMs < 4000, `first rows took ${firstRowsMs}ms`);
      await d.eval(`const v = document.querySelector('.viewport'); v.scrollTop = v.scrollHeight;`);
      await waitFor(async () => (await rows()).includes('file-100000'), { message: 'the last row after scrolling to the end' });
    });
  });
}
