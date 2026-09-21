<script lang="ts">
  import hljs from 'highlight.js/lib/core';
  import bash from 'highlight.js/lib/languages/bash';
  import c from 'highlight.js/lib/languages/c';
  import cpp from 'highlight.js/lib/languages/cpp';
  import css from 'highlight.js/lib/languages/css';
  import diff from 'highlight.js/lib/languages/diff';
  import dockerfile from 'highlight.js/lib/languages/dockerfile';
  import go from 'highlight.js/lib/languages/go';
  import ini from 'highlight.js/lib/languages/ini';
  import java from 'highlight.js/lib/languages/java';
  import javascript from 'highlight.js/lib/languages/javascript';
  import json from 'highlight.js/lib/languages/json';
  import kotlin from 'highlight.js/lib/languages/kotlin';
  import makefile from 'highlight.js/lib/languages/makefile';
  import markdown from 'highlight.js/lib/languages/markdown';
  import php from 'highlight.js/lib/languages/php';
  import python from 'highlight.js/lib/languages/python';
  import ruby from 'highlight.js/lib/languages/ruby';
  import rust from 'highlight.js/lib/languages/rust';
  import sql from 'highlight.js/lib/languages/sql';
  import swift from 'highlight.js/lib/languages/swift';
  import typescript from 'highlight.js/lib/languages/typescript';
  import xml from 'highlight.js/lib/languages/xml';
  import yaml from 'highlight.js/lib/languages/yaml';

  import { ApiError, getHead, getMeta, previewURL, type Head, type Meta, type Row } from './api';
  import { basename, formatDate, formatSize, kindOf, modeString } from './format';
  import { formatFor, languageFor, looksLikeImage, parseDelimited, plural, prettyJSON } from './preview';

  const languages = {
    bash, c, cpp, css, diff, dockerfile, go, ini, java, javascript, json, kotlin, makefile,
    markdown, php, python, ruby, rust, sql, swift, typescript, xml, yaml,
  };
  for (const [name, def] of Object.entries(languages)) {
    if (!hljs.getLanguage(name)) hljs.registerLanguage(name, def);
  }

  interface Props {
    /** The selected row, or null. */
    entry: Row | null;
    /** Its full path. */
    fullPath: string;
    onclose: () => void;
    oncopy: (text: string) => void;
  }

  let { entry, fullPath, onclose, oncopy }: Props = $props();

  const HEAD_BYTES = 64 * 1024;
  const CSV_ROWS = 200;

  type Mode = 'idle' | 'loading' | 'folder' | 'image' | 'text' | 'binary' | 'empty' | 'dataless' | 'error';

  let mode = $state<Mode>('idle');
  let meta = $state<Meta | null>(null);
  let head = $state<Head | null>(null);
  let error = $state('');

  // Load meta and content for the selected entry. A short debounce keeps
  // holding an arrow key from firing a request per row.
  $effect(() => {
    const e = entry;
    const p = fullPath;
    meta = null;
    head = null;
    error = '';
    if (!e || !p) {
      mode = 'idle';
      return;
    }
    mode = 'loading';
    const ctrl = new AbortController();
    const timer = setTimeout(() => {
      getMeta(p, true, ctrl.signal)
        .then((m) => (meta = m))
        .catch((err) => {
          if (!ctrl.signal.aborted) error = describe(err);
        });
      if (e.isDir) mode = 'folder';
      else if (e.broken) mode = 'error';
      else if (looksLikeImage(e.name)) mode = 'image';
      else loadHead(p, ctrl);
    }, 120);
    return () => {
      clearTimeout(timer);
      ctrl.abort();
    };
  });

  function loadHead(p: string, ctrl: AbortController) {
    getHead(p, HEAD_BYTES, ctrl.signal)
      .then((h) => {
        head = h;
        mode = h.kind;
      })
      .catch((err) => {
        if (ctrl.signal.aborted) return;
        error = describe(err);
        mode = 'error';
      });
  }

  // The server refused to serve this as an image (not really a PNG/JPEG/GIF/
  // WebP): fall back to treating it as an ordinary file.
  function imageFailed() {
    if (!entry) return;
    mode = 'loading';
    loadHead(fullPath, new AbortController());
  }

  function describe(err: unknown): string {
    if (err instanceof ApiError) {
      if (err.status === 404) return 'Not available.';
      if (err.status === 409) return 'Stored in the cloud and not downloaded. fsb will not trigger a download.';
      if (err.status === 403) return 'Permission denied.';
      return `Server error (${err.status}).`;
    }
    return err instanceof Error ? err.message : String(err);
  }

  // A symlink is presented according to what it points at (link-to-main -> main.go).
  const typeName = $derived(entry?.isSymlink && meta?.symlinkTarget ? basename(meta.symlinkTarget) : (entry?.name ?? ''));
  const format = $derived(entry ? formatFor(typeName) : 'text');
  const pretty = $derived(head?.kind === 'text' && format === 'json' && !head.truncated ? prettyJSON(head.text ?? '') : null);

  const highlighted = $derived.by(() => {
    if (mode !== 'text' || !head?.text) return '';
    const source = pretty ?? head.text;
    const lang = pretty ? 'json' : languageFor(typeName);
    if (!lang || !hljs.getLanguage(lang)) return '';
    try {
      // highlight.js escapes the source, so its output is safe to insert as HTML.
      return hljs.highlight(source, { language: lang, ignoreIllegals: true }).value;
    } catch {
      return '';
    }
  });

  const table = $derived(
    mode === 'text' && head?.text && (format === 'csv' || format === 'tsv')
      ? parseDelimited(head.text, format === 'csv' ? ',' : '\t', CSV_ROWS)
      : null,
  );
</script>

<aside class="preview" aria-label="Preview">
  <header class="phead">
    <strong class="ptitle" title={fullPath}>{entry?.rel ?? entry?.name ?? 'Preview'}</strong>
    <span class="pactions">
      {#if entry}<button onclick={() => oncopy(fullPath)} title="Copy the full path (c)">Copy path</button>{/if}
      <button onclick={onclose} aria-label="Close preview" title="Close (Space)">✕</button>
    </span>
  </header>

  <div class="pbody">
    {#if !entry}
      <p class="hint">Select an item with ↑ ↓ to preview it. Space shows or hides this pane.</p>
    {:else}
      {#if mode === 'loading'}
        <p class="hint">Loading…</p>
      {:else if mode === 'folder'}
        <p class="hint">Folder</p>
      {:else if mode === 'image'}
        <img class="pimg" src={previewURL(fullPath)} alt={entry.name} onerror={imageFailed} />
      {:else if mode === 'binary'}
        <p class="hint">Binary file: no preview.</p>
      {:else if mode === 'empty'}
        <p class="hint">Empty file.</p>
      {:else if mode === 'dataless'}
        <p class="hint">Stored in the cloud and not downloaded. fsb will not trigger a download.</p>
      {:else if mode === 'error'}
        <p class="hint error">{error || 'No preview available.'}</p>
      {:else if mode === 'text' && head}
        {#if table}
          <div class="tablewrap">
            <table class="csv">
              <thead>
                <tr>{#each table.rows[0] ?? [] as cell, i (i)}<th>{cell}</th>{/each}</tr>
              </thead>
              <tbody>
                {#each table.rows.slice(1) as r, ri (ri)}
                  <tr>{#each r as cell, ci (ci)}<td>{cell}</td>{/each}</tr>
                {/each}
              </tbody>
            </table>
          </div>
          <p class="note">
            {plural(table.rows.length, 'row')}{table.more || head.truncated ? ' shown (more in the file)' : ''}
          </p>
        {:else if highlighted}
          <pre class="code hljs"><code>{@html highlighted}</code></pre>
        {:else}
          <pre class="code"><code>{head.text}</code></pre>
        {/if}
        {#if head.truncated && !table}
          <p class="note">Showing the first {formatSize(HEAD_BYTES)} of {formatSize(head.size)}.</p>
        {/if}
      {/if}

      <section class="meta" aria-label="Details">
        <h3>Details</h3>
        {#if meta}
          <dl>
            <dt>Path</dt><dd class="mono wrap">{meta.path}</dd>
            <dt>Kind</dt><dd>{kindOf(meta)}</dd>
            {#if !meta.isDir}<dt>Size</dt><dd>{formatSize(meta.size)} ({meta.size.toLocaleString()} bytes)</dd>{/if}
            <dt>Modified</dt><dd>{formatDate(meta.modTime)}</dd>
            <dt>Permissions</dt><dd class="mono">{modeString(meta)}</dd>
            {#if meta.symlinkTarget}<dt>Link to</dt><dd class="mono wrap">{meta.symlinkTarget}</dd>{/if}
            {#if meta.dataless}<dt>Cloud</dt><dd>Not downloaded</dd>{/if}
          </dl>
          <h3>Extended attributes</h3>
          {#if meta.xattrs.length === 0}
            <p class="note">None.</p>
          {:else}
            <ul class="xattrs">
              {#each meta.xattrs as x (x.name)}
                <li>
                  <span class="mono xname">{x.name}</span>{#if x.encoding === 'hex' && !x.large}<span class="tag">hex</span>{/if}
                  {#if x.large}
                    <span class="note">({formatSize(x.size ?? 0)}, too large to show)</span>
                  {:else if x.value !== undefined && x.value !== ''}
                    <pre class="xval">{x.value}</pre>
                  {:else}
                    <span class="note">(empty)</span>
                  {/if}
                </li>
              {/each}
            </ul>
          {/if}
        {:else if error && mode !== 'error'}
          <p class="hint error">{error}</p>
        {:else}
          <p class="hint">Loading…</p>
        {/if}
      </section>
    {/if}
  </div>
</aside>
