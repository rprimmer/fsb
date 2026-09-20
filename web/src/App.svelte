<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import VirtualList from './lib/VirtualList.svelte';
  import { ApiError, fileURL, getStatus, streamList, type Entry, type Status } from './lib/api';
  import {
    crumbsFor,
    formatDate,
    formatSize,
    hashToPath,
    joinPath,
    kindOf,
    modeString,
    pathToHash,
  } from './lib/format';

  type SortKey = 'name' | 'size' | 'modTime' | 'kind';

  const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

  let status = $state<Status | null>(null);
  let path = $state('');
  let entries = $state.raw<Entry[]>([]);
  let loading = $state(false);
  let error = $state('');
  let filter = $state('');
  let showHidden = $state(false);
  let sortKey = $state<SortKey>('name');
  let sortAsc = $state(true);
  let filterInput: HTMLInputElement | undefined;
  let list: { reset(): void } | undefined;

  const crumbs = $derived(status && path ? crumbsFor(path, status.roots) : []);

  const visible = $derived.by(() => {
    const q = filter.trim().toLowerCase();
    const rows = entries.filter(
      (e) => (showHidden || !e.name.startsWith('.')) && (!q || e.name.toLowerCase().includes(q)),
    );
    const dir = sortAsc ? 1 : -1;
    rows.sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1; // folders first
      let c = 0;
      switch (sortKey) {
        case 'name':
          c = collator.compare(a.name, b.name);
          break;
        case 'size':
          c = a.size - b.size;
          break;
        case 'modTime':
          c = Date.parse(a.modTime) - Date.parse(b.modTime);
          break;
        case 'kind':
          c = collator.compare(kindOf(a), kindOf(b));
          break;
      }
      return (c || collator.compare(a.name, b.name)) * dir;
    });
    return rows;
  });

  function describe(e: unknown): string {
    if (e instanceof ApiError) {
      if (e.status === 404) return 'Not found. The folder does not exist or is not available.';
      if (e.status === 403 && /permission/i.test(e.message)) {
        return 'Permission denied by macOS. You may need to grant your terminal access in System Settings > Privacy & Security.';
      }
      if (e.status === 403) return 'Session not authorized. Re-open the single-use URL that fsb printed when it started.';
      return `Server error (${e.status}).`;
    }
    return e instanceof Error ? e.message : String(e);
  }

  function sortBy(key: SortKey) {
    if (sortKey === key) sortAsc = !sortAsc;
    else {
      sortKey = key;
      sortAsc = true;
    }
  }

  function ariaSort(key: SortKey): 'ascending' | 'descending' | 'none' {
    return sortKey !== key ? 'none' : sortAsc ? 'ascending' : 'descending';
  }

  function onKey(ev: KeyboardEvent) {
    const typing = document.activeElement instanceof HTMLInputElement;
    if (ev.key === '/' && !typing) {
      ev.preventDefault();
      filterInput?.focus();
    } else if (ev.key === 'Escape' && typing) {
      filter = '';
      filterInput?.blur();
    }
  }

  onMount(() => {
    const onHash = () => {
      const p = hashToPath(location.hash);
      if (p) path = p;
    };
    window.addEventListener('hashchange', onHash);
    window.addEventListener('keydown', onKey);
    getStatus()
      .then((s) => {
        status = s;
        if (!hashToPath(location.hash) && s.roots.length) {
          history.replaceState(null, '', pathToHash(s.roots[0]));
        }
        onHash();
      })
      .catch((e) => (error = describe(e)));
    return () => {
      window.removeEventListener('hashchange', onHash);
      window.removeEventListener('keydown', onKey);
    };
  });

  // Load (stream) the directory whenever the path changes.
  $effect(() => {
    const p = path;
    if (!p) return;
    const ctrl = new AbortController();
    entries = [];
    error = '';
    loading = true;
    filter = '';
    untrack(() => list?.reset());

    // Throttle state updates so a 100k-entry directory does not re-sort on
    // every chunk.
    let buf: Entry[] = [];
    let acc: Entry[] = [];
    let timer: ReturnType<typeof setTimeout> | undefined;
    const flush = () => {
      timer = undefined;
      if (buf.length) {
        acc = acc.concat(buf);
        buf = [];
        entries = acc;
      }
    };

    streamList(
      p,
      (es) => {
        buf.push(...es);
        if (acc.length === 0) flush(); // paint the first chunk immediately
        else timer ??= setTimeout(flush, 120);
      },
      ctrl.signal,
    )
      .then(() => {
        clearTimeout(timer);
        flush();
        loading = false;
      })
      .catch((e) => {
        if (ctrl.signal.aborted) return;
        clearTimeout(timer);
        flush();
        loading = false;
        error = describe(e);
      });

    return () => {
      ctrl.abort();
      clearTimeout(timer);
    };
  });

  $effect(() => {
    document.title = path ? `${path.split('/').filter(Boolean).pop() ?? '/'} - fsb` : 'fsb';
  });
</script>

{#if status && status.coreDenyMissing.length > 0}
  <div class="banner" role="alert">
    <strong>Warning:</strong> core deny rule(s) disabled: <code>{status.coreDenyMissing.join(', ')}</code>. These paths
    are browsable. Restore them in <code>~/.config/fsb/deny</code> (see the README section on deny rules).
  </div>
{/if}

<header class="bar">
  <nav class="crumbs" aria-label="Path">
    {#each crumbs as c, i (c.path)}
      {#if i > 0}<span class="sep" aria-hidden="true">/</span>{/if}
      {#if i === crumbs.length - 1}
        <span class="crumb current" aria-current="page">{c.label}</span>
      {:else}
        <a class="crumb" href={pathToHash(c.path)}>{c.label}</a>
      {/if}
    {/each}
  </nav>
  <span class="badge" title="fsb never writes to your filesystem">read-only</span>
</header>

<div class="tools">
  <input
    bind:this={filterInput}
    bind:value={filter}
    type="search"
    placeholder="Filter this folder   ( / )"
    aria-label="Filter this folder"
    autocomplete="off"
    spellcheck="false"
  />
  <label class="toggle"><input type="checkbox" bind:checked={showHidden} /> Show hidden files</label>
</div>

<div class="table" role="grid" aria-label="Files" aria-rowcount={visible.length}>
  <div class="row head" role="row">
    <div role="columnheader" class="c-name" aria-sort={ariaSort('name')}>
      <button onclick={() => sortBy('name')}>Name{sortKey === 'name' ? (sortAsc ? ' ▲' : ' ▼') : ''}</button>
    </div>
    <div role="columnheader" class="c-size" aria-sort={ariaSort('size')}>
      <button onclick={() => sortBy('size')}>Size{sortKey === 'size' ? (sortAsc ? ' ▲' : ' ▼') : ''}</button>
    </div>
    <div role="columnheader" class="c-date" aria-sort={ariaSort('modTime')}>
      <button onclick={() => sortBy('modTime')}>Modified{sortKey === 'modTime' ? (sortAsc ? ' ▲' : ' ▼') : ''}</button>
    </div>
    <div role="columnheader" class="c-kind" aria-sort={ariaSort('kind')}>
      <button onclick={() => sortBy('kind')}>Kind{sortKey === 'kind' ? (sortAsc ? ' ▲' : ' ▼') : ''}</button>
    </div>
    <div role="columnheader" class="c-mode">Permissions</div>
  </div>

  {#if error}
    <p class="msg error" role="alert">{error}</p>
  {:else if !loading && visible.length === 0}
    <p class="msg">{entries.length === 0 ? 'This folder is empty.' : 'No items match.'}</p>
  {/if}

  <VirtualList bind:this={list} items={visible} label="Directory entries">
    {#snippet row(e: Entry)}
      <div class="row" role="row">
        <div role="gridcell" class="c-name">
          {#if e.broken}
            <span class="name broken" title="Broken symlink">{e.name}</span>
          {:else if e.isDir}
            <a class="name dir" href={pathToHash(joinPath(path, e.name))}>{e.name}/</a>
          {:else}
            <a class="name" href={fileURL(joinPath(path, e.name))} download={e.name}>{e.name}</a>
          {/if}
          {#if e.isSymlink}<span class="link" title="Symbolic link">→</span>{/if}
        </div>
        <div role="gridcell" class="c-size">{e.isDir ? '' : formatSize(e.size)}</div>
        <div role="gridcell" class="c-date">{formatDate(e.modTime)}</div>
        <div role="gridcell" class="c-kind">{kindOf(e)}</div>
        <div role="gridcell" class="c-mode mono">{modeString(e)}</div>
      </div>
    {/snippet}
  </VirtualList>
</div>

<footer class="status" aria-live="polite">
  {#if loading}Loading… {entries.length.toLocaleString()} items so far
  {:else}{visible.length.toLocaleString()} of {entries.length.toLocaleString()} items{/if}
</footer>
