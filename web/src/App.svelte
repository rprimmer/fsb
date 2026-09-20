<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import VirtualList from './lib/VirtualList.svelte';
  import { ApiError, fileURL, getStatus, streamList, type Entry, type Status } from './lib/api';
  import {
    COLUMNS,
    MAX_WIDTH,
    MIN_WIDTH,
    clampWidth,
    defaultLayout,
    gridTemplate,
    isSortable,
    moveBy,
    moveColumn,
    parseLayout,
    totalWidth,
    type ColId,
    type Layout,
    type SortKey,
  } from './lib/columns';
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

  const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });
  const LAYOUT_KEY = 'fsb.columns.v1';

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

  // Column layout: a per-viewer preference, kept in localStorage (which can be
  // unavailable or empty, so every access is guarded).
  let layout = $state<Layout>(loadLayout());
  let resizing: { id: ColId; startX: number; startW: number } | null = null;
  let draggingCol = $state<ColId | null>(null);
  let dropTarget = $state<ColId | null>(null);

  function loadLayout(): Layout {
    try {
      return parseLayout(localStorage.getItem(LAYOUT_KEY));
    } catch {
      return defaultLayout();
    }
  }

  function saveLayout() {
    try {
      localStorage.setItem(LAYOUT_KEY, JSON.stringify(layout));
    } catch {
      /* storage unavailable: the layout just won't persist */
    }
  }

  function setWidth(id: ColId, w: number) {
    layout = { ...layout, widths: { ...layout.widths, [id]: clampWidth(w) } };
  }

  function resetLayout() {
    layout = defaultLayout();
    try {
      localStorage.removeItem(LAYOUT_KEY);
    } catch {
      /* ignore */
    }
  }

  // --- resizing: drag the edge, or focus it and use the keyboard -------------
  function startResize(ev: PointerEvent, id: ColId) {
    ev.preventDefault(); // also stops the header's native drag from starting
    (ev.currentTarget as HTMLElement).setPointerCapture(ev.pointerId);
    resizing = { id, startX: ev.clientX, startW: layout.widths[id] };
  }

  function moveResize(ev: PointerEvent) {
    if (resizing) setWidth(resizing.id, resizing.startW + ev.clientX - resizing.startX);
  }

  function endResize() {
    if (!resizing) return;
    resizing = null;
    saveLayout();
  }

  function resizeKey(ev: KeyboardEvent, id: ColId) {
    const step = ev.shiftKey ? 50 : 10;
    if (ev.key === 'ArrowLeft' || ev.key === 'ArrowRight') {
      ev.preventDefault();
      setWidth(id, layout.widths[id] + (ev.key === 'ArrowLeft' ? -step : step));
      saveLayout();
    } else if (ev.key === 'Enter') {
      ev.preventDefault();
      fitColumn(id);
    }
  }

  let measureCtx: CanvasRenderingContext2D | null = null;

  /** Fits a column to the widest content among the rows currently rendered. */
  function fitColumn(id: ColId) {
    measureCtx ??= document.createElement('canvas').getContext('2d');
    const ctx = measureCtx;
    if (!ctx) return;
    const measure = (el: Element, text: string) => {
      ctx.font = getComputedStyle(el).font;
      return ctx.measureText(text).width;
    };
    let widest = 0;
    for (const cell of document.querySelectorAll<HTMLElement>(`.vrow [data-col="${id}"]`)) {
      const inner = cell.querySelector('.name') ?? cell; // folder names are bold
      widest = Math.max(widest, measure(inner, cell.textContent?.trim() ?? ''));
    }
    const head = document.querySelector<HTMLElement>(`.head [data-col="${id}"]`);
    if (head) widest = Math.max(widest, measure(head, head.textContent?.trim() ?? '') + 20);
    setWidth(id, Math.ceil(widest) + 8);
    saveLayout();
  }

  // --- reordering: drag a header onto another, or Alt+Arrow on a header ------
  function dragStart(ev: DragEvent, id: ColId) {
    draggingCol = id;
    ev.dataTransfer?.setData('text/plain', id); // Firefox needs data to start a drag
    if (ev.dataTransfer) ev.dataTransfer.effectAllowed = 'move';
  }

  function dragOver(ev: DragEvent, id: ColId) {
    if (!draggingCol) return; // not one of our headers (e.g. a file dragged in)
    ev.preventDefault();
    dropTarget = id;
  }

  function drop(ev: DragEvent, id: ColId) {
    ev.preventDefault();
    if (draggingCol) {
      layout = { ...layout, order: moveColumn(layout.order, draggingCol, id) };
      saveLayout();
    }
    endDrag();
  }

  function endDrag() {
    draggingCol = null;
    dropTarget = null;
  }

  function headerKey(ev: KeyboardEvent, id: ColId) {
    if (ev.altKey && (ev.key === 'ArrowLeft' || ev.key === 'ArrowRight')) {
      ev.preventDefault();
      layout = { ...layout, order: moveBy(layout.order, id, ev.key === 'ArrowLeft' ? -1 : 1) };
      saveLayout();
    }
  }

  // --- data ------------------------------------------------------------------
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
        return 'Permission denied by the operating system or security software. On macOS, check System Settings > Privacy & Security, and any security tool that guards this folder.';
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

{#snippet cell(id: ColId, e: Entry)}
  {#if id === 'name'}
    <div role="gridcell" class="c-name" data-col="name">
      {#if e.broken}
        <span class="name broken" title="Broken symlink">{e.name}</span>
      {:else if e.isDir}
        <a class="name dir" href={pathToHash(joinPath(path, e.name))}>{e.name}/</a>
      {:else}
        <a class="name" href={fileURL(joinPath(path, e.name))} download={e.name}>{e.name}</a>
      {/if}
      {#if e.isSymlink}<span class="link" title="Symbolic link">→</span>{/if}
    </div>
  {:else if id === 'size'}
    <div role="gridcell" class="c-size" data-col="size">{e.isDir ? '' : formatSize(e.size)}</div>
  {:else if id === 'modTime'}
    <div role="gridcell" class="c-date" data-col="modTime">{formatDate(e.modTime)}</div>
  {:else if id === 'kind'}
    <div role="gridcell" class="c-kind" data-col="kind">{kindOf(e)}</div>
  {:else}
    <div role="gridcell" class="c-mode mono" data-col="mode">{modeString(e)}</div>
  {/if}
{/snippet}

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
  <button
    class="textbtn"
    onclick={resetLayout}
    title="Drag a header to reorder, drag its edge to resize (double-click to fit). Keyboard: Alt+Left/Right moves a column; on an edge, Left/Right resizes and Enter fits."
  >
    Reset columns
  </button>
</div>

<div class="table">
  <div
    class="grid"
    role="grid"
    aria-label="Files"
    aria-rowcount={visible.length}
    style:--cols={gridTemplate(layout)}
    style:--total="{totalWidth(layout)}px"
  >
    <div class="row head" role="row">
      {#each layout.order as id (id)}
        <div
          role="columnheader"
          class="hcell {COLUMNS[id].cls}"
          data-col={id}
          aria-sort={isSortable(id) ? ariaSort(id) : undefined}
        >
          <div
            class="hdr"
            class:drop={dropTarget === id && draggingCol !== id}
            class:dragging={draggingCol === id}
            role="presentation"
            draggable="true"
            ondragstart={(ev) => dragStart(ev, id)}
            ondragover={(ev) => dragOver(ev, id)}
            ondrop={(ev) => drop(ev, id)}
            ondragend={endDrag}
          >
            <button
              onclick={() => isSortable(id) && sortBy(id)}
              onkeydown={(ev) => headerKey(ev, id)}
              title={isSortable(id) ? 'Click to sort. Drag or Alt+Left/Right to move.' : 'Drag or Alt+Left/Right to move.'}
            >
              {COLUMNS[id].label}{isSortable(id) && sortKey === id ? (sortAsc ? ' ▲' : ' ▼') : ''}
            </button>
          </div>
          <!-- A focusable separator with aria-valuenow is the ARIA "window splitter" widget; the lint rules treat it as static. -->
          <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
          <div
            class="resize"
            role="separator"
            aria-orientation="vertical"
            aria-label="Resize {COLUMNS[id].label} column"
            aria-valuenow={layout.widths[id]}
            aria-valuemin={MIN_WIDTH}
            aria-valuemax={MAX_WIDTH}
            tabindex="0"
            onpointerdown={(ev) => startResize(ev, id)}
            onpointermove={moveResize}
            onpointerup={endResize}
            onpointercancel={endResize}
            onkeydown={(ev) => resizeKey(ev, id)}
            ondblclick={() => fitColumn(id)}
          ></div>
        </div>
      {/each}
    </div>

    {#if error}
      <p class="msg error" role="alert">{error}</p>
    {:else if !loading && visible.length === 0}
      <p class="msg">{entries.length === 0 ? 'This folder is empty.' : 'No items match.'}</p>
    {/if}

    <VirtualList bind:this={list} items={visible} label="Directory entries">
      {#snippet row(e: Entry)}
        <div class="row" role="row">
          {#each layout.order as id (id)}
            {@render cell(id, e)}
          {/each}
        </div>
      {/snippet}
    </VirtualList>
  </div>
</div>

<footer class="status" aria-live="polite">
  {#if loading}Loading… {entries.length.toLocaleString()} items so far
  {:else}{visible.length.toLocaleString()} of {entries.length.toLocaleString()} items{/if}
</footer>
