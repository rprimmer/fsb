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
        const mdf = p.querySelector('iframe.mdframe');
        if (mdf && !mdf.dataset.info) return { ready: false };
        return {
          ready: true,
          view: mdf ? 'rendered' : p.querySelector('.pimg') ? 'image' : csv ? 'csv' : code ? (code.classList.contains('hljs') ? 'highlighted' : 'plain') : 'message',
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
      await d.eval(`[...document.querySelectorAll('.toggle')].find((l) => l.textContent.includes('Hidden files')).querySelector('input').click()`);
      await waitFor(async () => (await d.eval(`return [...document.querySelectorAll('.toggle')].find((l) => l.textContent.includes('Hidden files')).querySelector('input').checked`)) === true);
      assert.deepEqual(await rows(), fx.homeRows);
    });

    test('never lists secret files, even with hidden files shown', async () => {
      await open(fx.work);
      await d.eval(`[...document.querySelectorAll('.toggle')].find((l) => l.textContent.includes('Hidden files')).querySelector('input').click()`);
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
      assert.equal(md.view, 'rendered'); // details in the Markdown tests below

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

    // ---- PDF -----------------------------------------------------------------
    test('shows a real PDF in the built-in viewer frame, and treats a fake .pdf as text', async () => {
      await open(fx.work);
      await selectByKeys('doc.pdf');
      if (!(await previewOpen())) await d.key(' ');
      const src = await waitFor(() => d.eval(`return document.querySelector('.preview iframe.pdfframe')?.getAttribute('src') ?? null`), { message: 'the PDF frame' });
      assert.match(src, /^\/api\/pdf\?path=/);
      const h = await d.eval(`const r = await fetch(${JSON.stringify(src)}); return { type: r.headers.get('content-type'), xfo: r.headers.get('x-frame-options'), csp: r.headers.get('content-security-policy'), nosniff: r.headers.get('x-content-type-options'), head: (await r.text()).slice(0, 5) };`);
      assert.deepEqual(h, { type: 'application/pdf', xfo: 'SAMEORIGIN', csp: "frame-ancestors 'self'; script-src 'none'", nosniff: 'nosniff', head: '%PDF-' });
      assert.equal(await d.eval(`return document.querySelector('.preview iframe.pdfframe').hasAttribute('sandbox')`), false);

      // Not a PDF by its bytes: never framed, shown as ordinary text instead.
      const fake = await preview('fake.pdf');
      assert.equal(await d.eval(`return !!document.querySelector('.preview iframe')`), false);
      assert.match(fake.text, /alert\(1\)/);
      const r = await d.eval(`return (await fetch('/api/pdf?path=' + encodeURIComponent(${JSON.stringify(fx.work + '/fake.pdf')}))).status`);
      assert.equal(r, 415);
      // The app itself still cannot be framed.
      assert.equal(await d.eval(`return (await fetch('/')).headers.get('x-frame-options')`), 'DENY');
    });

    // ---- rendered Markdown -------------------------------------------------
    const mdInfo = () => d.eval(`const f = document.querySelector('.preview iframe.mdframe'); return f?.dataset.info ? JSON.parse(f.dataset.info) : null`);

    test('renders Markdown in a sandboxed frame; raw HTML and scripts stay inert', async () => {
      await open(fx.work);
      await preview('README.md');
      const info = await waitFor(mdInfo, { message: 'the Markdown to render' });
      assert.match(info.text, /Title/);
      assert.match(info.text, /Some markdown with code\./);
      assert.match(info.text, /Remote image blocked: tracker/);
      // Raw HTML is shown as text, not run.
      assert.match(info.text, /<script>document\.title = "PWNED"<\/script>/);
      assert.notEqual(await title(), 'PWNED');
      // javascript: links are dropped to plain text; the other two survive.
      assert.deepEqual(info.links.map((l) => l.kind + ':' + l.href), ['file:' + fx.work + '/src/main.go', 'external:https://example.com/docs']);
      assert.match(info.text, /click me/);
      assert.equal(info.images, 1, 'the local image was fetched through the guarded endpoint');
      const f = await d.eval(`const f = document.querySelector('.preview iframe.mdframe');
        let reach = 'blocked'; try { reach = f.contentDocument ? 'reachable' : 'blocked'; } catch { reach = 'blocked'; }
        return { sandbox: f.getAttribute('sandbox'), reach };`);
      assert.equal(f.sandbox, 'allow-scripts', 'no allow-same-origin');
      assert.equal(f.reach, 'blocked', 'the app cannot reach into the frame, nor the frame into the app');
      const h = await d.eval(`const r = await fetch('/api/mdframe'); return { csp: r.headers.get('content-security-policy'), xfo: r.headers.get('x-frame-options') };`);
      assert.match(h.csp, /default-src 'none'/);
      assert.doesNotMatch(h.csp, /unsafe/);
      assert.equal(h.xfo, 'SAMEORIGIN');
    });

    test('a Markdown file link opens that file inside fsb; a web link opens a new tab', async () => {
      await open(fx.work);
      await preview('README.md');
      const info = await waitFor(mdInfo, { message: 'the Markdown to render' });
      const box = await d.eval(`const b = document.querySelector('.preview iframe.mdframe').getBoundingClientRect(); return { x: b.x, y: b.y }`);
      const center = (l) => ({ x: box.x + l.x + l.w / 2, y: box.y + l.y + l.h / 2 });
      // Record window.open rather than spawning a real tab.
      await d.eval(`window.__opened = []; window.open = (...a) => { window.__opened.push(a); return null; }; return true`);
      const web = center(info.links.find((l) => l.kind === 'external'));
      await d.click(web.x, web.y);
      const opened = await waitFor(() => d.eval(`return window.__opened.length ? window.__opened[0] : null`), { message: 'the web link to open' });
      assert.equal(opened[0], 'https://example.com/docs');
      assert.match(opened[2], /noopener/);
      assert.match(opened[2], /noreferrer/);
      const file = center(info.links.find((l) => l.kind === 'file'));
      await d.click(file.x, file.y);
      await waitFor(async () => (await d.eval(`return document.querySelector('.row.selected .name')?.textContent`)) === 'main.go', { message: 'main.go to be selected in its folder' });
      assert.match(await d.eval(`return location.hash`), /src\?select=main\.go$/);
    });

    test('Markdown can be viewed as source, and back', async () => {
      await open(fx.work);
      await preview('README.md');
      await waitFor(mdInfo, { message: 'the Markdown to render' });
      const press = (label) => d.eval(`[...document.querySelectorAll('.viewtoggle button')].find((b) => b.textContent === ${JSON.stringify(label)}).click(); return true`);
      await press('Source');
      await waitFor(() => d.eval(`return !!document.querySelector('.preview pre.code') && !document.querySelector('.preview iframe.mdframe')`), { message: 'the source view' });
      assert.match(await d.eval(`return document.querySelector('.preview pre.code').innerText`), /^# Title/);
      await press('Rendered');
      await waitFor(mdInfo, { message: 'the rendered view again' });
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



    // ---- interface details from manual testing --------------------------------
    test('the preview pane is a checkbox like the other view options', async () => {
      await open(fx.work);
      const box = `[...document.querySelectorAll('.toggle')].find((l) => l.textContent.includes('Preview pane')).querySelector('input')`;
      const checked = () => d.eval(`return ${box}.checked`);
      const start = await previewOpen();
      assert.equal(await checked(), start, 'the checkbox reflects the pane');
      assert.equal(await d.eval(`return [...document.querySelectorAll('.tools .textbtn')].some((b) => /preview/i.test(b.textContent))`), false, 'the old show/hide button is gone');
      const p = await d.eval(`const b = ${box}.getBoundingClientRect(); return { x: b.x + b.width / 2, y: b.y + b.height / 2 }`);
      await d.click(p.x, p.y);
      await waitFor(async () => (await previewOpen()) === !start && (await checked()) === !start, { message: 'the checkbox to toggle the pane' });
      await d.click(p.x, p.y);
      await waitFor(async () => (await previewOpen()) === start && (await checked()) === start, { message: 'the checkbox to toggle the pane back' });
      // Space and the checkbox stay in step.
      await d.eval(`document.activeElement?.blur?.(); document.body.focus();`);
      await d.key(' ');
      await waitFor(async () => (await previewOpen()) === !start && (await checked()) === !start, { message: 'Space to update the checkbox' });
    });

    test('the Columns menu closes on an outside click and on Escape', async () => {
      await open(fx.work);
      const isOpen = () => d.eval(`return document.querySelector('.colmenu').open`);
      const openMenu = async () => {
        const s = await pointOf('.colmenu summary');
        await d.click(s.x, s.y);
        await waitFor(isOpen, { message: 'the menu to open' });
      };
      await openMenu();
      // A click inside the menu keeps it open.
      const inside = await d.eval(`const b = document.querySelector('.colmenu .menu').getBoundingClientRect(); return { x: b.x + 4, y: b.y + 4 }`);
      await d.click(inside.x, inside.y);
      await sleep(200);
      assert.equal(await isOpen(), true, 'a click inside the menu must not close it');
      // A click anywhere else closes it.
      const outside = await pointOf('.status');
      await d.click(outside.x, outside.y);
      await waitFor(async () => !(await isOpen()), { message: 'an outside click to close the menu' });
      // Escape closes it too.
      await openMenu();
      await d.key('Escape');
      await waitFor(async () => !(await isOpen()), { message: 'Escape to close the menu' });
    });

    test('the current-folder link leaves a search and returns to the listing', async () => {
      await open(fx.home);
      await d.eval(`document.querySelector('input[aria-label="Search subfolders by name"]').focus()`);
      await d.type('needle');
      await d.key('Enter');
      await waitFor(async () => (await rows()).length === 2, { message: 'the search results' });
      assert.equal(await d.eval(`return document.querySelector('.crumbs .crumb.current')?.tagName`), 'A', 'while results are shown the current crumb is a link');
      const p = await pointOf('.crumbs .crumb.current');
      await d.click(p.x, p.y);
      await waitFor(async () => JSON.stringify(await rows()) === JSON.stringify(fx.homeRows), { message: 'the folder listing to return' });
      assert.equal(await d.eval(`return document.querySelector('.crumbs .crumb.current')?.tagName`), 'SPAN');
      assert.equal(await d.eval(`return document.querySelector('.status').textContent.includes('Clear search')`), false);
    });

    test('a binary attribute is labelled hex before its digits', async (t) => {
      if (!fx.xattrsHex) return t.skip('extended attributes are not available here');
      await open(fx.work);
      await preview('data.json');
      const r = await d.eval(`const li = [...document.querySelectorAll('.preview .xattrs li')].find((l) => l.querySelector('.xname')?.textContent.includes('com.example.blob'));
        if (!li) return null;
        const tag = li.querySelector('.tag');
        const pre = li.querySelector('pre.xval');
        return { tag: tag?.textContent ?? null, value: pre?.textContent ?? null, tagFirst: !!(tag && pre && (tag.compareDocumentPosition(pre) & Node.DOCUMENT_POSITION_FOLLOWING)), trailingNote: !!li.querySelector('.note') };`);
      assert.ok(r, 'the binary attribute is listed');
      assert.equal(r.tag, 'hex');
      assert.equal(r.value, 'de ad be ef 00 01');
      assert.equal(r.tagFirst, true, 'the label must come before the digits');
      assert.equal(r.trailingNote, false, 'nothing may follow the digits');
    });


    test('the preview pane can be resized by dragging its edge or with the keyboard, and remembers its width', async () => {
      await open(fx.work);
      if (!(await previewOpen())) await d.key(' ');
      const width = () => d.eval(`return Math.round(document.querySelector('.preview').getBoundingClientRect().width)`);
      const saved = () => d.eval(`return JSON.parse(localStorage.getItem('fsb.prefs.v1') ?? 'null')?.previewWidth ?? null`);
      const start = await width();
      assert.equal(start, 380, 'the default width');

      // Drag the edge to the left: the pane grows.
      const edge = await pointOf('.psplit');
      await d.drag(edge, { x: edge.x - 100, y: edge.y });
      await waitFor(async () => (await width()) === start + 100, { message: 'the drag to widen the pane' });
      assert.equal(await saved(), start + 100);

      // Keyboard: Left widens by 10, Shift+Right narrows by 50.
      await d.eval(`document.querySelector('.psplit').focus()`);
      await pressTimes('ArrowLeft', 3);
      await waitFor(async () => (await width()) === start + 130, { message: 'the keyboard to widen the pane' });
      await d.key('ArrowRight', { shift: true });
      await waitFor(async () => (await width()) === start + 80, { message: 'Shift+Right to narrow the pane' });

      // Limits: never below the minimum.
      await d.eval(`const h = document.querySelector('.psplit'); for (let i = 0; i < 60; i++) h.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', shiftKey: true, bubbles: true }))`);
      await waitFor(async () => (await width()) === 240, { message: 'the minimum width' });

      // It survives a reload, and a double click restores the default.
      await d.goto('about:blank');
      await d.goto(`${base}/${hashFor(fx.work)}`);
      await waitFor(async () => (await previewOpen()) && (await width()) === 240, { message: 'the saved width to be restored' });
      const again = await pointOf('.psplit');
      await d.doubleClick(again.x, again.y);
      await waitFor(async () => (await width()) === 380, { message: 'a double click to reset the width' });
      assert.equal(await saved(), 380);
    });


    test('sorts like eza by default, and Folders first is an option that applies to every column', async () => {
      await open(fx.work);
      // Show dotfiles so the leading-dot rule is visible.
      await d.eval(`[...document.querySelectorAll('.toggle')].find((l) => l.textContent.includes('Hidden files')).querySelector('input').click()`);
      await waitFor(async () => (await rows()).includes('.envrc'), { message: 'dotfiles to appear' });
      // Case-insensitive, dotfiles first, folders NOT grouped (src/ sits among the files by name).
      assert.deepEqual(await rows(), ['.envrc', 'app.log', 'data.json', 'disguised.png', 'doc.pdf', 'fake.pdf', 'link-to-main', 'needle-shallow.txt', 'people.csv', 'random.bin', 'README.md', 'src/']);

      const foldersFirst = `[...document.querySelectorAll('.toggle')].find((l) => l.textContent.includes('Folders first')).querySelector('input')`;
      const box = await d.eval(`const b = ${foldersFirst}.getBoundingClientRect(); return { x: b.x + b.width / 2, y: b.y + b.height / 2 }`);
      const sizeHeader = async () => { const p = await pointOf('.head [data-col="size"] button'); await d.click(p.x, p.y); };

      // Sorting by size: a folder counts as smaller than any file, so it leads ascending and trails descending.
      await sizeHeader();
      await waitFor(async () => (await rows())[0] === 'src/', { message: 'src/ first when sorting by size ascending' });
      await sizeHeader();
      await waitFor(async () => (await rows()).at(-1) === 'src/', { message: 'src/ last when sorting by size descending' });

      // Folders first keeps folders in front for BOTH directions.
      await d.click(box.x, box.y);
      await waitFor(async () => (await rows())[0] === 'src/', { message: 'src/ first with Folders first (descending)' });
      await sizeHeader(); // back to ascending
      await waitFor(async () => (await d.eval(`return document.querySelector('.head [data-col="size"]').getAttribute('aria-sort')`)) === 'ascending');
      assert.equal((await rows())[0], 'src/');

      // The choice is remembered.
      assert.equal(await d.eval(`return JSON.parse(localStorage.getItem('fsb.prefs.v1')).foldersFirst`), true);
    });


    test('Match case applies to the filter and to search', async () => {
      await open(`${fx.home}/casetest`);
      const matchCase = `[...document.querySelectorAll('.toggle')].find((l) => l.textContent.includes('Match case')).querySelector('input')`;
      const box = await d.eval(`const b = ${matchCase}.getBoundingClientRect(); return { x: b.x + b.width / 2, y: b.y + b.height / 2 }`);

      // Filter: case-insensitive by default.
      await d.eval(`document.activeElement?.blur?.(); document.body.focus();`);
      await d.key('/');
      await d.type('Report');
      await waitFor(async () => JSON.stringify(await rows()) === JSON.stringify(['report-draft.txt', 'Report-final.txt']), { message: 'both names to match ignoring case' });
      await d.click(box.x, box.y);
      await waitFor(async () => JSON.stringify(await rows()) === JSON.stringify(['Report-final.txt']), { message: 'Match case to narrow the filter' });
      assert.equal(await d.eval(`return JSON.parse(localStorage.getItem('fsb.prefs.v1')).matchCase`), true);
      await d.click(box.x, box.y);
      await waitFor(async () => (await rows()).length === 2, { message: 'unchecking to widen the filter again' });

      // Search: the same option, and toggling it re-runs the search.
      await open(fx.home);
      await d.eval(`document.querySelector('input[aria-label="Search subfolders by name"]').focus()`);
      await d.type('Report');
      await d.key('Enter');
      await waitFor(async () => (await rows()).length === 2 && !(await d.eval(`return document.querySelector('.status').textContent.includes('Searching')`)), { message: 'the case-insensitive search results' });
      const b2 = await d.eval(`const b = ${matchCase}.getBoundingClientRect(); return { x: b.x + b.width / 2, y: b.y + b.height / 2 }`);
      await d.click(b2.x, b2.y);
      await waitFor(async () => JSON.stringify(await rows()) === JSON.stringify(['casetest/Report-final.txt']), { message: 'the search to re-run with Match case' });
    });


    test('Go to path jumps to any folder inside the served roots, with helpful messages', async () => {
      await open(fx.home);
      const goTo = async (text) => {
        await d.eval(`document.activeElement?.blur?.(); document.body.focus();`);
        await d.key('g');
        await waitFor(async () => (await d.eval(`return document.activeElement?.getAttribute('aria-label')`)) === 'Go to path', { message: 'g to focus the box' });
        await d.type(text);
        await d.key('Enter');
      };
      // An absolute path.
      await goTo(`${fx.work}/src`);
      await waitFor(async () => (await title()) === 'src - fsb', { message: 'to open src' });
      assert.deepEqual(await rows(), ['deep/', 'main.go']);
      // ~ expands to the home folder.
      await goTo('~/pics');
      await waitFor(async () => (await title()) === 'pics - fsb', { message: '~/pics to open' });
      // Messy input is cleaned.
      await goTo(`${fx.work}//src/../src/./deep/`);
      await waitFor(async () => (await title()) === 'deep - fsb', { message: 'a messy path to be cleaned' });
      // A relative path is refused with an explanation, and nothing moves.
      await goTo('work');
      const msg = await waitFor(() => d.eval(`return document.querySelector('.goerr')?.textContent ?? null`), { message: 'a message for a relative path' });
      assert.match(msg, /absolute path/);
      assert.equal(await title(), 'deep - fsb');
      // A path outside the served roots says so and lists what is served.
      await goTo('/etc');
      const outside = await waitFor(() => d.eval(`return document.querySelector('.msg.error:not(.goerr)')?.textContent ?? null`), { message: 'a message for a path outside the roots' });
      assert.match(outside, /outside the folders fsb serves/);
      assert.ok(outside.includes(fx.home), 'the message lists the served folders');
      // A denied folder inside the roots looks like any missing one: no "outside" hint, no other difference.
      // (Wait for the NEW message: the previous one is still on screen until the next request fails.)
      const errorFor = (name) =>
        waitFor(async () => {
          if ((await d.eval(`return document.title`)) !== `${name} - fsb`) return null;
          const m = await d.eval(`return document.querySelector('.msg.error:not(.goerr)')?.textContent ?? null`);
          return m && !/outside the folders/.test(m) ? m : null;
        }, { message: `the error for ${name}` });
      await goTo(`${fx.home}/.ssh`);
      const denied = await errorFor('.ssh');
      await goTo(`${fx.home}/nope`);
      const missing = await errorFor('nope');
      assert.equal(denied, missing, 'a denied folder must look exactly like a missing one');
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
